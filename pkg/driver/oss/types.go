package oss

import (
	"sync"

	csicommon "github.com/kubernetes-csi/drivers/pkg/csi-common"
	"k8s.io/client-go/kubernetes"
)

type OssDriver struct {
	csiDriver        *csicommon.CSIDriver
	endpoint         string
	idServer         *IdentityServer
	nodeServer       *NodeServer
	controllerServer *ControllerServer
}

type NodeServer struct {
	*csicommon.DefaultNodeServer
	mounter Mounter
	mountMu sync.Mutex
}

type ControllerServer struct {
	*csicommon.DefaultControllerServer
	Client *kubernetes.Clientset
}

type IdentityServer struct {
	*csicommon.DefaultIdentityServer
}

type OssOpts struct {
	Bucket          string `json:"bucket"`
	Endpoint        string `json:"endpoint"`
	Path            string `json:"path"`
	AddressingStyle string `json:"addressingStyle"`
}

type PublishOptions struct {
	OssOpts
	NodePublishPath string
	AllowSharePath  bool
}
