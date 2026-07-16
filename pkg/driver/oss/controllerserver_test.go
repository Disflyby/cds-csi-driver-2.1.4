package oss

import "testing"

func TestDynamicVolumeIDRoundTrip(t *testing.T) {
	want := dynamicVolumeRef{Bucket: "bucket-a", URL: "https://oss.example.test", Path: "/ai/csi-123"}
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
		"bucket": "bucket-a",
		"url":    "https://oss.example.test",
		"path":   "/team-a/",
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
}

func TestNewDynamicVolumeRefRejectsParentPath(t *testing.T) {
	_, err := newDynamicVolumeRef(map[string]string{
		"bucket": "bucket-a",
		"url":    "https://oss.example.test",
		"path":   "/team-a/../other-team",
	}, "pvc-123")
	if err == nil {
		t.Fatal("expected parent path to be rejected")
	}
}

func TestNewOssClientRejectsUnexpectedURLParts(t *testing.T) {
	_, err := newOssClient("https://oss.example.test?region=cn", OssCredentials{AccessKeyID: "id", AccessKeySecret: "secret"})
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
