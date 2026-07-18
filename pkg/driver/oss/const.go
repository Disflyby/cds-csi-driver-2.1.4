package oss

const (
	defaultOssRoot            = "/"
	defaultOSSAddressingStyle = "path"
	ossAddressingStyleAuto    = "auto"
	ossAddressingStylePath    = "path"
	ossAddressingStyleVirtual = "virtual"
	defaultOSSEndpointMode    = "service"
	ossEndpointModeService    = "service"
	ossEndpointModeBucket     = "bucket"
	defaultOSSSignatureType   = "v4"
	ossSignatureTypeV2        = "v2"
	ossSignatureTypeV4        = "v4"
	defaultOSSMounter         = "s3fs"
	ossMounterS3FS            = "s3fs"
	credentialDirectory       = "/var/lib/kubelet/plugins/oss.csi.cds.net/credentials"
	dynamicVolumePrefix       = "oss-dynamic:"
	dynamicMarkerName         = ".csi-volume"
)

var defaultS3fsOptions = []string{
	"compat_dir",
	"connect_timeout=10",
	"readwrite_timeout=30",
	"retries=2",
	"dbglevel=info",
	"curldbg",
	"allow_other",
}
