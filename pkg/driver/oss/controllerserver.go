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
	URL             string `json:"url"`
	Path            string `json:"path"`
	AddressingStyle string `json:"addressingStyle,omitempty"`
	Region          string `json:"region,omitempty"`
	SignatureType   string `json:"signatureType,omitempty"`
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

	client, err := newOssClient(ref, credentials)
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
			"url":             ref.URL,
			"path":            ref.Path,
			"addressingStyle": ref.AddressingStyle,
			"region":          ref.Region,
			"signatureType":   ref.SignatureType,
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
	ref := dynamicVolumeRef{}
	for key, value := range parameters {
		switch strings.ToLower(key) {
		case "bucket":
			ref.Bucket = strings.TrimSpace(value)
		case "url":
			ref.URL = strings.TrimSpace(value)
		case "path":
			ref.Path = strings.TrimSpace(value)
		case "addressingstyle":
			ref.AddressingStyle = strings.TrimSpace(value)
		case "region":
			ref.Region = strings.TrimSpace(value)
		case "signaturetype":
			ref.SignatureType = strings.TrimSpace(value)
		}
	}
	if ref.Bucket == "" || ref.URL == "" {
		return ref, fmt.Errorf("StorageClass parameters bucket and url are required")
	}
	if ref.Path == "" {
		ref.Path = defaultOssRoot
	}
	addressingStyle, err := normalizeAddressingStyle(ref.AddressingStyle)
	if err != nil {
		return ref, err
	}
	ref.AddressingStyle = addressingStyle
	signatureType, err := normalizeSignatureType(ref.SignatureType)
	if err != nil {
		return ref, err
	}
	ref.SignatureType = signatureType
	for _, segment := range strings.Split(strings.Trim(ref.Path, "/"), "/") {
		if segment == ".." {
			return ref, fmt.Errorf("StorageClass parameter path must not contain ..")
		}
	}
	basePath := pathpkg.Clean("/" + strings.TrimPrefix(ref.Path, "/"))
	digest := sha256.Sum256([]byte(volumeName))
	ref.Path = pathpkg.Join(basePath, "csi-"+hex.EncodeToString(digest[:12]))
	return ref, nil
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
	if ref.Bucket == "" || ref.URL == "" || !strings.HasPrefix(pathpkg.Base(ref.Path), "csi-") {
		return dynamicVolumeRef{}, true, fmt.Errorf("invalid dynamic OSS volume ID")
	}
	ref.AddressingStyle, err = normalizeAddressingStyle(ref.AddressingStyle)
	if err != nil {
		return dynamicVolumeRef{}, true, err
	}
	ref.SignatureType, err = normalizeSignatureType(ref.SignatureType)
	if err != nil {
		return dynamicVolumeRef{}, true, err
	}
	return ref, true, nil
}

func newOssClient(ref dynamicVolumeRef, credentials OssCredentials) (*minio.Client, error) {
	endpoint, err := url.Parse(ref.URL)
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
	return minio.New(endpoint.Host, &minio.Options{
		Creds:        minioCredentials.NewStaticV4(credentials.AccessKeyID, credentials.AccessKeySecret, ""),
		Secure:       endpoint.Scheme == "https",
		BucketLookup: bucketLookup,
		Region:       ref.Region,
	})
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
