package oss

import (
	"crypto/sha256"
	"errors"
	"fmt"
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

func (opts *OssOpts) parsOssOpts() error {
	// Validate the values before they are passed to the s3fs process.
	if opts.URL == "" || opts.Bucket == "" {
		return errors.New("OSS parameters url and bucket are required")
	}
	endpoint, err := url.Parse(opts.URL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return errors.New("invalid OSS endpoint URL")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return errors.New("OSS endpoint URL must contain only a scheme and host")
	}
	if !isValidBucket(opts.Bucket) {
		return errors.New("invalid OSS bucket name")
	}
	opts.URL = strings.TrimSuffix(endpoint.String(), "/")
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
	if style != ossAddressingStylePath && style != ossAddressingStyleVirtual {
		return "", errors.New("OSS addressingStyle must be path or virtual")
	}
	return style, nil
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
