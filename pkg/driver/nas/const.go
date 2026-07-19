package nas

import "time"

const (
	createVolumeRoot         = "/nas_volume/create"
	deleteVolumeRoot         = "/nas_volume/delete"
	publishVolumeRoot        = "/nas_volume/publish"
	mountPointMode           = 0777
	defaultV3Opts            = "noresvport,nolock,tcp"
	defaultV4Opts            = "noresvport"
	nasPortNumber            = "2049"
	dialTimeout              = time.Duration(3) * time.Second
	subpathLiteral           = "subpath"
	nasDynamicVolumePrefix   = "nas-dynamic:"
	nasDynamicVolumeV2Prefix = nasDynamicVolumePrefix + "v2:"
	defaultNfsVersion        = "4.0"
)
