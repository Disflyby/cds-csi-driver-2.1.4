package oss

import (
	"errors"
	"io/ioutil"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
)

type OssCredentials struct {
	AccessKeyID     string
	AccessKeySecret string
}

func (opts *OssOpts) parsOssOpts() error {
	// parse url and bucket
	if opts.URL == "" || opts.Bucket == "" {
		return errors.New("Oss Parametes error: Url or Bucket empty ")
	}
	// parse path
	if opts.Path == "" {
		log.Warnf("oss, path is empty, using default root %s", defaultOssRoot)
		opts.Path = defaultOssRoot
	}
	// remove / if path end with /;
	for opts.Path != "/" && strings.HasSuffix(opts.Path, "/") {
		opts.Path = opts.Path[0 : len(opts.Path)-1]
	}
	return nil
}

// save ak file: bucket:ak_id:ak_secret
func (opts *OssOpts) saveOssCredential(akFile string) error {
	newContentStr := opts.AkID + ":" + opts.AkSecret + "\n"
	if err := ioutil.WriteFile(akFile, []byte(newContentStr), 0600); err != nil {
		log.Errorf("Save Credential File failed: %s", err)
		return err
	}
	log.Debugf("saveOssCredential, save AK and AS into %s succeed!", CredentialFile)
	return nil
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
