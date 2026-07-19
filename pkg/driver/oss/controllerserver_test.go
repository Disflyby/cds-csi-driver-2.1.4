package oss

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
)

func TestDynamicVolumeIDRoundTrip(t *testing.T) {
	want := dynamicVolumeRef{
		Bucket: "bucket-a", Endpoint: "https://oss.example.test",
		Path: "/ai/csi-123", AddressingStyle: ossAddressingStyleVirtual,
	}
	volumeID, err := encodeDynamicVolumeID(want)
	if err != nil {
		t.Fatalf("encodeDynamicVolumeID() error = %v", err)
	}
	got, dynamic, err := decodeDynamicVolumeID(volumeID)
	if err != nil {
		t.Fatalf("decodeDynamicVolumeID() error = %v", err)
	}
	if !dynamic || got != want {
		t.Fatalf("decodeDynamicVolumeID() = (%+v, %t), want (%+v, true)", got, dynamic, want)
	}
}

func TestNewDynamicVolumeRefUsesStableScopedPath(t *testing.T) {
	parameters := map[string]string{
		"bucket": "bucket-a", "endpoint": "https://oss.example.test", "path": "/team-a/",
	}
	first, err := newDynamicVolumeRef(parameters, "pvc-123")
	if err != nil {
		t.Fatalf("newDynamicVolumeRef() error = %v", err)
	}
	second, err := newDynamicVolumeRef(parameters, "pvc-123")
	if err != nil {
		t.Fatalf("newDynamicVolumeRef() error = %v", err)
	}
	if first.Path != second.Path || first.Path == "/team-a" {
		t.Fatalf("dynamic path must be deterministic and scoped: %q, %q", first.Path, second.Path)
	}
	if first.AddressingStyle != ossAddressingStyleAuto {
		t.Fatalf("addressing style = %q, want automatic detection", first.AddressingStyle)
	}
}

func TestNewDynamicVolumeRefRejectsParentPath(t *testing.T) {
	_, err := newDynamicVolumeRef(map[string]string{
		"bucket": "bucket-a", "endpoint": "https://oss.example.test", "path": "/team-a/../other-team",
	}, "pvc-123")
	if err == nil {
		t.Fatal("expected parent path to be rejected")
	}
}

func TestNewDynamicVolumeRefRequiresEndpointAndBucket(t *testing.T) {
	for _, parameters := range []map[string]string{
		{"bucket": "bucket-a"},
		{"endpoint": "https://oss.example.test"},
		{"bucket": "bucket-a", "endpoint": "https://oss.example.test/bucket-a"},
	} {
		if _, err := newDynamicVolumeRef(parameters, "pvc-123"); err == nil {
			t.Fatalf("expected parameters to be rejected: %#v", parameters)
		}
	}
}

func TestNewOssClientRejectsUnexpectedEndpointParts(t *testing.T) {
	_, err := newOssClient(dynamicVolumeRef{Endpoint: "https://oss.example.test?region=cn", AddressingStyle: ossAddressingStylePath}, OssCredentials{AccessKeyID: "id", AccessKeySecret: "secret"})
	if err == nil {
		t.Fatal("expected endpoint query to be rejected")
	}
}

func TestDecodeDynamicVolumeIDKeepsStaticIDsUntouched(t *testing.T) {
	_, dynamic, err := decodeDynamicVolumeID("oss-csi-pv")
	if err != nil || dynamic {
		t.Fatalf("static volume ID must remain a no-op, got dynamic=%t err=%v", dynamic, err)
	}
}

func TestCredentialsFromValues(t *testing.T) {
	credentials := credentialsFromValues(map[string]string{"akId": "id", "akSecret": "secret"})
	if !credentials.valid() {
		t.Fatal("expected credentials to be parsed")
	}
}

func TestEnsureObjectPrefixCreatesDirectoryAndOwnershipMarkers(t *testing.T) {
	putter := &recordingObjectPutter{}
	ref := dynamicVolumeRef{Bucket: "bucket-a", Path: "/team/csi-volume-a"}
	if err := ensureObjectPrefix(context.Background(), putter, ref); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"team/csi-volume-a/", "team/csi-volume-a/.csi-volume"}
	if strings.Join(putter.keys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("object keys = %v, want %v", putter.keys, wantKeys)
	}
	if putter.contentTypes[0] != "application/x-directory" {
		t.Fatalf("directory marker content type = %q", putter.contentTypes[0])
	}
}

type recordingObjectPutter struct {
	keys         []string
	contentTypes []string
}

func (p *recordingObjectPutter) PutObject(_ context.Context, _ string, key string, reader io.Reader, _ int64, options minio.PutObjectOptions) (minio.UploadInfo, error) {
	if _, err := io.ReadAll(reader); err != nil {
		return minio.UploadInfo{}, err
	}
	p.keys = append(p.keys, key)
	p.contentTypes = append(p.contentTypes, options.ContentType)
	return minio.UploadInfo{}, nil
}
