package nas

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestNFSMountArgsPreserveArgumentBoundaries(t *testing.T) {
	args, err := nfsMountArgs("nfs.example.internal", "/nfsshare/pvc-123", "/var/lib/kubelet/pods/pvc-123", "4.0", "rw,noresvport", true)
	if err != nil {
		t.Fatalf("nfsMountArgs() error = %v", err)
	}
	want := []string{
		"-t", "nfs", "-o", "vers=4.0,noresvport,ro",
		"nfs.example.internal:/nfsshare/pvc-123", "/var/lib/kubelet/pods/pvc-123",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("nfsMountArgs() = %#v, want %#v", args, want)
	}
}

func TestNFSInputRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name       string
		server     string
		remotePath string
		targetPath string
		vers       string
		options    string
	}{
		{"server", "nfs;touch", "/nfsshare/pvc", "/target", "4.0", "noresvport"},
		{"remote traversal", "nfs.example", "/nfsshare/../etc", "/target", "4.0", "noresvport"},
		{"target newline", "nfs.example", "/nfsshare/pvc", "/target\nnext", "4.0", "noresvport"},
		{"version", "nfs.example", "/nfsshare/pvc", "/target", "2", "noresvport"},
		{"options", "nfs.example", "/nfsshare/pvc", "/target", "4.0", "noresvport,$(id)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := nfsMountArgs(test.server, test.remotePath, test.targetPath, test.vers, test.options, false); err == nil {
				t.Fatal("nfsMountArgs() error = nil, want validation error")
			}
		})
	}
}

func TestMountInfoParser(t *testing.T) {
	mountInfo := "36 25 0:32 / / rw,relatime - tmpfs tmpfs rw\n" +
		"42 36 0:42 / /var/lib/kubelet/pods/pvc\\040one rw,relatime - nfs nfs.example:/nfsshare rw\n"
	if !isMountPointInMountInfo(mountInfo, "/var/lib/kubelet/pods/pvc one") {
		t.Fatal("expected escaped mount point to be found")
	}
	if isMountPointInMountInfo(mountInfo, "/var/lib/kubelet/pods/missing") {
		t.Fatal("unexpected mount point match")
	}
}

func TestVolumeMarkerIsIdempotent(t *testing.T) {
	directory := t.TempDir()
	exists, err := volumeMarkerExists(directory, "pvc-123")
	if err != nil || exists {
		t.Fatalf("volumeMarkerExists() = (%t, %v), want (false, nil)", exists, err)
	}
	if err := ensureVolumeMarker(directory, "pvc-123"); err != nil {
		t.Fatalf("ensureVolumeMarker() error = %v", err)
	}
	if err := ensureVolumeMarker(directory, "pvc-123"); err != nil {
		t.Fatalf("second ensureVolumeMarker() error = %v", err)
	}
	exists, err = volumeMarkerExists(directory, "pvc-123")
	if err != nil || !exists {
		t.Fatalf("volumeMarkerExists() = (%t, %v), want (true, nil)", exists, err)
	}
	if _, err := volumeMarkerExists(directory, "pvc-other"); err == nil {
		t.Fatal("volumeMarkerExists() error = nil for a different volume")
	}

	contents, err := os.ReadFile(filepath.Join(directory, dynamicSubpathMarker))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if string(contents) != "pvc-123\n" {
		t.Fatalf("marker contents = %q", contents)
	}
}

func TestDeterministicServerSelection(t *testing.T) {
	servers := []*NfsServer{
		{Address: "nfs-a", Path: "/nfsshare/a"},
		{Address: "nfs-b", Path: "/nfsshare/b"},
		{Address: "nfs-c", Path: "/nfsshare/c"},
	}
	first := selectDeterministicNfsServer(servers, "pvc-123")
	for i := 0; i < 100; i++ {
		selected := selectDeterministicNfsServer(servers, "pvc-123")
		if selected != first {
			t.Fatalf("selection changed from %#v to %#v", first, selected)
		}
	}
	if selectDeterministicNfsServer(nil, "pvc-123") != nil {
		t.Fatal("empty server list must not select a server")
	}
}

func TestUsageThresholdNormalization(t *testing.T) {
	tests := []struct {
		value string
		want  float64
	}{
		{"0.9", 90},
		{"90", 90},
		{"1", 100},
	}
	for _, test := range tests {
		got, err := normalizeUsageThreshold(test.value)
		if err != nil || got != test.want {
			t.Fatalf("normalizeUsageThreshold(%q) = (%v, %v), want (%v, nil)", test.value, got, err, test.want)
		}
	}
	if _, err := normalizeUsageThreshold("101"); err == nil {
		t.Fatal("normalizeUsageThreshold(101) error = nil")
	}
}

func TestDeleteNFSSubpathRejectsInvalidPathBeforeMount(t *testing.T) {
	if err := deleteNFSSubpath("nfs.example", "/nfsshare/../etc", "4.0", t.TempDir(), "pvc-123", false); err == nil {
		t.Fatal("deleteNFSSubpath() error = nil for a traversal path")
	}
}

func TestDeleteNFSSubpathPropagatesMountFailure(t *testing.T) {
	originalRunNFSCommand := runNFSCommand
	runNFSCommand = func(name string, args ...string) (string, error) {
		return "", errors.New("mount failed")
	}
	defer func() { runNFSCommand = originalRunNFSCommand }()

	err := deleteNFSSubpath("nfs.example", "/nfsshare/pvc-123", "4.0", t.TempDir(), "pvc-123", false)
	if err == nil {
		t.Fatal("deleteNFSSubpath() error = nil when mount fails")
	}
}

func TestValidateVolumeID(t *testing.T) {
	for _, volumeID := range []string{"../pvc", "pvc/name", "pvc\\name", ""} {
		if err := validateVolumeID(volumeID); err == nil {
			t.Fatalf("validateVolumeID(%q) error = nil", volumeID)
		}
	}
	if err := validateVolumeID("pvc-123"); err != nil {
		t.Fatalf("validateVolumeID(valid) error = %v", err)
	}
}

func TestChangeNasModeRejectsRecursive(t *testing.T) {
	err := changeNasMode(&PublishOptions{NfsOpts: NfsOpts{Mode: "0777", ModeType: "recursive"}, NodePublishPath: "/target"})
	if err == nil {
		t.Fatal("changeNasMode() error = nil for recursive mode")
	}
}

func TestSubpathCreateLockIsScopedToVolume(t *testing.T) {
	unlockFirst := lockSubpathCreate("pvc-first")
	sameVolumeAcquired := make(chan struct{})
	go func() {
		unlock := lockSubpathCreate("pvc-first")
		close(sameVolumeAcquired)
		unlock()
	}()

	select {
	case <-sameVolumeAcquired:
		t.Fatal("same-volume lock acquired before the first lock was released")
	case <-time.After(50 * time.Millisecond):
	}

	differentVolumeAcquired := make(chan struct{})
	go func() {
		unlock := lockSubpathCreate("pvc-second")
		close(differentVolumeAcquired)
		unlock()
	}()
	select {
	case <-differentVolumeAcquired:
	case <-time.After(time.Second):
		t.Fatal("different-volume lock was blocked")
	}

	unlockFirst()
	select {
	case <-sameVolumeAcquired:
	case <-time.After(time.Second):
		t.Fatal("same-volume lock did not acquire after release")
	}
}
