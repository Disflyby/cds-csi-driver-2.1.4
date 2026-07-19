package nas

import (
	"context"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newNasCreateVolumeRequest(archiveOnDelete string) *csi.CreateVolumeRequest {
	return &csi.CreateVolumeRequest{
		Name: "pvc-123",
		Parameters: map[string]string{
			"server":          "nfs.example",
			"path":            "/",
			"vers":            "4.0",
			"volumeAs":        "subpath",
			"archiveOnDelete": archiveOnDelete,
		},
	}
}

func TestNasDynamicVolumeIDV2RoundTrip(t *testing.T) {
	for _, archiveOnDelete := range []bool{false, true} {
		want := nasDynamicVolumeRef{
			Server:          "106.3.141.78",
			BasePath:        "/",
			SubDir:          "pvc-a5d2effd-f906-413e-a014-43d55c1dfae8",
			Vers:            "4.0",
			ArchiveOnDelete: archiveOnDelete,
		}
		volumeID, err := encodeNasDynamicVolumeID(want)
		if err != nil {
			t.Fatalf("encodeNasDynamicVolumeID() error = %v", err)
		}
		if !strings.HasPrefix(volumeID, nasDynamicVolumeV2Prefix) {
			t.Fatalf("volume ID %q does not have V2 prefix", volumeID)
		}
		if len(volumeID) > 128 {
			t.Fatalf("volume ID length = %d, want <= 128", len(volumeID))
		}
		got, dynamic, err := decodeNasDynamicVolumeID(volumeID)
		if err != nil || !dynamic {
			t.Fatalf("decodeNasDynamicVolumeID() = (%#v, %t, %v)", got, dynamic, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("decoded ref = %#v, want %#v", got, want)
		}
	}
}

func TestDecodeNasDynamicVolumeIDRejectsLegacyFormat(t *testing.T) {
	_, dynamic, err := decodeNasDynamicVolumeID(nasDynamicVolumePrefix + "eyJzZXJ2ZXIiOiJuZnMifQ")
	if !dynamic || err == nil {
		t.Fatalf("decodeNasDynamicVolumeID() dynamic = %t, error = %v; want legacy rejection", dynamic, err)
	}
	if !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("legacy error = %q", err)
	}
}

func TestDeleteVolumeRejectsLegacyDynamicVolumeID(t *testing.T) {
	controller := &ControllerServer{}
	_, err := controller.DeleteVolume(context.Background(), &csi.DeleteVolumeRequest{
		VolumeId: nasDynamicVolumePrefix + "eyJzZXJ2ZXIiOiJuZnMifQ",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("DeleteVolume() code = %v, error = %v; want %v", status.Code(err), err, codes.FailedPrecondition)
	}
}

func TestCreateVolumeRejectsInvalidDynamicSubDir(t *testing.T) {
	controller := &ControllerServer{}
	_, err := controller.CreateVolume(context.Background(), &csi.CreateVolumeRequest{Name: "pvc..child"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateVolume() code = %v, error = %v; want %v", status.Code(err), err, codes.InvalidArgument)
	}
}

func TestDecodeNasDynamicVolumeIDIgnoresNonDynamicID(t *testing.T) {
	_, dynamic, err := decodeNasDynamicVolumeID("pvc-123")
	if dynamic || err != nil {
		t.Fatalf("decodeNasDynamicVolumeID() dynamic = %t, error = %v", dynamic, err)
	}
}

func TestDecodeNasDynamicVolumeIDRejectsTrailingData(t *testing.T) {
	ref := nasDynamicVolumeRef{
		Server:   "nfs.example",
		BasePath: "/exports",
		SubDir:   "pvc-123",
		Vers:     "4.0",
	}
	volumeID, err := encodeNasDynamicVolumeID(ref)
	if err != nil {
		t.Fatalf("encodeNasDynamicVolumeID() error = %v", err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(volumeID, nasDynamicVolumeV2Prefix))
	if err != nil {
		t.Fatalf("decode test payload: %v", err)
	}
	payload = append(payload, 0)
	tamperedID := nasDynamicVolumeV2Prefix + base64.RawURLEncoding.EncodeToString(payload)
	if _, dynamic, err := decodeNasDynamicVolumeID(tamperedID); !dynamic || err == nil {
		t.Fatalf("decodeNasDynamicVolumeID() dynamic = %t, error = %v; want invalid payload", dynamic, err)
	}
}

func TestParseVolumeCreateSubpathOptionsArchiveOnDelete(t *testing.T) {
	req := newNasCreateVolumeRequest("true")
	opts, err := parseVolumeCreateSubpathOptions(req)
	if err != nil {
		t.Fatalf("parseVolumeCreateSubpathOptions() error = %v", err)
	}
	if !opts.ArchiveOnDelete {
		t.Fatal("ArchiveOnDelete = false, want true")
	}

	req = newNasCreateVolumeRequest("not-a-bool")
	if _, err := parseVolumeCreateSubpathOptions(req); err == nil {
		t.Fatal("parseVolumeCreateSubpathOptions() error = nil for invalid archiveOnDelete")
	}
}
