package nas

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/capitalonline/cds-csi-driver/pkg/driver/utils"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/kubernetes-csi/drivers/pkg/csi-common"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	// stores the processed pvc: key - pvname, value - *csi.Volume
	processedPvc       sync.Map
	subpathCreateLocks = struct {
		sync.Mutex
		locks map[string]*volumeCreateLock
	}{locks: make(map[string]*volumeCreateLock)}
)

type volumeCreateLock struct {
	mutex      sync.Mutex
	references int
}

// lockSubpathCreate serializes retries for one volume without blocking
// unrelated dynamic provisions during an NFS mount operation.
func lockSubpathCreate(volumeID string) func() {
	subpathCreateLocks.Lock()
	lock := subpathCreateLocks.locks[volumeID]
	if lock == nil {
		lock = &volumeCreateLock{}
		subpathCreateLocks.locks[volumeID] = lock
	}
	lock.references++
	subpathCreateLocks.Unlock()

	lock.mutex.Lock()
	return func() {
		lock.mutex.Unlock()
		subpathCreateLocks.Lock()
		lock.references--
		if lock.references == 0 {
			delete(subpathCreateLocks.locks, volumeID)
		}
		subpathCreateLocks.Unlock()
	}
}

func NewControllerServer(d *NasDriver) *ControllerServer {
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("NewControllerServer:: Failed to create kubernetes config: %v", err)
		utils.SentrySendError(fmt.Errorf("NewControllerServer:: Failed to create kubernetes config: %v", err))
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("NewControllerServer:: Failed to create kubernetes client: %v", err)
		utils.SentrySendError(fmt.Errorf("NewControllerServer:: Failed to create kubernetes client: %v", err))
	}

	return &ControllerServer{
		Client:                  clientset,
		DefaultControllerServer: csicommon.NewDefaultControllerServer(d.csiDriver),
	}
}

func (c *ControllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	log.Infof("CreateVolume: Starting NFS CreateVolume, req.Name is: %s", req.Name)
	if err := validateVolumeID(req.GetName()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid volume name: %v", err)
	}
	if err := validateDynamicSubDir(req.GetName()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid dynamic volume name: %v", err)
	}

	pvName := req.GetName()
	if value, ok := processedPvc.Load(pvName); ok && value != nil {
		log.Warnf("CreateVolume:: nas Volume %s had been created before, skip: %v", pvName, value)
		return &csi.CreateVolumeResponse{Volume: value.(*csi.Volume)}, nil
	}

	volOptions := req.GetParameters()
	volumeAs, ok := volOptions["volumeAs"]
	if !ok {
		volumeAs = subpathLiteral
	}
	if volumeAs != subpathLiteral {
		return nil, status.Errorf(codes.InvalidArgument, "unsupported volumeAs %q", volumeAs)
	}

	defer lockSubpathCreate(pvName)()
	if value, ok := processedPvc.Load(pvName); ok && value != nil {
		return &csi.CreateVolumeResponse{Volume: value.(*csi.Volume)}, nil
	}
	opts, err := parseVolumeCreateSubpathOptions(req)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "parse NFS subpath options: %v", err)
	}
	volumeID, err := encodeNasDynamicVolumeID(nasDynamicVolumeRef{
		Server:          opts.Server,
		BasePath:        opts.Path,
		SubDir:          pvName,
		Vers:            opts.Vers,
		ArchiveOnDelete: opts.ArchiveOnDelete,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encode NAS volume ID: %v", err)
	}
	if err := opts.createDynamicNasSubDir(createVolumeRoot, pvName); err != nil {
		utils.SentrySendError(fmt.Errorf("CreateVolume:: nas, failed to create subpath on the NAS server: %s", err))
		return nil, status.Errorf(codes.Internal, "create NFS subpath: %v", err)
	}

	volToCreate := &csi.Volume{
		VolumeId:      volumeID,
		CapacityBytes: req.GetCapacityRange().GetRequiredBytes(),
		VolumeContext: newSubpathVolumeContext(opts, pvName),
	}
	processedPvc.Store(pvName, volToCreate)
	log.Infof("CreateVolume:: nas, succeed provisioned pv %+v:", volToCreate)
	return &csi.CreateVolumeResponse{Volume: volToCreate}, nil
}

func (c *ControllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	log.Infof("DeleteVolume:: nas, delete volumeId(pvName) is: %s", req.GetVolumeId())
	if err := validateVolumeID(req.GetVolumeId()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid volume ID: %v", err)
	}

	// Try to decode as a dynamic VolumeID first. Dynamic volumes carry
	// their cleanup parameters inline so that DeleteVolume does not
	// depend on the PV or StorageClass still existing in the API.
	ref, dynamic, decodeErr := decodeNasDynamicVolumeID(req.GetVolumeId())
	if decodeErr != nil {
		if dynamic {
			return nil, status.Errorf(codes.FailedPrecondition, "decode NAS volume ID: %v", decodeErr)
		}
		return nil, status.Errorf(codes.InvalidArgument, "decode NAS volume ID: %v", decodeErr)
	}
	if dynamic {
		defer lockSubpathCreate(ref.SubDir)()
		log.Infof("DeleteVolume: decoded dynamic volume ref server=%s basePath=%s subDir=%s vers=%s", ref.Server, ref.BasePath, ref.SubDir, ref.Vers)
		if err := deleteNFSSubpath(ref.Server, ref.BasePath, ref.SubDir, ref.Vers, deleteVolumeRoot, ref.ArchiveOnDelete); err != nil {
			utils.SentrySendError(fmt.Errorf("DeleteVolume:: nas, delete NFS subpath: %s", err))
			return nil, status.Errorf(codes.Aborted, "delete NFS subpath for volume %s: %v", req.VolumeId, err)
		}
		processedPvc.Delete(ref.SubDir)
		log.Infof("DeleteVolume:: volume %s has been deleted successfully", req.VolumeId)
		return &csi.DeleteVolumeResponse{}, nil
	}

	pv, err := c.Client.CoreV1().PersistentVolumes().Get(req.VolumeId, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Warnf("DeleteVolume: PV %s not found and volume ID is not dynamic - NFS subdirectory may be orphaned", req.VolumeId)
			return &csi.DeleteVolumeResponse{}, nil
		}
		return nil, fmt.Errorf("DeleteVolume:: nas, get Volume: %s from cluster error: %s", req.VolumeId, err.Error())
	}

	// check pv plug-in, must be not empty
	if pv.Spec.CSI == nil {
		return nil, fmt.Errorf("DeleteVolume:: Nas, Volume Spec with CSI empty: %s, pv: %v", req.VolumeId, pv)
	}
	var volumeAs string
	if value, ok := pv.Spec.CSI.VolumeAttributes["volumeAs"]; !ok {
		volumeAs = subpathLiteral
	} else {
		volumeAs = value
	}
	log.Debugf("DeleteVolume: Nas, volumeAs is: %s", volumeAs)

	if volumeAs == subpathLiteral {
		if pv.Spec.StorageClassName == "" {
			return nil, status.Errorf(codes.InvalidArgument, "volume %s has no storage class", req.VolumeId)
		}
		sc, err := c.Client.StorageV1().StorageClasses().Get(pv.Spec.StorageClassName, metav1.GetOptions{})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "get storage class for volume %s: %v", req.VolumeId, err)
		}
		opts := getDeleteVolumeSubpathOptions(pv, sc)
		basePath, subDir, err := splitDynamicVolumePath(opts.Path)
		if err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "invalid NFS volume path for volume %s: %v", req.VolumeId, err)
		}
		if err := deleteNFSSubpath(opts.Server, basePath, subDir, opts.Vers, deleteVolumeRoot, opts.ArchiveOnDelete); err != nil {
			utils.SentrySendError(fmt.Errorf("DeleteVolume:: nas, delete NFS subpath: %s", err))
			return nil, status.Errorf(codes.Aborted, "delete NFS subpath for volume %s: %v", req.VolumeId, err)
		}
		processedPvc.Delete(req.VolumeId)
		log.Infof("DeleteVolume:: volume %s has been deleted successfully", req.VolumeId)
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func (c ControllerServer) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	for _, capability := range req.VolumeCapabilities {
		mode := capability.GetAccessMode().GetMode()
		if mode != csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER &&
			mode != csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY {
			return &csi.ValidateVolumeCapabilitiesResponse{Message: ""}, nil
		}
	}
	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: req.VolumeCapabilities,
		},
	}, nil
}

func (c ControllerServer) ControllerExpandVolume(context.Context, *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func encodeNasDynamicVolumeID(ref nasDynamicVolumeRef) (string, error) {
	if err := validateNasDynamicVolumeRef(ref); err != nil {
		return "", err
	}

	var payload bytes.Buffer
	for _, value := range []string{ref.Server, ref.BasePath, ref.SubDir, ref.Vers} {
		if err := writeCompactString(&payload, value); err != nil {
			return "", err
		}
	}
	if ref.ArchiveOnDelete {
		payload.WriteByte(1)
	} else {
		payload.WriteByte(0)
	}
	return nasDynamicVolumeV2Prefix + base64.RawURLEncoding.EncodeToString(payload.Bytes()), nil
}

func decodeNasDynamicVolumeID(volumeID string) (nasDynamicVolumeRef, bool, error) {
	if !strings.HasPrefix(volumeID, nasDynamicVolumePrefix) {
		return nasDynamicVolumeRef{}, false, nil
	}
	if !strings.HasPrefix(volumeID, nasDynamicVolumeV2Prefix) {
		return nasDynamicVolumeRef{}, true, fmt.Errorf("legacy dynamic NAS volume IDs are not supported; recreate the PVC/PV")
	}
	payload := strings.TrimPrefix(volumeID, nasDynamicVolumeV2Prefix)
	if payload == "" {
		return nasDynamicVolumeRef{}, true, fmt.Errorf("empty V2 dynamic NAS volume ID")
	}
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nasDynamicVolumeRef{}, true, fmt.Errorf("invalid V2 dynamic NAS volume ID: %w", err)
	}
	reader := bytes.NewReader(data)
	values := make([]string, 4)
	for i := range values {
		values[i], err = readCompactString(reader)
		if err != nil {
			return nasDynamicVolumeRef{}, true, fmt.Errorf("invalid V2 dynamic NAS volume ID: %w", err)
		}
	}
	archiveFlag, err := reader.ReadByte()
	if err != nil {
		return nasDynamicVolumeRef{}, true, fmt.Errorf("invalid V2 dynamic NAS volume ID: missing archive flag")
	}
	if archiveFlag > 1 || reader.Len() != 0 {
		return nasDynamicVolumeRef{}, true, fmt.Errorf("invalid V2 dynamic NAS volume ID payload")
	}
	ref := nasDynamicVolumeRef{
		Server:          values[0],
		BasePath:        values[1],
		SubDir:          values[2],
		Vers:            values[3],
		ArchiveOnDelete: archiveFlag == 1,
	}
	if err := validateNasDynamicVolumeRef(ref); err != nil {
		return nasDynamicVolumeRef{}, true, fmt.Errorf("invalid V2 dynamic NAS volume ID: %w", err)
	}
	return ref, true, nil
}

func validateNasDynamicVolumeRef(ref nasDynamicVolumeRef) error {
	if !nfsServerPattern.MatchString(ref.Server) {
		return fmt.Errorf("invalid NFS server %q", ref.Server)
	}
	if _, err := resolveDynamicVolumePath(ref.BasePath, ref.SubDir); err != nil {
		return err
	}
	if ref.Vers != "3" && ref.Vers != "4.0" && ref.Vers != "4.1" {
		return fmt.Errorf("unsupported NFS version %q", ref.Vers)
	}
	return nil
}

func writeCompactString(writer io.Writer, value string) error {
	if len(value) > int(^uint16(0)) {
		return fmt.Errorf("NAS volume ID field is too long")
	}
	if err := binary.Write(writer, binary.BigEndian, uint16(len(value))); err != nil {
		return err
	}
	_, err := io.WriteString(writer, value)
	return err
}

func readCompactString(reader *bytes.Reader) (string, error) {
	var length uint16
	if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
		return "", err
	}
	value := make([]byte, int(length))
	if _, err := io.ReadFull(reader, value); err != nil {
		return "", err
	}
	return string(value), nil
}
