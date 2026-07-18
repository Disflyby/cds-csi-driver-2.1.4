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
	Endpoint        string `json:"endpoint,omitempty"`
	URL             string `json:"url"`
	EndpointMode    string `json:"endpointMode,omitempty"`
	OtherOpts       string `json:"otherOpts"`
	AkID            string `json:"akId"`
	AkSecret        string `json:"akSecret"`
	Path            string `json:"path"`
	AuthType        string `json:"authType"`
	AddressingStyle string `json:"addressingStyle"`
	Region          string `json:"region"`
	SignatureType   string `json:"signatureType"`
	Mounter         string `json:"mounter,omitempty"`
}

type PublishOptions struct {
	OssOpts
	NodePublishPath string
	AllowSharePath  bool
}
