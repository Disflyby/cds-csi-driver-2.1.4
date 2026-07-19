package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/capitalonline/cds-csi-driver/pkg/driver/oss/mountagent"
)

func validRequest() mountagent.MountRequest {
	return mountagent.MountRequest{
		Endpoint:        "https://s3.example.test",
		Bucket:          "bucket-a",
		Prefix:          "/models/run-a",
		Target:          "/var/lib/kubelet/plugins/kubernetes.io/csi/oss.csi.cds.net/volume/globalmount",
		CredentialFile:  "/var/lib/kubelet/plugins/oss.csi.cds.net/credentials/volume.credentials",
		AddressingStyle: "path",
	}
}

func TestValidateMountRequestRejectsArbitraryPaths(t *testing.T) {
	request := validRequest()
	request.Target = "/tmp/mount"
	if err := validateMountRequest(request); err == nil {
		t.Fatal("expected arbitrary target path to be rejected")
	}
	request = validRequest()
	request.CredentialFile = "/tmp/credentials"
	if err := validateMountRequest(request); err == nil {
		t.Fatal("expected arbitrary credential path to be rejected")
	}
}

func TestGeeseFSArgsUseStructuredValues(t *testing.T) {
	request := validRequest()
	request.AddressingStyle = "virtual"
	want := []string{
		"--endpoint", "https://s3.example.test",
		"--shared-config", request.CredentialFile,
		"--profile", "default",
		"--list-type", "2",
		"--memory-limit", "256",
		"--use-enomem",
		"--sdk-max-retries", "2",
		"--http-timeout", "30s",
		"--dir-mode", "0750",
		"--file-mode", "0640",
		"--subdomain",
		"-o", "allow_other",
		"bucket-a:models/run-a", request.Target,
	}
	if got := geesefsArgs(request); !reflect.DeepEqual(got, want) {
		t.Fatalf("geesefsArgs() = %#v, want %#v", got, want)
	}
}

func TestExecuteMountReturnsGeeseFSError(t *testing.T) {
	original := runGeeseFS
	defer func() { runGeeseFS = original }()
	runGeeseFS = func(context.Context, ...string) ([]byte, error) {
		return []byte("access denied"), context.DeadlineExceeded
	}
	response := executeMount(validRequest())
	if response.Success || !strings.Contains(response.Error, "access denied") {
		t.Fatalf("unexpected response: %+v", response)
	}
}
