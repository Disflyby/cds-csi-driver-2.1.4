package oss

import (
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/kubernetes-csi/drivers/pkg/csi-common"
)

var (
	volumeCap = []csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
	}

	controllerCap = []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
	}

	nodeCap = []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
	}
)

func NewIdentityServer(d *OssDriver) *IdentityServer {
	d.csiDriver.AddVolumeCapabilityAccessModes(volumeCap)
	d.csiDriver.AddControllerServiceCapabilities(controllerCap)
	d.csiDriver.AddNodeServiceCapabilities(nodeCap)
	return &IdentityServer{
		DefaultIdentityServer: csicommon.NewDefaultIdentityServer(d.csiDriver),
	}
}
