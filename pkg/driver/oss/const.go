package oss

const (
	defaultOssRoot      = "/"
	credentialDirectory = "/var/lib/kubelet/plugins/oss.csi.cds.net/credentials"
	dynamicVolumePrefix = "oss-dynamic:"
	dynamicMarkerName   = ".csi-volume"
)

var defaultS3fsOptions = []string{
	"dbglevel=info",
	"curldbg",
	"allow_other",
	"use_path_request_style",
}
