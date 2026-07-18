package oss

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
)

type OssCredentials struct {
	AccessKeyID     string
	AccessKeySecret string
}

func ossOptsFromValues(values map[string]string) OssOpts {
	opts := OssOpts{}
	for key, value := range values {
		value = strings.TrimSpace(value)
		switch strings.ToLower(key) {
		case "bucket":
			opts.Bucket = value
		case "endpoint":
			opts.Endpoint = value
		case "url":
			opts.URL = value
		case "endpointmode":
			opts.EndpointMode = value
		case "path", "prefix":
			opts.Path = value
		case "akid":
			opts.AkID = value
		case "aksecret":
			opts.AkSecret = value
		case "authtype":
			opts.AuthType = strings.ToLower(value)
		case "addressingstyle":
			opts.AddressingStyle = value
		case "region":
			opts.Region = value
		case "signaturetype":
			opts.SignatureType = value
		case "mounter":
			opts.Mounter = value
		}
	}
	return opts
}

func (opts *OssOpts) parsOssOpts() error {
	// Endpoint is the canonical field. URL remains a compatibility alias for
	// existing StorageClasses and static PVs.
	if opts.Endpoint != "" && opts.URL != "" && strings.TrimRight(opts.Endpoint, "/") != strings.TrimRight(opts.URL, "/") {
		return errors.New("OSS parameters endpoint and url must not conflict")
	}
	if opts.Endpoint == "" {
		opts.Endpoint = opts.URL
	}
	if opts.Endpoint == "" {
		return errors.New("OSS parameter endpoint is required")
	}

	endpoint, err := url.Parse(opts.Endpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return errors.New("invalid OSS endpoint URL")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errors.New("OSS endpoint URL must not contain credentials, a query, or a fragment")
	}

	endpointMode, err := normalizeEndpointMode(opts.EndpointMode)
	if err != nil {
		return err
	}
	opts.EndpointMode = endpointMode
	if endpointMode == ossEndpointModeBucket {
		if err := normalizeBucketEndpoint(endpoint, opts); err != nil {
			return err
		}
		// Downstream VolumeContext contains the canonical service endpoint.
		// Mark it as service mode so a Node does not normalize it a second time.
		opts.EndpointMode = ossEndpointModeService
	} else if endpoint.Path != "" && endpoint.Path != "/" {
		return errors.New("service OSS endpoint URL must contain only a scheme and host")
	}
	if opts.Bucket == "" {
		return errors.New("OSS bucket is required for a service endpoint and could not be derived from the bucket endpoint")
	}
	if !isValidBucket(opts.Bucket) {
		return errors.New("invalid OSS bucket name")
	}
	endpoint.Path = ""
	opts.Endpoint = strings.TrimSuffix(endpoint.String(), "/")
	opts.URL = opts.Endpoint

	addressingStyle, err := normalizeAddressingStyle(opts.AddressingStyle)
	if err != nil {
		return err
	}
	opts.AddressingStyle = addressingStyle

	signatureType, err := normalizeSignatureType(opts.SignatureType)
	if err != nil {
		return err
	}
	opts.SignatureType = signatureType

	mounter, err := normalizeMounter(opts.Mounter)
	if err != nil {
		return err
	}
	opts.Mounter = mounter

	if opts.Path == "" {
		log.Warnf("oss, path is empty, using default root %s", defaultOssRoot)
		opts.Path = defaultOssRoot
	}
	if !strings.HasPrefix(opts.Path, "/") {
		opts.Path = "/" + opts.Path
	}
	if strings.ContainsAny(opts.Path, "\x00\r\n") {
		return errors.New("invalid OSS path")
	}
	for _, segment := range strings.Split(strings.Trim(opts.Path, "/"), "/") {
		if segment == ".." {
			return errors.New("OSS path must not contain ..")
		}
	}
	for opts.Path != "/" && strings.HasSuffix(opts.Path, "/") {
		opts.Path = opts.Path[0 : len(opts.Path)-1]
	}
	return nil
}

func normalizeAddressingStyle(value string) (string, error) {
	style := strings.ToLower(strings.TrimSpace(value))
	if style == "" {
		return defaultOSSAddressingStyle, nil
	}
	if style != ossAddressingStyleAuto && style != ossAddressingStylePath && style != ossAddressingStyleVirtual {
		return "", errors.New("OSS addressingStyle must be auto, path or virtual")
	}
	return style, nil
}

func normalizeEndpointMode(value string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode == "" {
		return defaultOSSEndpointMode, nil
	}
	if mode != ossEndpointModeService && mode != ossEndpointModeBucket {
		return "", errors.New("OSS endpointMode must be service or bucket")
	}
	return mode, nil
}

func normalizeMounter(value string) (string, error) {
	mounter := strings.ToLower(strings.TrimSpace(value))
	if mounter == "" {
		return defaultOSSMounter, nil
	}
	if mounter != ossMounterS3FS {
		return "", fmt.Errorf("OSS mounter %q is not available in this driver image", mounter)
	}
	return mounter, nil
}

// normalizeBucketEndpoint converts a standard bucket-scoped endpoint into the
// canonical service endpoint required by the SDK and s3fs. Opaque CNAMEs and
// access-point aliases cannot be reversed safely and must include a service
// endpoint instead.
func normalizeBucketEndpoint(endpoint *url.URL, opts *OssOpts) error {
	pathBucket := strings.Trim(endpoint.EscapedPath(), "/")
	if pathBucket != "" {
		if strings.Contains(pathBucket, "/") {
			return errors.New("bucket OSS endpoint URL path must contain only the bucket name")
		}
		decodedBucket, err := url.PathUnescape(pathBucket)
		if err != nil {
			return errors.New("invalid bucket name in OSS endpoint URL")
		}
		if opts.Bucket != "" && opts.Bucket != decodedBucket {
			return errors.New("OSS bucket conflicts with the bucket endpoint path")
		}
		opts.Bucket = decodedBucket
		endpoint.Path = ""
		endpoint.RawPath = ""
		if opts.AddressingStyle != "" && !strings.EqualFold(opts.AddressingStyle, ossAddressingStyleAuto) && !strings.EqualFold(opts.AddressingStyle, ossAddressingStylePath) {
			return errors.New("path-style bucket endpoint conflicts with addressingStyle")
		}
		opts.AddressingStyle = ossAddressingStylePath
		return nil
	}

	hostname := endpoint.Hostname()
	labels := strings.Split(hostname, ".")
	if opts.Bucket == "" {
		if net.ParseIP(hostname) != nil || len(labels) < 2 {
			return errors.New("bucket name cannot be derived from this OSS bucket endpoint")
		}
		opts.Bucket = labels[0]
	}
	prefix := opts.Bucket + "."
	if !strings.HasPrefix(strings.ToLower(hostname), strings.ToLower(prefix)) {
		return errors.New("bucket endpoint host must start with '<bucket>.'; use endpointMode=service for opaque CNAME or access-point endpoints")
	}
	serviceHost := hostname[len(prefix):]
	if serviceHost == "" {
		return errors.New("invalid OSS bucket endpoint host")
	}
	if port := endpoint.Port(); port != "" {
		endpoint.Host = net.JoinHostPort(serviceHost, port)
	} else {
		endpoint.Host = serviceHost
	}
	if opts.AddressingStyle != "" && !strings.EqualFold(opts.AddressingStyle, ossAddressingStyleAuto) && !strings.EqualFold(opts.AddressingStyle, ossAddressingStyleVirtual) {
		return errors.New("virtual-host bucket endpoint conflicts with addressingStyle")
	}
	opts.AddressingStyle = ossAddressingStyleVirtual
	return nil
}

func normalizeSignatureType(value string) (string, error) {
	style := strings.ToLower(strings.TrimSpace(value))
	if style == "" {
		return defaultOSSSignatureType, nil
	}
	if style != ossSignatureTypeV2 && style != ossSignatureTypeV4 {
		return "", errors.New("OSS signatureType must be v2 or v4")
	}
	return style, nil
}

func isValidBucket(bucket string) bool {
	for _, char := range bucket {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '.' && char != '-' {
			return false
		}
	}
	return bucket != ""
}

func credentialFilePath(volumeID, targetPath string) (string, error) {
	if volumeID == "" || targetPath == "" {
		return "", errors.New("volume ID and target path are required for OSS credentials")
	}
	return filepath.Join(credentialDirectory, credentialFileName(volumeID, targetPath)), nil
}

func credentialFileName(volumeID, targetPath string) string {
	digest := sha256.Sum256([]byte(volumeID + "\x00" + targetPath))
	return fmt.Sprintf("%x.passwd", digest)
}

func writeOssCredential(credentialFile string, credentials OssCredentials) error {
	if err := os.MkdirAll(filepath.Dir(credentialFile), 0700); err != nil {
		return fmt.Errorf("create OSS credential directory: %w", err)
	}
	file, err := os.OpenFile(credentialFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("open OSS credential file: %w", err)
	}
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return fmt.Errorf("set OSS credential permissions: %w", err)
	}
	if _, err := file.WriteString(credentials.AccessKeyID + ":" + credentials.AccessKeySecret + "\n"); err != nil {
		return fmt.Errorf("write OSS credentials: %w", err)
	}
	return nil
}

func removeOssCredential(credentialFile string) error {
	err := os.Remove(credentialFile)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("remove OSS credential file: %w", err)
}

func credentialsFromValues(values map[string]string) OssCredentials {
	credentials := OssCredentials{}
	for key, value := range values {
		switch strings.ToLower(key) {
		case "akid", "accesskeyid", "access_key_id":
			credentials.AccessKeyID = strings.TrimSpace(value)
		case "aksecret", "secretaccesskey", "access_key_secret":
			credentials.AccessKeySecret = strings.TrimSpace(value)
		}
	}
	return credentials
}

func (credentials OssCredentials) valid() bool {
	return credentials.AccessKeyID != "" && credentials.AccessKeySecret != ""
}

// IsFileExisting check file exist in volume driver or not
func IsFileExisting(filename string) bool {
	_, err := os.Stat(filename)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	return true
}
