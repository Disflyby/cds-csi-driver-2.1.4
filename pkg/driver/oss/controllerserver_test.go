package oss

import "testing"

func TestDynamicVolumeIDRoundTrip(t *testing.T) {
	want := dynamicVolumeRef{
		Bucket: "bucket-a", Endpoint: "https://oss.example.test", URL: "https://oss.example.test",
		Path: "/ai/csi-123", EndpointMode: "service", AddressingStyle: "virtual",
		Region: "cn-east-1", SignatureType: "v4", Mounter: "s3fs",
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
		"bucket":          "bucket-a",
		"url":             "https://oss.example.test",
		"path":            "/team-a/",
		"addressingStyle": "virtual",
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
	if first.AddressingStyle != ossAddressingStyleVirtual {
		t.Fatalf("addressing style = %q, want virtual", first.AddressingStyle)
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
	_, err := newOssClient(dynamicVolumeRef{Endpoint: "https://oss.example.test?region=cn", AddressingStyle: ossAddressingStylePath}, OssCredentials{AccessKeyID: "id", AccessKeySecret: "secret"})
	if err == nil {
		t.Fatal("expected endpoint query to be rejected")
	}
}

func TestNewDynamicVolumeRefDefaultsToPathStyle(t *testing.T) {
	ref, err := newDynamicVolumeRef(map[string]string{"bucket": "bucket-a", "url": "https://oss.example.test"}, "pvc-123")
	if err != nil {
		t.Fatal(err)
	}
	if ref.AddressingStyle != ossAddressingStylePath {
		t.Fatalf("addressing style = %q, want path", ref.AddressingStyle)
	}
}

func TestNewDynamicVolumeRefDefaultsSignatureType(t *testing.T) {
	ref, err := newDynamicVolumeRef(map[string]string{"bucket": "bucket-a", "url": "https://oss.example.test"}, "pvc-123")
	if err != nil {
		t.Fatal(err)
	}
	if ref.SignatureType != defaultOSSSignatureType {
		t.Fatalf("signature type = %q, want %q", ref.SignatureType, defaultOSSSignatureType)
	}
}

func TestNewDynamicVolumeRefAcceptsRegionAndSignature(t *testing.T) {
	ref, err := newDynamicVolumeRef(map[string]string{
		"bucket":        "bucket-a",
		"url":           "https://oss.example.test",
		"region":        "cn-east-1",
		"signatureType": "v2",
	}, "pvc-123")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Region != "cn-east-1" {
		t.Fatalf("region = %q, want cn-east-1", ref.Region)
	}
	if ref.SignatureType != ossSignatureTypeV2 {
		t.Fatalf("signature type = %q, want v2", ref.SignatureType)
	}
}

func TestNewDynamicVolumeRefNormalizesVirtualBucketEndpoint(t *testing.T) {
	ref, err := newDynamicVolumeRef(map[string]string{
		"endpoint":     "https://training-data.oss-cn-beijing.aliyuncs.com",
		"endpointMode": "bucket",
	}, "pvc-123")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Bucket != "training-data" {
		t.Fatalf("bucket = %q, want training-data", ref.Bucket)
	}
	if ref.Endpoint != "https://oss-cn-beijing.aliyuncs.com" {
		t.Fatalf("endpoint = %q", ref.Endpoint)
	}
	if ref.AddressingStyle != ossAddressingStyleVirtual {
		t.Fatalf("addressing style = %q, want virtual", ref.AddressingStyle)
	}
	if ref.EndpointMode != ossEndpointModeService {
		t.Fatalf("canonical endpoint mode = %q, want service", ref.EndpointMode)
	}
	canonical := ref.ossOpts()
	if err := canonical.parsOssOpts(); err != nil {
		t.Fatalf("canonical bucket endpoint must be safe to parse again: %v", err)
	}
}

func TestNewDynamicVolumeRefNormalizesPathBucketEndpoint(t *testing.T) {
	ref, err := newDynamicVolumeRef(map[string]string{
		"endpoint":     "https://minio.example.test/training-data",
		"endpointMode": "bucket",
	}, "pvc-123")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Bucket != "training-data" || ref.Endpoint != "https://minio.example.test" {
		t.Fatalf("normalized ref = %+v", ref)
	}
	if ref.AddressingStyle != ossAddressingStylePath {
		t.Fatalf("addressing style = %q, want path", ref.AddressingStyle)
	}
	if ref.EndpointMode != ossEndpointModeService {
		t.Fatalf("canonical endpoint mode = %q, want service", ref.EndpointMode)
	}
}

func TestNewDynamicVolumeRefRejectsOpaqueBucketEndpoint(t *testing.T) {
	_, err := newDynamicVolumeRef(map[string]string{
		"endpoint":     "https://objects.example.test",
		"endpointMode": "bucket",
		"bucket":       "training-data",
	}, "pvc-123")
	if err == nil {
		t.Fatal("expected opaque bucket endpoint to be rejected")
	}
}

func TestNewDynamicVolumeRefAcceptsAutoAddressing(t *testing.T) {
	ref, err := newDynamicVolumeRef(map[string]string{
		"endpoint":        "https://s3.example.test",
		"bucket":          "training-data",
		"addressingStyle": "auto",
	}, "pvc-123")
	if err != nil {
		t.Fatal(err)
	}
	if ref.AddressingStyle != ossAddressingStyleAuto {
		t.Fatalf("addressing style = %q, want auto", ref.AddressingStyle)
	}
}

func TestNewDynamicVolumeRefRejectsBadSignatureType(t *testing.T) {
	_, err := newDynamicVolumeRef(map[string]string{
		"bucket":        "bucket-a",
		"url":           "https://oss.example.test",
		"signatureType": "v3",
	}, "pvc-123")
	if err == nil {
		t.Fatal("expected bad signature type to be rejected")
	}
}

func TestDecodeLegacyDynamicVolumeIDDefaultsToPathStyle(t *testing.T) {
	volumeID, err := encodeDynamicVolumeID(dynamicVolumeRef{Bucket: "bucket-a", URL: "https://oss.example.test", Path: "/csi-legacy"})
	if err != nil {
		t.Fatal(err)
	}
	ref, dynamic, err := decodeDynamicVolumeID(volumeID)
	if err != nil || !dynamic {
		t.Fatalf("decode legacy volume ID: dynamic=%t err=%v", dynamic, err)
	}
	if ref.AddressingStyle != ossAddressingStylePath {
		t.Fatalf("legacy addressing style = %q, want path", ref.AddressingStyle)
	}
	if ref.SignatureType != defaultOSSSignatureType {
		t.Fatalf("legacy signature type = %q, want %q", ref.SignatureType, defaultOSSSignatureType)
	}
	if ref.Endpoint != "https://oss.example.test" || ref.EndpointMode != defaultOSSEndpointMode {
		t.Fatalf("legacy endpoint was not normalized: %+v", ref)
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
