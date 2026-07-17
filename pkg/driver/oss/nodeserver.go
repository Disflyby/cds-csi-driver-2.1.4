package oss

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/capitalonline/cds-csi-driver/pkg/driver/utils"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/kubernetes-csi/drivers/pkg/csi-common"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func NewNodeServer(d *OssDriver) *NodeServer {
	return &NodeServer{
		DefaultNodeServer: csicommon.NewDefaultNodeServer(d.csiDriver),
	}
}

func (n *NodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	log.Infof("NodePublishVolume:: starting mount oss volume %s at %s", req.GetVolumeId(), req.GetTargetPath())
	opts := &PublishOptions{}
	opts.NodePublishPath = req.GetTargetPath()
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "OSS volume ID is required")
	}
	if opts.NodePublishPath == "" {
		log.Errorf("oss mountPath is necessary but input empty")
		utils.SentrySendError(fmt.Errorf("oss mountPath is necessary but input empty"))
		return nil, errors.New("oss mountPath is necessary but input empty")
	}
	for key, value := range req.VolumeContext {
		key = strings.ToLower(key)
		if key == "bucket" {
			opts.Bucket = strings.TrimSpace(value)
		} else if key == "url" {
			opts.URL = strings.TrimSpace(value)
		} else if key == "path" {
			opts.Path = strings.TrimSpace(value)
		} else if key == "akid" {
			opts.AkID = strings.TrimSpace(value)
		} else if key == "aksecret" {
			opts.AkSecret = strings.TrimSpace(value)
		} else if key == "authtype" {
			opts.AuthType = strings.ToLower(strings.TrimSpace(value))
		} else if key == "addressingstyle" {
			opts.AddressingStyle = strings.TrimSpace(value)
		} else if key == "region" {
			opts.Region = strings.TrimSpace(value)
		} else if key == "signaturetype" {
			opts.SignatureType = strings.TrimSpace(value)
		}
	}

	// Dynamic volumes receive credentials through NodePublishSecretRef. Keep the
	// volume-context fallback so existing static PVs continue to work unchanged.
	credentials := credentialsFromValues(req.GetSecrets())
	if credentials.AccessKeyID != "" {
		opts.AkID = credentials.AccessKeyID
	}
	if credentials.AccessKeySecret != "" {
		opts.AkSecret = credentials.AccessKeySecret
	}
	// check parameters
	if err := opts.parsOssOpts(); err != nil {
		return nil, err
	}
	if opts.AkID == "" || opts.AkSecret == "" {
		return nil, errors.New("oss credentials are required")
	}

	mounted, err := utils.IsSystemMountPoint(opts.NodePublishPath)
	if err != nil {
		return nil, fmt.Errorf("check OSS mount point: %w", err)
	}
	if mounted {
		log.Debugf("NodePublishVolume:: oss, mountPath: %s is mounted", opts.NodePublishPath)
		return &csi.NodePublishVolumeResponse{}, nil
	}

	credentialFile, err := credentialFilePath(req.GetVolumeId(), opts.NodePublishPath)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	credentials = OssCredentials{AccessKeyID: opts.AkID, AccessKeySecret: opts.AkSecret}
	if err := writeOssCredential(credentialFile, credentials); err != nil {
		return nil, err
	}
	mounted = false
	defer func() {
		if !mounted {
			if err := removeOssCredential(credentialFile); err != nil {
				log.Warnf("remove failed OSS credential file %s: %v", credentialFile, err)
			}
		}
	}()

	if err := utils.CreateDir(opts.NodePublishPath, 0777); err != nil {
		return nil, fmt.Errorf("NodePublishVolume:: oss, unable to create directory: %s", opts.NodePublishPath)
	}

	log.Debugf("NodePublishVolume:: Start mount source [%s:%s] to [%s]", opts.Bucket, opts.Path, opts.NodePublishPath)
	if err := utils.RunSystemCommand("mount", s3fsMountArgs(opts, credentialFile)...); err != nil {
		return nil, fmt.Errorf("mount OSS volume: %w", err)
	}
	// A successful s3fs process may already own this credential file even if a
	// follow-up mount-info read fails, so keep it until an unpublish succeeds.
	mounted = true

	mounted, err = utils.IsSystemMountPoint(opts.NodePublishPath)
	if err != nil {
		return nil, fmt.Errorf("verify OSS mount point: %w", err)
	}
	if !mounted {
		log.Errorf("Remote bucket path [%s:%s] is not exist, please create it firstly", opts.Bucket, opts.Path)
		utils.SentrySendError(fmt.Errorf("Remote bucket path [%s:%s] is not exist, please create it firstly", opts.Bucket, opts.Path))
		return nil, errors.New("OSS mount did not appear at target path")
	}

	log.Infof("NodePublishVolume:: Mount Oss successful, volumeID:%s, oss: [%s:%s], targetPath:%s", req.VolumeId, opts.NodePublishPath, opts.Path, opts.NodePublishPath)
	return &csi.NodePublishVolumeResponse{}, nil
}

func (n *NodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	log.Infof("NodeUnpublishVolume:: starting Umount Oss Volume %s at path %s", req.VolumeId, req.TargetPath)
	mountPoint := req.TargetPath
	if req.GetVolumeId() == "" || mountPoint == "" {
		return nil, status.Error(codes.InvalidArgument, "OSS volume ID and target path are required")
	}
	credentialFile, err := credentialFilePath(req.GetVolumeId(), mountPoint)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	removeCredential := false
	defer func() {
		if removeCredential {
			if err := removeOssCredential(credentialFile); err != nil {
				log.Warnf("remove OSS credential file %s: %v", credentialFile, err)
			}
		}
	}()

	mounted, err := utils.IsSystemMountPoint(mountPoint)
	if err != nil {
		return nil, fmt.Errorf("check OSS mount point: %w", err)
	}
	if !mounted {
		log.Warnf("NodeUnpublishVolume:: oss, unmount mountpoint not found, skipping: %s", mountPoint)
		removeCredential = true
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	if err := utils.RunSystemCommand("unmount", mountPoint); err != nil {
		return nil, fmt.Errorf("NodeUnpublishVolume:: oss, Umount oss bucket fail: %s", err.Error())
	}
	removeCredential = true

	log.Infof("NodeUnpublishVolume:: Unmount oss Successfully on: %s", mountPoint)
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func s3fsMountArgs(opts *PublishOptions, credentialFile string) []string {
	args := []string{
		fmt.Sprintf("%s:%s", opts.Bucket, opts.Path),
		opts.NodePublishPath,
		"-o", "passwd_file=" + credentialFile,
		"-o", "url=" + opts.URL,
	}
	if opts.AddressingStyle == "" || opts.AddressingStyle == ossAddressingStylePath {
		args = append(args, "-o", "use_path_request_style")
	}
	if opts.Region != "" {
		args = append(args, "-o", "region="+opts.Region)
	}
	if opts.SignatureType == ossSignatureTypeV2 {
		args = append(args, "-o", "sigv2")
	}
	for _, option := range defaultS3fsOptions {
		args = append(args, "-o", option)
	}
	return args
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
