package oss

const (
	defaultOssRoot            = "/"
	defaultOSSAddressingStyle = "path"
	ossAddressingStylePath    = "path"
	ossAddressingStyleVirtual = "virtual"
	defaultOSSSignatureType   = "v4"
	ossSignatureTypeV2        = "v2"
	ossSignatureTypeV4        = "v4"
	credentialDirectory       = "/var/lib/kubelet/plugins/oss.csi.cds.net/credentials"
	dynamicVolumePrefix       = "oss-dynamic:"
	dynamicMarkerName         = ".csi-volume"
)

var defaultS3fsOptions = []string{
	"dbglevel=info",
	"curldbg",
	"allow_other",
}
