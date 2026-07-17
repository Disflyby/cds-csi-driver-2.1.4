package nas

import (
	"context"
	"fmt"
	"github.com/capitalonline/cds-csi-driver/pkg/driver/utils"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/kubernetes-csi/drivers/pkg/csi-common"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
)

func NewNodeServer(d *NasDriver) *NodeServer {
	return &NodeServer{
		DefaultNodeServer: csicommon.NewDefaultNodeServer(d.csiDriver),
	}
}

func (n *NodeServer) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	nodeCap := &csi.NodeServiceCapability{
		Type: &csi.NodeServiceCapability_Rpc{
			Rpc: &csi.NodeServiceCapability_RPC{
				Type: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
			},
		},
	}

	// Disk Metric enable config
	nodeSvcCap := []*csi.NodeServiceCapability{nodeCap}

	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: nodeSvcCap,
	}, nil
}

func (n *NodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	log.Infof("NodePublishVolume:: starting mount nas volume with req: %+v", req)
	opts, err := parsePublishOptions(req)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "parse NFS mount options: %v", err)
	}
	mounted, err := isMountPoint(opts.NodePublishPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "check NFS mount point %s: %v", opts.NodePublishPath, err)
	}
	if mounted {
		log.Warnf("NodePublishVolume:: nas, mount point %s has existed, ignore", opts.NodePublishPath)
		return &csi.NodePublishVolumeResponse{}, nil
	}
	if err := utils.CreateDir(opts.NodePublishPath, mountPointMode); err != nil {
		return nil, status.Errorf(codes.Internal, "create NFS mount point %s: %v", opts.NodePublishPath, err)
	}
	if err := mountNasVolume(opts, req.VolumeId); err != nil {
		return nil, status.Errorf(codes.Internal, "mount NFS volume %s: %v", req.VolumeId, err)
	}
	mounted, err = isMountPoint(opts.NodePublishPath)
	if err != nil || !mounted {
		_ = unmountNFS(opts.NodePublishPath)
		_ = os.Remove(opts.NodePublishPath)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "verify NFS mount point %s: %v", opts.NodePublishPath, err)
		}
		return nil, status.Errorf(codes.Internal, "NFS mount check failed for %s", opts.NodePublishPath)
	}
	if err := changeNasMode(opts); err != nil {
		_ = unmountNFS(opts.NodePublishPath)
		_ = os.Remove(opts.NodePublishPath)
		return nil, status.Errorf(codes.InvalidArgument, "apply NFS mode: %v", err)
	}
	log.Infof("NodePublishVolume:: volume %s mount successfully on mount point: %s", req.VolumeId, opts.NodePublishPath)
	return &csi.NodePublishVolumeResponse{}, nil
}

func (n *NodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	log.Infof("NodeUnpublishVolume:: starting Umount Nas Volume %s at path %s", req.VolumeId, req.TargetPath)
	mountPoint := req.TargetPath
	if mountPoint == "" {
		return nil, status.Error(codes.InvalidArgument, "target path is required")
	}
	mounted, err := isMountPoint(mountPoint)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "check NFS mount point %s: %v", mountPoint, err)
	}
	if !mounted {
		log.Warnf("NodeUnpublishVolume:: nas, unmount mountpoint not found, skipping: %s", mountPoint)
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}
	if err := unmountNFS(mountPoint); err != nil {
		return nil, status.Errorf(codes.Internal, "unmount NFS path %s: %v", mountPoint, err)
	}

	log.Infof("NodeUnpublishVolume:: Unmount nas Successfully on: %s", mountPoint)
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (n *NodeServer) NodeStageVolume(context.Context, *csi.NodeStageVolumeRequest) (
	*csi.NodeStageVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (n *NodeServer) NodeUnstageVolume(context.Context, *csi.NodeUnstageVolumeRequest) (
	*csi.NodeUnstageVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (n *NodeServer) NodeExpandVolume(context.Context, *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// NodeGetVolumeStats used for csi metrics
func (ns *NodeServer) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	var err error
	targetPath := req.GetVolumePath()
	if targetPath == "" {
		err = fmt.Errorf("NodeGetVolumeStats targetpath %v is empty", targetPath)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return utils.GetMetrics(targetPath)
}
