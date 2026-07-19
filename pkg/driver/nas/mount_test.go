package nas

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func TestParseVolumeCreateSubpathOptionsPreservesExportPath(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "root export", path: "/"},
		{name: "nested export", path: "/exports/team-a"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts, err := parseVolumeCreateSubpathOptions(&csi.CreateVolumeRequest{
				Name: "pvc-123",
				Parameters: map[string]string{
					"server":   "nfs.example.internal",
					"path":     test.path,
					"vers":     "4.0",
					"volumeAs": "subpath",
				},
			})
			if err != nil {
				t.Fatalf("parseVolumeCreateSubpathOptions() error = %v", err)
			}
			if opts.Server != "nfs.example.internal" || opts.Path != test.path {
				t.Fatalf("options = server %q, path %q; want server %q, path %q", opts.Server, opts.Path, "nfs.example.internal", test.path)
			}
		})
	}
}

func TestNFSOptionsRejectEmptyExportPath(t *testing.T) {
	opts := &NfsOpts{Server: "nfs.example.internal"}
	if err := opts.parsNfsOpts(); err == nil {
		t.Fatal("parsNfsOpts() error = nil for an empty export path")
	}
}

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

func TestDeleteNFSSubpathRejectsInvalidPathBeforeMount(t *testing.T) {
	if err := deleteNFSSubpath("nfs.example", "/nfsshare", "../etc", "4.0", t.TempDir(), false); err == nil {
		t.Fatal("deleteNFSSubpath() error = nil for a traversal path")
	}
}

func TestCreateDynamicNasSubDirRejectsExistingUnmarkedDirectory(t *testing.T) {
	originalRunNFSCommand := runNFSCommand
	runNFSCommand = func(name string, args ...string) (string, error) {
		return "", nil
	}
	defer func() { runNFSCommand = originalRunNFSCommand }()

	mountRoot := t.TempDir()
	remoteSubDir := filepath.Join(mountRoot, "pvc-123", "pvc-123")
	if err := os.MkdirAll(remoteSubDir, 0755); err != nil {
		t.Fatalf("create existing remote subdirectory: %v", err)
	}
	opts := &NfsOpts{Server: "nfs.example", Path: "/", Vers: "4.0"}
	if err := opts.createDynamicNasSubDir(mountRoot, "pvc-123"); err == nil {
		t.Fatal("createDynamicNasSubDir() error = nil for an existing unmarked directory")
	}
	if _, err := os.Stat(filepath.Join(remoteSubDir, dynamicSubpathMarker)); !os.IsNotExist(err) {
		t.Fatalf("unexpected marker was created, stat error = %v", err)
	}
}

func TestCreateDynamicNasSubDirAcceptsMatchingMarker(t *testing.T) {
	originalRunNFSCommand := runNFSCommand
	runNFSCommand = func(name string, args ...string) (string, error) {
		return "", nil
	}
	defer func() { runNFSCommand = originalRunNFSCommand }()

	mountRoot := t.TempDir()
	remoteSubDir := filepath.Join(mountRoot, "pvc-123", "pvc-123")
	if err := os.MkdirAll(remoteSubDir, 0755); err != nil {
		t.Fatalf("create existing remote subdirectory: %v", err)
	}
	if err := ensureVolumeMarker(remoteSubDir, "pvc-123"); err != nil {
		t.Fatalf("create marker: %v", err)
	}
	opts := &NfsOpts{Server: "nfs.example", Path: "/", Vers: "4.0"}
	if err := opts.createDynamicNasSubDir(mountRoot, "pvc-123"); err != nil {
		t.Fatalf("createDynamicNasSubDir() error = %v", err)
	}
}

func TestDeleteNFSSubpathPropagatesMountFailure(t *testing.T) {
	originalRunNFSCommand := runNFSCommand
	runNFSCommand = func(name string, args ...string) (string, error) {
		return "", errors.New("mount failed")
	}
	defer func() { runNFSCommand = originalRunNFSCommand }()

	err := deleteNFSSubpath("nfs.example", "/nfsshare", "pvc-123", "4.0", t.TempDir(), false)
	if err == nil {
		t.Fatal("deleteNFSSubpath() error = nil when mount fails")
	}
}

func TestDeleteNFSSubpathRequiresMatchingMarker(t *testing.T) {
	originalRunNFSCommand := runNFSCommand
	runNFSCommand = func(name string, args ...string) (string, error) {
		return "", nil
	}
	defer func() { runNFSCommand = originalRunNFSCommand }()

	mountRoot := t.TempDir()
	deletePath := filepath.Join(mountRoot, "pvc-123-delete", "pvc-123")
	if err := os.MkdirAll(deletePath, 0755); err != nil {
		t.Fatalf("create delete path: %v", err)
	}
	if err := ensureVolumeMarker(deletePath, "pvc-other"); err != nil {
		t.Fatalf("create mismatched marker: %v", err)
	}

	err := deleteNFSSubpath("nfs.example", "/", "pvc-123", "4.0", mountRoot, false)
	if err == nil {
		t.Fatal("deleteNFSSubpath() error = nil for a mismatched marker")
	}
	if _, err := os.Stat(deletePath); err != nil {
		t.Fatalf("delete path was changed despite marker mismatch: %v", err)
	}
}

func TestDeleteNFSSubpathRejectsMissingMarker(t *testing.T) {
	originalRunNFSCommand := runNFSCommand
	runNFSCommand = func(name string, args ...string) (string, error) {
		return "", nil
	}
	defer func() { runNFSCommand = originalRunNFSCommand }()

	mountRoot := t.TempDir()
	deletePath := filepath.Join(mountRoot, "pvc-123-delete", "pvc-123")
	if err := os.MkdirAll(deletePath, 0755); err != nil {
		t.Fatalf("create delete path: %v", err)
	}

	if err := deleteNFSSubpath("nfs.example", "/", "pvc-123", "4.0", mountRoot, false); err == nil {
		t.Fatal("deleteNFSSubpath() error = nil for a missing marker")
	}
	if _, err := os.Stat(deletePath); err != nil {
		t.Fatalf("delete path was changed despite missing marker: %v", err)
	}
}

func TestDeleteNFSSubpathDeletesMatchingDirectory(t *testing.T) {
	originalRunNFSCommand := runNFSCommand
	var mountArgs []string
	runNFSCommand = func(name string, args ...string) (string, error) {
		if name == "mount" {
			mountArgs = append([]string(nil), args...)
		}
		return "", nil
	}
	defer func() { runNFSCommand = originalRunNFSCommand }()

	mountRoot := t.TempDir()
	deletePath := filepath.Join(mountRoot, "pvc-123-delete", "pvc-123")
	if err := os.MkdirAll(deletePath, 0755); err != nil {
		t.Fatalf("create delete path: %v", err)
	}
	if err := ensureVolumeMarker(deletePath, "pvc-123"); err != nil {
		t.Fatalf("create marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(deletePath, "data"), []byte("payload"), 0600); err != nil {
		t.Fatalf("create volume data: %v", err)
	}

	if err := deleteNFSSubpath("nfs.example", "/", "pvc-123", "4.0", mountRoot, false); err != nil {
		t.Fatalf("deleteNFSSubpath() error = %v", err)
	}
	if _, err := os.Stat(deletePath); !os.IsNotExist(err) {
		t.Fatalf("delete path still exists, stat error = %v", err)
	}
	wantSource := "nfs.example:/"
	if len(mountArgs) < 2 || mountArgs[len(mountArgs)-2] != wantSource {
		t.Fatalf("mount args = %#v, want source %q", mountArgs, wantSource)
	}
}

func TestDeleteNFSSubpathMissingDirectoryIsIdempotent(t *testing.T) {
	originalRunNFSCommand := runNFSCommand
	runNFSCommand = func(name string, args ...string) (string, error) {
		return "", nil
	}
	defer func() { runNFSCommand = originalRunNFSCommand }()

	if err := deleteNFSSubpath("nfs.example", "/exports/team-a", "pvc-missing", "4.0", t.TempDir(), false); err != nil {
		t.Fatalf("deleteNFSSubpath() error for missing directory = %v", err)
	}
}

func TestDeleteNFSSubpathArchivesMatchingDirectory(t *testing.T) {
	originalRunNFSCommand := runNFSCommand
	runNFSCommand = func(name string, args ...string) (string, error) {
		return "", nil
	}
	defer func() { runNFSCommand = originalRunNFSCommand }()

	mountRoot := t.TempDir()
	mountPoint := filepath.Join(mountRoot, "pvc-123-delete")
	deletePath := filepath.Join(mountPoint, "pvc-123")
	if err := os.MkdirAll(deletePath, 0755); err != nil {
		t.Fatalf("create delete path: %v", err)
	}
	if err := ensureVolumeMarker(deletePath, "pvc-123"); err != nil {
		t.Fatalf("create marker: %v", err)
	}

	if err := deleteNFSSubpath("nfs.example", "/exports", "pvc-123", "4.0", mountRoot, true); err != nil {
		t.Fatalf("deleteNFSSubpath() archive error = %v", err)
	}
	if _, err := os.Stat(deletePath); !os.IsNotExist(err) {
		t.Fatalf("original delete path still exists, stat error = %v", err)
	}
	archives, err := filepath.Glob(filepath.Join(mountPoint, "archived-pvc-123.*"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("archived paths = %#v, error = %v", archives, err)
	}
	exists, err := volumeMarkerExists(archives[0], "pvc-123")
	if err != nil || !exists {
		t.Fatalf("archived marker = (%t, %v), want (true, nil)", exists, err)
	}
}

func TestResolveDynamicVolumePath(t *testing.T) {
	volumePath, err := resolveDynamicVolumePath("/", "pvc-123")
	if err != nil {
		t.Fatalf("resolveDynamicVolumePath() error = %v", err)
	}
	if volumePath != "/pvc-123" {
		t.Fatalf("resolveDynamicVolumePath() = %q, want %q", volumePath, "/pvc-123")
	}

	for _, subDir := range []string{"", ".", "..", "pvc/child", `pvc\\child`, "pvc..child"} {
		if _, err := resolveDynamicVolumePath("/exports", subDir); err == nil {
			t.Fatalf("resolveDynamicVolumePath(%q) error = nil", subDir)
		}
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
