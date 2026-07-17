package nas

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	processedPvc sync.Map
	subpathCreateLocks     = struct {
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
	if err := opts.createDynamicNasSubDir(createVolumeRoot, pvName); err != nil {
		utils.SentrySendError(fmt.Errorf("CreateVolume:: nas, failed to create subpath on the NAS server: %s", err))
		return nil, status.Errorf(codes.Internal, "create NFS subpath: %v", err)
	}

	volumeID, err := encodeNasDynamicVolumeID(nasDynamicVolumeRef{
		Server: opts.Server,
		Path:   opts.Path,
		Vers:   opts.Vers,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encode NAS volume ID: %v", err)
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
		log.Warnf("DeleteVolume: failed to decode dynamic volume ID, falling back to k8s API: %v", decodeErr)
	}
	if dynamic {
		log.Infof("DeleteVolume: decoded dynamic volume ref server=%s path=%s vers=%s", ref.Server, ref.Path, ref.Vers)
		if err := deleteNFSSubpath(ref.Server, ref.Path, ref.Vers, deleteVolumeRoot, req.GetVolumeId(), false); err != nil {
			utils.SentrySendError(fmt.Errorf("DeleteVolume:: nas, delete NFS subpath: %s", err))
			return nil, status.Errorf(codes.Aborted, "delete NFS subpath for volume %s: %v", req.VolumeId, err)
		}
		processedPvc.Delete(req.VolumeId)
		log.Infof("DeleteVolume:: volume %s has been deleted successfully", req.VolumeId)
		return &csi.DeleteVolumeResponse{}, nil
	}

	pv, err := c.Client.CoreV1().PersistentVolumes().Get(req.VolumeId, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Warnf("DeleteVolume: PV %s not found and volume ID is not dynamic — NFS subdirectory may be orphaned", req.VolumeId)
			return &csi.DeleteVolumeResponse{}, nil
		}
		return nil, fmt.Errorf("DeleteVolume:: nas, get Volume: %s from cluster error: %s", req.VolumeId, err.Error())
	}

	// check pv's plug-in, must be not empty
	if pv.Spec.CSI == nil {
		return nil, fmt.Errorf("DeleteVolume:: Nas, Volume Spec with CSI empty: %s, pv: %v", req.VolumeId, pv)
	}
	// get pv's volumeAs, default is subpath
	var volumeAs string
	if value, ok := pv.Spec.CSI.VolumeAttributes["volumeAs"]; !ok {
		volumeAs = subpathLiteral
	} else {
		volumeAs = value
	}
	log.Debugf("DeleteVolume: Nas, volumeAs is: %s", volumeAs)

	// subpath
	if volumeAs == subpathLiteral {
		if pv.Spec.StorageClassName == "" {
			return nil, status.Errorf(codes.InvalidArgument, "volume %s has no storage class", req.VolumeId)
		}
		sc, err := c.Client.StorageV1().StorageClasses().Get(pv.Spec.StorageClassName, metav1.GetOptions{})
		if err != nil {
			return nil, status.Errorf(codes.Internal, "get storage class for volume %s: %v", req.VolumeId, err)
		}
		opts := getDeleteVolumeSubpathOptions(pv, sc)
		if err := deleteNFSSubpath(opts.Server, opts.Path, opts.Vers, deleteVolumeRoot, req.GetVolumeId(), opts.ArchiveOnDelete); err != nil {
			utils.SentrySendError(fmt.Errorf("DeleteVolume:: nas, delete NFS subpath: %s", err))
			return nil, status.Errorf(codes.Aborted, "delete NFS subpath for volume %s: %v", req.VolumeId, err)
		}
		processedPvc.Delete(req.VolumeId)
		log.Infof("DeleteVolume:: volume %s has been deleted successfully", req.VolumeId)
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
	data, err := json.Marshal(ref)
	if err != nil {
		return "", err
	}
	return nasDynamicVolumePrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeNasDynamicVolumeID(volumeID string) (nasDynamicVolumeRef, bool, error) {
	if !strings.HasPrefix(volumeID, nasDynamicVolumePrefix) {
		return nasDynamicVolumeRef{}, false, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(volumeID, nasDynamicVolumePrefix))
	if err != nil {
		return nasDynamicVolumeRef{}, true, fmt.Errorf("invalid dynamic NAS volume ID: %w", err)
	}
	ref := nasDynamicVolumeRef{}
	if err := json.Unmarshal(data, &ref); err != nil {
		return nasDynamicVolumeRef{}, true, fmt.Errorf("invalid dynamic NAS volume ID: %w", err)
	}
	if ref.Server == "" || ref.Path == "" {
		return nasDynamicVolumeRef{}, true, fmt.Errorf("incomplete dynamic NAS volume ID")
	}
	return ref, true, nil
}

