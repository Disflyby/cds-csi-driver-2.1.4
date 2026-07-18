package oss

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

func TestCredentialFileNameIsIsolatedPerMount(t *testing.T) {
	first := credentialFileName("volume-a", "/var/lib/kubelet/pods/a/mount")
	second := credentialFileName("volume-a", "/var/lib/kubelet/pods/b/mount")
	if first == second {
		t.Fatal("credential file names must differ for separate publish targets")
	}
	if !strings.HasSuffix(first, ".passwd") {
		t.Fatalf("credential file name %q does not have the expected suffix", first)
	}
}

func TestWriteAndRemoveOssCredential(t *testing.T) {
	credentialFile := filepath.Join(t.TempDir(), "credentials", "volume.passwd")
	credentials := OssCredentials{AccessKeyID: "access-key", AccessKeySecret: "access-secret"}
	if err := writeOssCredential(credentialFile, credentials); err != nil {
		t.Fatalf("writeOssCredential() error = %v", err)
	}
	info, err := os.Stat(credentialFile)
	if err != nil {
		t.Fatalf("stat credential file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("credential permissions = %o, want 0600", info.Mode().Perm())
	}
	contents, err := os.ReadFile(credentialFile)
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	if string(contents) != "access-key:access-secret\n" {
		t.Fatalf("credential file has unexpected contents %q", contents)
	}
	if err := removeOssCredential(credentialFile); err != nil {
		t.Fatalf("removeOssCredential() error = %v", err)
	}
	if _, err := os.Stat(credentialFile); !os.IsNotExist(err) {
		t.Fatalf("credential file still exists, stat error = %v", err)
	}
}

func TestParseOssOptsRejectsUnsafeEndpointAndBucket(t *testing.T) {
	unsafeEndpoint := &OssOpts{Bucket: "bucket-a", URL: "https://oss.example.test/other"}
	if err := unsafeEndpoint.parsOssOpts(); err == nil {
		t.Fatal("expected endpoint path to be rejected")
	}
	unsafeBucket := &OssOpts{Bucket: "bucket;rm", URL: "https://oss.example.test"}
	if err := unsafeBucket.parsOssOpts(); err == nil {
		t.Fatal("expected unsafe bucket name to be rejected")
	}
	unsafeStyle := &OssOpts{Bucket: "bucket-a", URL: "https://oss.example.test", AddressingStyle: "other"}
	if err := unsafeStyle.parsOssOpts(); err == nil {
		t.Fatal("expected unsupported addressing style to be rejected")
	}
}

func TestParseOssOptsRejectsBadSignatureType(t *testing.T) {
	unsafeSig := &OssOpts{Bucket: "bucket-a", URL: "https://oss.example.test", SignatureType: "v3"}
	if err := unsafeSig.parsOssOpts(); err == nil {
		t.Fatal("expected unsupported signature type to be rejected")
	}
}

func TestParseOssOptsRejectsConflictingEndpointAliases(t *testing.T) {
	opts := &OssOpts{
		Bucket: "bucket-a", Endpoint: "https://s3.example.test", URL: "https://other.example.test",
	}
	if err := opts.parsOssOpts(); err == nil {
		t.Fatal("expected conflicting endpoint and url values to be rejected")
	}
}

func TestParseOssOptsAcceptsRegionAndSignature(t *testing.T) {
	opts := &OssOpts{Bucket: "bucket-a", URL: "https://oss.example.test", Region: "cn-east-1", SignatureType: "v2"}
	if err := opts.parsOssOpts(); err != nil {
		t.Fatalf("parsOssOpts() error = %v", err)
	}
	if opts.Region != "cn-east-1" {
		t.Fatalf("region = %q, want cn-east-1", opts.Region)
	}
	if opts.SignatureType != "v2" {
		t.Fatalf("signature type = %q, want v2", opts.SignatureType)
	}
}

func TestS3fsMountArgsPreserveArgumentBoundaries(t *testing.T) {
	opts := &PublishOptions{OssOpts: OssOpts{Bucket: "bucket-a", URL: "https://oss.example.test", Path: "/prefix"}, NodePublishPath: "/target"}
	if err := opts.parsOssOpts(); err != nil {
		t.Fatal(err)
	}
	args := s3fsMountArgs(opts, "/credentials/volume.passwd")
	want := []string{"bucket-a:/prefix", "/target", "-o", "passwd_file=/credentials/volume.passwd", "-o", "url=https://oss.example.test"}
	for index, value := range want {
		if args[index] != value {
			t.Fatalf("args[%d] = %q, want %q", index, args[index], value)
		}
	}
	if !containsMountOption(args, "use_path_request_style") {
		t.Fatal("path-style mounts must include use_path_request_style")
	}
}

func TestNodeCapabilitiesIncludeStageUnstage(t *testing.T) {
	node := &NodeServer{}
	response, err := node.NodeGetCapabilities(context.Background(), &csi.NodeGetCapabilitiesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Capabilities) != 1 || response.Capabilities[0].GetRpc().GetType() != csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME {
		t.Fatalf("unexpected node capabilities: %+v", response.Capabilities)
	}
}

func TestNodeStageDefersWhenLegacyPVHasNoStageSecret(t *testing.T) {
	mounter := newFakeMounter()
	node := &NodeServer{mounter: mounter}
	response, err := node.NodeStageVolume(context.Background(), &csi.NodeStageVolumeRequest{
		VolumeId: "volume-a", StagingTargetPath: filepath.Join(t.TempDir(), "stage"),
	})
	if err != nil || response == nil {
		t.Fatalf("NodeStageVolume() response=%v error=%v", response, err)
	}
	if len(mounter.mounts) != 0 {
		t.Fatal("NodeStageVolume must not mount without a stage secret")
	}
}

func TestNodePublishBindsExistingStagingMount(t *testing.T) {
	mounter := newFakeMounter()
	root := t.TempDir()
	stagingTarget := filepath.Join(root, "stage")
	publishTarget := filepath.Join(root, "publish")
	mounter.mounted[stagingTarget] = true
	node := &NodeServer{mounter: mounter}
	_, err := node.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
		VolumeId: "volume-a", StagingTargetPath: stagingTarget, TargetPath: publishTarget, Readonly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(mounter.binds) != 1 || mounter.binds[0] != stagingTarget+"->"+publishTarget+":ro" {
		t.Fatalf("bind calls = %v", mounter.binds)
	}
	if len(mounter.mounts) != 0 {
		t.Fatal("NodePublishVolume must not create another s3fs mount when staging is mounted")
	}
}

func TestNodeStageChecksExistingStagingMountReadiness(t *testing.T) {
	mounter := newFakeMounter()
	stagingTarget := filepath.Join(t.TempDir(), "stage")
	mounter.mounted[stagingTarget] = true
	node := &NodeServer{mounter: mounter}

	_, err := node.NodeStageVolume(context.Background(), &csi.NodeStageVolumeRequest{
		VolumeId: "volume-a", StagingTargetPath: stagingTarget,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(mounter.readyChecks) != 1 || mounter.readyChecks[0] != stagingTarget {
		t.Fatalf("ready checks = %v", mounter.readyChecks)
	}
}

func TestS3fsMountArgsUseVirtualHostStyle(t *testing.T) {
	opts := &PublishOptions{OssOpts: OssOpts{Bucket: "bucket-a", URL: "https://oss.example.test", Path: "/prefix", AddressingStyle: "virtual"}, NodePublishPath: "/target"}
	if err := opts.parsOssOpts(); err != nil {
		t.Fatal(err)
	}
	args := s3fsMountArgs(opts, "/credentials/volume.passwd")
	if containsMountOption(args, "use_path_request_style") {
		t.Fatal("virtual-host-style mounts must not include use_path_request_style")
	}
}

func TestS3fsMountArgsIncludeRegionAndSignature(t *testing.T) {
	opts := &PublishOptions{OssOpts: OssOpts{
		Bucket: "bucket-a", URL: "https://oss.example.test", Path: "/prefix",
		Region: "cn-east-1", SignatureType: "v2",
	}, NodePublishPath: "/target"}
	if err := opts.parsOssOpts(); err != nil {
		t.Fatal(err)
	}
	args := s3fsMountArgs(opts, "/credentials/volume.passwd")
	if !containsMountOption(args, "region=cn-east-1") {
		t.Fatal("mount args must include region")
	}
	if !containsMountOption(args, "sigv2") {
		t.Fatal("mount args must include sigv2 for v2 signature")
	}
}

func TestS3fsMountArgsIncludeCompatibilityAndTimeouts(t *testing.T) {
	opts := &PublishOptions{OssOpts: OssOpts{
		Bucket: "bucket-a", URL: "https://oss.example.test", Path: "/prefix",
	}, NodePublishPath: "/target"}
	if err := opts.parsOssOpts(); err != nil {
		t.Fatal(err)
	}
	args := s3fsMountArgs(opts, "/credentials/volume.passwd")
	for _, option := range []string{"compat_dir", "connect_timeout=10", "readwrite_timeout=30", "retries=2"} {
		if !containsMountOption(args, option) {
			t.Fatalf("mount args must include %q", option)
		}
	}
}

func TestVerifyStagingMountRollsBackFailedRead(t *testing.T) {
	mounter := newFakeMounter()
	mounter.mounted["/stage"] = true
	mounter.readyErr = errors.New("read timed out")
	node := &NodeServer{mounter: mounter}

	err := node.verifyStagingMount(context.Background(), "/stage")
	if err == nil || !strings.Contains(err.Error(), "read timed out") {
		t.Fatalf("verifyStagingMount() error = %v", err)
	}
	if len(mounter.readyChecks) != 1 || mounter.readyChecks[0] != "/stage" {
		t.Fatalf("ready checks = %v", mounter.readyChecks)
	}
	if len(mounter.lazyUnmounts) != 1 || mounter.lazyUnmounts[0] != "/stage" {
		t.Fatalf("lazy unmounts = %v", mounter.lazyUnmounts)
	}
}

func containsMountOption(args []string, option string) bool {
	for _, arg := range args {
		if arg == option {
			return true
		}
	}
	return false
}

type fakeMounter struct {
	mounted      map[string]bool
	mounts       []string
	binds        []string
	unmounts     []string
	lazyUnmounts []string
	readyChecks  []string
	readyErr     error
}

func newFakeMounter() *fakeMounter {
	return &fakeMounter{mounted: map[string]bool{}}
}

func (m *fakeMounter) Mount(_ context.Context, opts *PublishOptions, _ string) error {
	m.mounts = append(m.mounts, opts.NodePublishPath)
	m.mounted[opts.NodePublishPath] = true
	return nil
}

func (m *fakeMounter) BindMount(_ context.Context, source, target string, readOnly bool) error {
	mode := "rw"
	if readOnly {
		mode = "ro"
	}
	m.binds = append(m.binds, source+"->"+target+":"+mode)
	m.mounted[target] = true
	return nil
}

func (m *fakeMounter) Unmount(_ context.Context, target string) error {
	m.unmounts = append(m.unmounts, target)
	delete(m.mounted, target)
	return nil
}

func (m *fakeMounter) UnmountLazy(_ context.Context, target string) error {
	m.lazyUnmounts = append(m.lazyUnmounts, target)
	delete(m.mounted, target)
	return nil
}

func (m *fakeMounter) CheckReady(_ context.Context, target string) error {
	m.readyChecks = append(m.readyChecks, target)
	return m.readyErr
}

func (m *fakeMounter) IsMounted(target string) (bool, error) {
	return m.mounted[target], nil
}
