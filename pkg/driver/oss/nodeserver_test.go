package oss

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestS3fsMountArgsPreserveArgumentBoundaries(t *testing.T) {
	opts := &PublishOptions{OssOpts: OssOpts{Bucket: "bucket-a", URL: "https://oss.example.test", Path: "/prefix"}, NodePublishPath: "/target"}
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

func containsMountOption(args []string, option string) bool {
	for _, arg := range args {
		if arg == option {
			return true
		}
	}
	return false
}
