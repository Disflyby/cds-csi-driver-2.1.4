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

func ossOptsFromValues(values map[string]string) OssOpts {
	opts := OssOpts{}
	for key, value := range values {
		value = strings.TrimSpace(value)
		switch strings.ToLower(key) {
		case "bucket":
			opts.Bucket = value
		case "endpoint":
			opts.Endpoint = value
		case "path", "prefix":
			opts.Path = value
		case "addressingstyle":
			// AddressingStyle is generated internally after endpoint probing and
			// carried in dynamic VolumeContext for the node-side mount.
			opts.AddressingStyle = value
		}
	}
	return opts
}

func (opts *OssOpts) parsOssOpts() error {
	if opts.Endpoint == "" {
		return errors.New("OSS parameter endpoint is required")
	}
	endpoint, err := url.Parse(opts.Endpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return errors.New("invalid OSS endpoint URL")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return errors.New("OSS endpoint URL must contain only a scheme and host")
	}
	endpoint.Path = ""
	opts.Endpoint = strings.TrimSuffix(endpoint.String(), "/")

	if !isValidBucket(opts.Bucket) {
		return errors.New("invalid or missing OSS bucket name")
	}
	addressingStyle, err := normalizeAddressingStyle(opts.AddressingStyle)
	if err != nil {
		return err
	}
	opts.AddressingStyle = addressingStyle

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
		opts.Path = strings.TrimSuffix(opts.Path, "/")
	}
	return nil
}

func normalizeAddressingStyle(value string) (string, error) {
	style := strings.ToLower(strings.TrimSpace(value))
	if style == "" {
		return defaultOSSAddressingStyle, nil
	}
	if style != ossAddressingStyleAuto && style != ossAddressingStylePath && style != ossAddressingStyleVirtual {
		return "", errors.New("invalid internal OSS addressing style")
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
	return fmt.Sprintf("%x.credentials", digest)
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
	contents := "[default]\naws_access_key_id = " + credentials.AccessKeyID + "\naws_secret_access_key = " + credentials.AccessKeySecret + "\n"
	if _, err := file.WriteString(contents); err != nil {
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
