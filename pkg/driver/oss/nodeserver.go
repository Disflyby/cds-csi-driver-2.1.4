package oss

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/capitalonline/cds-csi-driver/pkg/driver/utils"
	"github.com/container-storage-interface/spec/lib/go/csi"
	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errStageCredentialsUnavailable = errors.New("OSS credentials are unavailable for NodeStageVolume")

const (
	ossMountReadinessTimeout = 15 * time.Second
	ossMountRollbackTimeout  = 10 * time.Second
)

func NewNodeServer(d *OssDriver) *NodeServer {
	return newNodeServer(d, newS3FSMounter())
}

func newNodeServer(d *OssDriver, mounter Mounter) *NodeServer {
	return &NodeServer{
		DefaultNodeServer: csicommon.NewDefaultNodeServer(d.csiDriver),
		mounter:           mounter,
	}
}

func (n *NodeServer) NodeGetCapabilities(context.Context, *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{Capabilities: []*csi.NodeServiceCapability{
		{
			Type: &csi.NodeServiceCapability_Rpc{Rpc: &csi.NodeServiceCapability_RPC{
				Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
			}},
		},
	}}, nil
}

func (n *NodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	if req.GetVolumeId() == "" || req.GetStagingTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "OSS volume ID and staging target path are required")
	}
	if err := validateMountPath(req.GetStagingTargetPath()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	n.mountMu.Lock()
	defer n.mountMu.Unlock()
	err := n.stageVolume(ctx, req.GetVolumeId(), req.GetStagingTargetPath(), req.GetVolumeContext(), req.GetSecrets())
	if errors.Is(err, errStageCredentialsUnavailable) {
		// PVs created before STAGE_UNSTAGE support have only a node-publish
		// secret. NodePublishVolume will use that secret to stage the volume.
		log.Warnf("NodeStageVolume: credentials are unavailable for volume %s; deferring mount to NodePublishVolume", req.GetVolumeId())
		return &csi.NodeStageVolumeResponse{}, nil
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "stage OSS volume: %v", err)
	}
	return &csi.NodeStageVolumeResponse{}, nil
}

func (n *NodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	if req.GetVolumeId() == "" || req.GetStagingTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "OSS volume ID and staging target path are required")
	}
	if err := validateMountPath(req.GetStagingTargetPath()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	n.mountMu.Lock()
	defer n.mountMu.Unlock()
	mounted, err := n.mounter.IsMounted(req.GetStagingTargetPath())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "check OSS staging mount: %v", err)
	}
	if mounted {
		if err := n.mounter.Unmount(ctx, req.GetStagingTargetPath()); err != nil {
			return nil, status.Errorf(codes.Internal, "unstage OSS volume: %v", err)
		}
	}
	credentialFile, err := credentialFilePath(req.GetVolumeId(), req.GetStagingTargetPath())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := removeOssCredential(credentialFile); err != nil {
		return nil, status.Errorf(codes.Internal, "remove OSS staging credentials: %v", err)
	}
	_ = os.Remove(req.GetStagingTargetPath())
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (n *NodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	if req.GetVolumeId() == "" || req.GetStagingTargetPath() == "" || req.GetTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "OSS volume ID, staging target path and publish target path are required")
	}
	if err := validateMountPath(req.GetStagingTargetPath()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := validateMountPath(req.GetTargetPath()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	n.mountMu.Lock()
	defer n.mountMu.Unlock()
	mounted, err := n.mounter.IsMounted(req.GetTargetPath())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "check OSS publish mount: %v", err)
	}
	if mounted {
		return &csi.NodePublishVolumeResponse{}, nil
	}

	staged, err := n.mounter.IsMounted(req.GetStagingTargetPath())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "check OSS staging mount: %v", err)
	}
	if !staged {
		if err := n.stageVolume(ctx, req.GetVolumeId(), req.GetStagingTargetPath(), req.GetVolumeContext(), req.GetSecrets()); err != nil {
			return nil, status.Errorf(codes.Internal, "stage OSS volume during publish: %v", err)
		}
	}
	if err := utils.CreateDir(req.GetTargetPath(), 0750); err != nil {
		return nil, status.Errorf(codes.Internal, "create OSS publish target: %v", err)
	}
	if err := n.mounter.BindMount(ctx, req.GetStagingTargetPath(), req.GetTargetPath(), req.GetReadonly()); err != nil {
		return nil, status.Errorf(codes.Internal, "bind OSS volume: %v", err)
	}
	return &csi.NodePublishVolumeResponse{}, nil
}

func (n *NodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	if req.GetVolumeId() == "" || req.GetTargetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "OSS volume ID and publish target path are required")
	}
	if err := validateMountPath(req.GetTargetPath()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	n.mountMu.Lock()
	defer n.mountMu.Unlock()
	mounted, err := n.mounter.IsMounted(req.GetTargetPath())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "check OSS publish mount: %v", err)
	}
	if mounted {
		if err := n.mounter.Unmount(ctx, req.GetTargetPath()); err != nil {
			return nil, status.Errorf(codes.Internal, "unpublish OSS volume: %v", err)
		}
	}
	// Clean credentials left by the pre-staging implementation, where the
	// credential file was scoped to each pod publish target.
	if legacyCredentialFile, err := credentialFilePath(req.GetVolumeId(), req.GetTargetPath()); err == nil {
		_ = removeOssCredential(legacyCredentialFile)
	}
	_ = os.Remove(req.GetTargetPath())
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (n *NodeServer) stageVolume(ctx context.Context, volumeID, stagingTargetPath string, values, secrets map[string]string) error {
	mounted, err := n.mounter.IsMounted(stagingTargetPath)
	if err != nil {
		return fmt.Errorf("check mount point: %w", err)
	}
	if mounted {
		if err := n.verifyStagingMount(ctx, stagingTargetPath); err != nil {
			if credentialFile, pathErr := credentialFilePath(volumeID, stagingTargetPath); pathErr == nil {
				_ = removeOssCredential(credentialFile)
			}
			return err
		}
		return nil
	}

	opts := ossOptsFromValues(values)
	contextCredentials := OssCredentials{AccessKeyID: opts.AkID, AccessKeySecret: opts.AkSecret}
	if contextCredentials.AccessKeyID != "" || contextCredentials.AccessKeySecret != "" {
		log.Warnf("OSS credentials found in VolumeContext for volume %s; migrate them to a Kubernetes Secret", volumeID)
	}
	credentials := credentialsFromValues(secrets)
	if credentials.AccessKeyID == "" {
		credentials.AccessKeyID = contextCredentials.AccessKeyID
	}
	if credentials.AccessKeySecret == "" {
		credentials.AccessKeySecret = contextCredentials.AccessKeySecret
	}
	if !credentials.valid() {
		return errStageCredentialsUnavailable
	}
	if err := opts.parsOssOpts(); err != nil {
		return err
	}
	if opts.AddressingStyle == ossAddressingStyleAuto {
		_, resolvedRef, err := resolveOssClient(ctx, dynamicVolumeRefFromOpts(opts), credentials)
		if err != nil {
			return err
		}
		opts.AddressingStyle = resolvedRef.AddressingStyle
	}
	opts.AkID = credentials.AccessKeyID
	opts.AkSecret = credentials.AccessKeySecret
	publishOpts := PublishOptions{OssOpts: opts, NodePublishPath: stagingTargetPath}

	credentialFile, err := credentialFilePath(volumeID, stagingTargetPath)
	if err != nil {
		return err
	}
	if err := writeOssCredential(credentialFile, credentials); err != nil {
		return err
	}
	ready := false
	defer func() {
		if !ready {
			_ = removeOssCredential(credentialFile)
		}
	}()
	if err := utils.CreateDir(stagingTargetPath, 0750); err != nil {
		return fmt.Errorf("create staging target: %w", err)
	}
	if err := n.mounter.Mount(ctx, &publishOpts, credentialFile); err != nil {
		return err
	}
	mounted, err = n.mounter.IsMounted(stagingTargetPath)
	if err != nil {
		return fmt.Errorf("verify staging mount: %w", err)
	}
	if !mounted {
		return errors.New("s3fs exited without creating the staging mount")
	}
	if err := n.verifyStagingMount(ctx, stagingTargetPath); err != nil {
		return err
	}
	ready = true
	return nil
}

func (n *NodeServer) verifyStagingMount(ctx context.Context, stagingTargetPath string) error {
	readinessCtx, cancelReadiness := context.WithTimeout(ctx, ossMountReadinessTimeout)
	defer cancelReadiness()
	if err := n.mounter.CheckReady(readinessCtx, stagingTargetPath); err != nil {
		rollbackCtx, cancelRollback := context.WithTimeout(context.Background(), ossMountRollbackTimeout)
		defer cancelRollback()
		if rollbackErr := n.mounter.UnmountLazy(rollbackCtx, stagingTargetPath); rollbackErr != nil {
			return fmt.Errorf("read OSS staging mount: %v; rollback mount: %w", err, rollbackErr)
		}
		return fmt.Errorf("read OSS staging mount: %w", err)
	}
	return nil
}

func validateMountPath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("mount path %q must be absolute", path)
	}
	if filepath.Clean(path) == string(filepath.Separator) {
		return errors.New("mount path must not be the filesystem root")
	}
	return nil
}

func (n *NodeServer) NodeExpandVolume(context.Context, *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "OSS volumes do not support capacity expansion")
}

func (n *NodeServer) NodeGetVolumeStats(context.Context, *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "OSS volumes do not support volume stats")
}
