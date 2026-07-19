package oss

const (
	defaultOssRoot            = "/"
	defaultOSSAddressingStyle = "auto"
	ossAddressingStyleAuto    = "auto"
	ossAddressingStylePath    = "path"
	ossAddressingStyleVirtual = "virtual"
	credentialDirectory       = "/var/lib/kubelet/plugins/oss.csi.cds.net/credentials"
	dynamicVolumePrefix       = "oss-dynamic:"
	dynamicMarkerName         = ".csi-volume"
)
