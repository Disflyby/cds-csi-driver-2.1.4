package oss

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	pathpkg "path"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
	"github.com/minio/minio-go/v7"
	minioCredentials "github.com/minio/minio-go/v7/pkg/credentials"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type dynamicVolumeRef struct {
	Bucket          string `json:"bucket"`
	Endpoint        string `json:"endpoint,omitempty"`
	URL             string `json:"url"`
	Path            string `json:"path"`
	EndpointMode    string `json:"endpointMode,omitempty"`
	AddressingStyle string `json:"addressingStyle,omitempty"`
	Region          string `json:"region,omitempty"`
	SignatureType   string `json:"signatureType,omitempty"`
	Mounter         string `json:"mounter,omitempty"`
}

func NewControllerServer(d *OssDriver) *ControllerServer {
	return &ControllerServer{
		DefaultControllerServer: csicommon.NewDefaultControllerServer(d.csiDriver),
	}
}

func (c *ControllerServer) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume name is required")
	}
	if len(req.GetVolumeCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume capabilities are required")
	}
	for _, capability := range req.GetVolumeCapabilities() {
		if capability.GetAccessMode().GetMode() != csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER {
			return nil, status.Error(codes.InvalidArgument, "only MULTI_NODE_MULTI_WRITER is supported")
		}
	}

	ref, err := newDynamicVolumeRef(req.GetParameters(), req.GetName())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	credentials := credentialsFromValues(req.GetSecrets())
	if !credentials.valid() {
		return nil, status.Error(codes.InvalidArgument, "OSS credentials are required through the provisioner secret")
	}

	client, ref, err := resolveOssClient(ctx, ref, credentials)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := ensureObjectPrefix(ctx, client, ref); err != nil {
		return nil, status.Errorf(codes.Internal, "create OSS volume prefix: %v", err)
	}

	capacityBytes := int64(0)
	if req.GetCapacityRange() != nil {
		capacityBytes = req.GetCapacityRange().GetRequiredBytes()
	}
	volumeID, err := encodeDynamicVolumeID(ref)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "encode OSS volume ID: %v", err)
	}

	return &csi.CreateVolumeResponse{Volume: &csi.Volume{
		VolumeId:      volumeID,
		CapacityBytes: capacityBytes,
		VolumeContext: map[string]string{
			"bucket":          ref.Bucket,
			"endpoint":        ref.Endpoint,
			"url":             ref.URL,
			"path":            ref.Path,
			"endpointMode":    ref.EndpointMode,
			"addressingStyle": ref.AddressingStyle,
			"region":          ref.Region,
			"signatureType":   ref.SignatureType,
			"mounter":         ref.Mounter,
		},
	}}, nil
}

func (c *ControllerServer) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	ref, dynamic, err := decodeDynamicVolumeID(req.GetVolumeId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if !dynamic {
		// Static PVs are owned outside this driver and must never trigger a
		// bucket-prefix cleanup when their reclaim policy is Delete.
		return &csi.DeleteVolumeResponse{}, nil
	}
	credentials := credentialsFromValues(req.GetSecrets())
	if !credentials.valid() {
		return nil, status.Error(codes.InvalidArgument, "OSS credentials are required through the provisioner secret")
	}
	client, err := newOssClient(ref, credentials)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := removeObjectPrefix(ctx, client, ref); err != nil {
		return nil, status.Errorf(codes.Internal, "delete OSS volume prefix: %v", err)
	}
	return &csi.DeleteVolumeResponse{}, nil
}

func (c *ControllerServer) ValidateVolumeCapabilities(_ context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	for _, capability := range req.GetVolumeCapabilities() {
		if capability.GetAccessMode().GetMode() != csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER {
			return &csi.ValidateVolumeCapabilitiesResponse{Message: "only MULTI_NODE_MULTI_WRITER is supported"}, nil
		}
	}
	return &csi.ValidateVolumeCapabilitiesResponse{Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
		VolumeCapabilities: req.GetVolumeCapabilities(),
	}}, nil
}

func (c *ControllerServer) ControllerExpandVolume(context.Context, *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "OSS volumes do not support capacity expansion")
}

func newDynamicVolumeRef(parameters map[string]string, volumeName string) (dynamicVolumeRef, error) {
	opts := ossOptsFromValues(parameters)
	if err := opts.parsOssOpts(); err != nil {
		return dynamicVolumeRef{}, err
	}
	basePath := pathpkg.Clean("/" + strings.TrimPrefix(opts.Path, "/"))
	digest := sha256.Sum256([]byte(volumeName))
	opts.Path = pathpkg.Join(basePath, "csi-"+hex.EncodeToString(digest[:12]))
	return dynamicVolumeRefFromOpts(opts), nil
}

func encodeDynamicVolumeID(ref dynamicVolumeRef) (string, error) {
	data, err := json.Marshal(ref)
	if err != nil {
		return "", err
	}
	return dynamicVolumePrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeDynamicVolumeID(volumeID string) (dynamicVolumeRef, bool, error) {
	if !strings.HasPrefix(volumeID, dynamicVolumePrefix) {
		return dynamicVolumeRef{}, false, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(volumeID, dynamicVolumePrefix))
	if err != nil {
		return dynamicVolumeRef{}, true, fmt.Errorf("invalid dynamic OSS volume ID")
	}
	ref := dynamicVolumeRef{}
	if err := json.Unmarshal(data, &ref); err != nil {
		return dynamicVolumeRef{}, true, fmt.Errorf("invalid dynamic OSS volume ID")
	}
	if !strings.HasPrefix(pathpkg.Base(ref.Path), "csi-") {
		return dynamicVolumeRef{}, true, fmt.Errorf("invalid dynamic OSS volume ID")
	}
	opts := ref.ossOpts()
	if err := opts.parsOssOpts(); err != nil {
		return dynamicVolumeRef{}, true, err
	}
	opts.Path = ref.Path
	return dynamicVolumeRefFromOpts(opts), true, nil
}

func newOssClient(ref dynamicVolumeRef, credentials OssCredentials) (*minio.Client, error) {
	if ref.AddressingStyle == ossAddressingStyleAuto {
		return nil, fmt.Errorf("OSS addressingStyle must be resolved before creating a client")
	}
	endpoint, err := url.Parse(ref.Endpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return nil, fmt.Errorf("invalid OSS endpoint URL")
	}
	if endpoint.Path != "" && endpoint.Path != "/" {
		return nil, fmt.Errorf("OSS endpoint URL must not contain a path")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, fmt.Errorf("OSS endpoint URL must not contain credentials, a query, or a fragment")
	}
	bucketLookup := minio.BucketLookupPath
	if ref.AddressingStyle == ossAddressingStyleVirtual {
		bucketLookup = minio.BucketLookupDNS
	}
	credentialProvider := minioCredentials.NewStaticV4(credentials.AccessKeyID, credentials.AccessKeySecret, "")
	if ref.SignatureType == ossSignatureTypeV2 {
		credentialProvider = minioCredentials.NewStaticV2(credentials.AccessKeyID, credentials.AccessKeySecret, "")
	}
	return minio.New(endpoint.Host, &minio.Options{
		Creds:        credentialProvider,
		Secure:       endpoint.Scheme == "https",
		BucketLookup: bucketLookup,
		Region:       ref.Region,
	})
}

func resolveOssClient(ctx context.Context, ref dynamicVolumeRef, credentials OssCredentials) (*minio.Client, dynamicVolumeRef, error) {
	if ref.AddressingStyle != ossAddressingStyleAuto {
		client, err := newOssClient(ref, credentials)
		return client, ref, err
	}

	var probeErrors []string
	for _, style := range []string{ossAddressingStylePath, ossAddressingStyleVirtual} {
		candidate := ref
		candidate.AddressingStyle = style
		client, err := newOssClient(candidate, credentials)
		if err != nil {
			probeErrors = append(probeErrors, style+": "+err.Error())
			continue
		}
		exists, err := client.BucketExists(ctx, candidate.Bucket)
		if err == nil && exists {
			return client, candidate, nil
		}
		if err != nil {
			probeErrors = append(probeErrors, style+": "+err.Error())
		} else {
			probeErrors = append(probeErrors, style+": bucket not found")
		}
	}
	return nil, ref, fmt.Errorf("resolve OSS addressingStyle automatically: %s", strings.Join(probeErrors, "; "))
}

func dynamicVolumeRefFromOpts(opts OssOpts) dynamicVolumeRef {
	return dynamicVolumeRef{
		Bucket:          opts.Bucket,
		Endpoint:        opts.Endpoint,
		URL:             opts.URL,
		Path:            opts.Path,
		EndpointMode:    opts.EndpointMode,
		AddressingStyle: opts.AddressingStyle,
		Region:          opts.Region,
		SignatureType:   opts.SignatureType,
		Mounter:         opts.Mounter,
	}
}

func (ref dynamicVolumeRef) ossOpts() OssOpts {
	return OssOpts{
		Bucket:          ref.Bucket,
		Endpoint:        ref.Endpoint,
		URL:             ref.URL,
		Path:            ref.Path,
		EndpointMode:    ref.EndpointMode,
		AddressingStyle: ref.AddressingStyle,
		Region:          ref.Region,
		SignatureType:   ref.SignatureType,
		Mounter:         ref.Mounter,
	}
}

func ensureObjectPrefix(ctx context.Context, client *minio.Client, ref dynamicVolumeRef) error {
	marker := strings.TrimPrefix(ref.Path, "/") + "/" + dynamicMarkerName
	_, err := client.PutObject(ctx, ref.Bucket, marker, strings.NewReader(""), 0, minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return err
}

func removeObjectPrefix(ctx context.Context, client *minio.Client, ref dynamicVolumeRef) error {
	operationCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	prefix := strings.TrimPrefix(ref.Path, "/") + "/"
	objects := make(chan minio.ObjectInfo)
	listErr := make(chan error, 1)
	go func() {
		defer close(objects)
		for object := range client.ListObjects(operationCtx, ref.Bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
			if object.Err != nil {
				listErr <- object.Err
				return
			}
			select {
			case objects <- object:
			case <-operationCtx.Done():
				return
			}
		}
		listErr <- nil
	}()
	for removeErr := range client.RemoveObjects(operationCtx, ref.Bucket, objects, minio.RemoveObjectsOptions{}) {
		if removeErr.Err != nil {
			cancel()
			return removeErr.Err
		}
	}
	return <-listErr
}
