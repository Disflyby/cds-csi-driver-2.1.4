package nas

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/capitalonline/cds-csi-driver/pkg/driver/utils"
	"github.com/container-storage-interface/spec/lib/go/csi"
	log "github.com/sirupsen/logrus"
	core "k8s.io/api/core/v1"
	storage "k8s.io/api/storage/v1"
)

func (opts *NfsOpts) parsNfsOpts() error {
	opts.versNormalization()
	if opts.Path == "" {
		return errors.New("NFS path is required")
	}
	// Preserve the NFS export root while normalizing non-root trailing slashes.
	for opts.Path != "/" && strings.HasSuffix(opts.Path, "/") {
		opts.Path = opts.Path[0 : len(opts.Path)-1]
	}
	if err := validateNFSPath(opts.Path, "path"); err != nil {
		return err
	}

	// parse options, config defaults for nas based on vers
	if opts.Options == "" {
		opts.Options = opts.getDefaultMountOptions()
	} else if strings.ToLower(opts.Options) == "none" {
		opts.Options = ""
	}
	return validateNFSInput(opts.Server, opts.Path, "/", opts.Vers, opts.Options)
}

func (opts *NfsOpts) versNormalization() {
	if opts.Vers == "" {
		opts.Vers = "4.0"
	}
	if opts.Vers == "3.0" {
		opts.Vers = "3"
	} else if opts.Vers == "4" {
		opts.Vers = "4.0"
	}
}

func (opts *NfsOpts) nfsV4() bool {
	return strings.HasPrefix(opts.Vers, "4")
}

func (opts *NfsOpts) getDefaultMountOptions() string {
	if opts.nfsV4() {
		return defaultV4Opts
	} else {
		return defaultV3Opts
	}
}

func parseMountOptionsField(mntOptions []string) (vers string, opts string) {
	if len(mntOptions) > 0 {
		mntOptionsStr := strings.Join(mntOptions, ",")
		// mntOptions should re-split, as some like ["a,b,c", "d"]
		mntOptionsList := strings.Split(mntOptionsStr, ",")
		var tmpOptionsList []string

		if strings.Contains(mntOptionsStr, "vers=3.0") {
			for _, tmpOptions := range mntOptionsList {
				if tmpOptions != "vers=3.0" {
					tmpOptionsList = append(tmpOptionsList, tmpOptions)
				}
			}
			vers, opts = "3", strings.Join(tmpOptionsList, ",")
		} else if strings.Contains(mntOptionsStr, "vers=3") {
			for _, tmpOptions := range mntOptionsList {
				if tmpOptions != "vers=3" {
					tmpOptionsList = append(tmpOptionsList, tmpOptions)
				}
			}
			vers, opts = "3", strings.Join(tmpOptionsList, ",")
		} else if strings.Contains(mntOptionsStr, "vers=4.0") {
			for _, tmpOptions := range mntOptionsList {
				if tmpOptions != "vers=4.0" {
					tmpOptionsList = append(tmpOptionsList, tmpOptions)
				}
			}
			vers, opts = "4.0", strings.Join(tmpOptionsList, ",")
		} else if strings.Contains(mntOptionsStr, "vers=4.1") {
			for _, tmpOptions := range mntOptionsList {
				if tmpOptions != "vers=4.1" {
					tmpOptionsList = append(tmpOptionsList, tmpOptions)
				}
			}
			vers, opts = "4.1", strings.Join(tmpOptionsList, ",")
		} else {
			vers, opts = "", strings.Join(mntOptions, ",")
		}
	}
	return
}

func newPublishOptions(req *csi.NodePublishVolumeRequest) *PublishOptions {
	opts := &PublishOptions{}
	opts.NodePublishPath = req.GetTargetPath()
	opts.Readonly = req.GetReadonly()
	for key, value := range req.VolumeContext {
		if key == "server" {
			opts.Server = value
		} else if key == "path" {
			opts.Path = value
		} else if key == "vers" {
			opts.Vers = value
		} else if key == "mode" {
			opts.Mode = value
		} else if key == "options" {
			opts.Options = value
		} else if key == "modeType" {
			opts.ModeType = value
		} else if key == "volumeAs" {
			opts.VolumeAs = value
		} else if key == "allowShared" {
			allowed, err := strconv.ParseBool(value)
			if err != nil {
				opts.AllowSharePath = false
			}
			opts.AllowSharePath = allowed
		} else if key == dynamicSubpathContextKey {
			dynamic, err := strconv.ParseBool(value)
			if err == nil {
				opts.DynamicSubpath = dynamic
			}
		}
	}
	return opts
}

func newSubpathVolumeContext(opts *VolumeCreateSubpathOptions, pvName string) map[string]string {
	ctx := make(map[string]string)
	ctx["volumeAs"] = opts.VolumeAs
	ctx["server"] = opts.Server
	ctx["path"] = filepath.Join(opts.Path, pvName)
	ctx["mode"] = opts.Mode
	ctx["modeType"] = opts.ModeType
	ctx["options"] = opts.Options
	ctx["vers"] = opts.Vers
	ctx[dynamicSubpathContextKey] = "true"
	return ctx
}

func parsePublishOptions(req *csi.NodePublishVolumeRequest) (*PublishOptions, error) {
	if err := validateVolumeID(req.GetVolumeId()); err != nil {
		return nil, err
	}
	if req.GetVolumeCapability() == nil || req.GetVolumeCapability().GetMount() == nil {
		return nil, errors.New("mount volume capability is required")
	}
	opts := newPublishOptions(req)

	// set volumeAs to default "subpath"
	if opts.VolumeAs == "" {
		opts.VolumeAs = "subpath"
	}

	if opts.NodePublishPath == "" {
		return nil, errors.New("mountPath is empty")
	}

	if opts.Server == "" {
		return nil, errors.New("host is empty, should input nas domain")
	}
	if opts.VolumeAs != subpathLiteral && opts.VolumeAs != fileSystemLiteral {
		return nil, fmt.Errorf("unsupported volumeAs %q", opts.VolumeAs)
	}

	if err := opts.parsNfsOpts(); err != nil {
		return nil, err
	}

	// version/options settings in mountOptions field will overwrite the options
	if req.VolumeCapability != nil && req.VolumeCapability.GetMount() != nil {
		mntOptions := req.VolumeCapability.GetMount().MountFlags
		vers, options := parseMountOptionsField(mntOptions)
		if vers != "" {
			if opts.Vers != "" {
				log.Warnf("nas, Vers(%s) (in volumeAttributes) is ignored as Vers(%s) also configured in mountOptions", opts.Vers, vers)
			}
			opts.Vers = vers
		}
		if options != "" {
			if opts.Options != "" {
				log.Warnf("nas, Options(%s) (in volumeAttributes) is ignored as Options(%s) also configured in mountOptions", opts.Options, options)
			}
			opts.Options = options
		}
	}
	opts.versNormalization()
	if err := validateNFSInput(opts.Server, opts.Path, opts.NodePublishPath, opts.Vers, opts.Options); err != nil {
		return nil, err
	}

	if !utils.ServerReachable(opts.Server, nasPortNumber, dialTimeout) {
		log.Errorf("nas, cannot connect to nas host: %s", opts.Server)
		return nil, fmt.Errorf("nas, cannot connect to nas host: %s", opts.Server)
	}

	return opts, nil
}

func newVolumeCreateSubpathOptions(param map[string]string) *VolumeCreateSubpathOptions {
	opts := &VolumeCreateSubpathOptions{}
	opts.VolumeAs = param["volumeAs"]
	opts.Servers = param["servers"]
	opts.Server = param["server"]
	opts.Path = param["path"]
	opts.Vers = param["vers"]
	opts.Options = param["options"]
	opts.Mode = param["mode"]
	opts.ModeType = param["modeType"]

	return opts
}

func parseVolumeCreateSubpathOptions(req *csi.CreateVolumeRequest) (*VolumeCreateSubpathOptions, error) {

	opts := newVolumeCreateSubpathOptions(req.GetParameters())

	if opts.Server == "" && opts.Servers == "" {
		return nil, fmt.Errorf("nas, fatel error, server or servers is missing on volume as subpath")
	}

	var serverSlice []string

	if opts.Servers != "" {
		serverSlice = strings.Split(opts.Servers, ",")
	}
	if opts.Server != "" {
		serverSlice = append(serverSlice, strings.Join([]string{opts.Server, strings.TrimPrefix(opts.Path, "/")}, "/"))
	}

	log.Debugf("serverSlice is: %s", serverSlice)
	servers, err := parseConfiguredNFSServers(serverSlice)
	if err != nil {
		return nil, err
	}

	switch len(servers) {
	case 0:
		return nil, fmt.Errorf("nas, fatel error, [server or servers is missing ] or [servers usage all > 80] on volume as subpath")
	case 1:
		opts.Server = servers[0].Address
		opts.Path = servers[0].Path
	default:
		nfsServer := selectDeterministicNfsServer(servers, req.GetName())
		if nfsServer == nil {
			return nil, fmt.Errorf("nas, failed to choose an NFS server for volume %s", req.GetName())
		}
		opts.Server = nfsServer.Address
		opts.Path = nfsServer.Path
	}

	if err := opts.parsNfsOpts(); err != nil {
		return nil, err
	}

	if opts.ModeType == "" {
		opts.ModeType = "non-recursive"
	}

	return opts, nil
}

func mountNasVolume(opts *PublishOptions, volumeId string) error {
	var serverMountPoint string
	if opts.VolumeAs == subpathLiteral {
		if opts.AllowSharePath {
			serverMountPoint = opts.Path
		} else if opts.DynamicSubpath || filepath.Base(filepath.Clean(opts.Path)) == volumeId {
			// New dynamic PVs carry an explicit marker. The exact basename fallback
			// keeps already-provisioned dynamic PVs mountable after an upgrade.
			serverMountPoint = opts.Path
		} else {
			serverMountPoint = filepath.Join(opts.Path, volumeId)
		}
	} else if opts.VolumeAs == fileSystemLiteral {
		serverMountPoint = opts.Path
	} else {
		return fmt.Errorf("unsupported volumeAs %q", opts.VolumeAs)
	}

	err := mountNFS(opts.Server, serverMountPoint, opts.NodePublishPath, opts.Vers, opts.Options, opts.Readonly)
	if err != nil && opts.Path != "/" && !opts.DynamicSubpath {
		if strings.Contains(err.Error(), "No such file or directory") ||
			strings.Contains(err.Error(), "access denied by server while mounting") {
			log.Warnf("nas: NFS mount failed, auto-creating missing NFS subdirectory %s (may indicate a permission or path issue)", serverMountPoint)
			subDir := volumeId
			if opts.AllowSharePath {
				subDir = ""
			}
			if err := opts.createNasSubDir(publishVolumeRoot, subDir); err != nil {
				return fmt.Errorf("nas, create subpath error: %s", err.Error())
			}
			if err := mountNFS(opts.Server, serverMountPoint, opts.NodePublishPath, opts.Vers, opts.Options, opts.Readonly); err != nil {
				log.Errorf("nas, mount NFS failed after creating the sub directory: %s", err.Error())
				return err
			}
		} else {
			return err
		}
	} else if err != nil {
		return err
	}

	log.Debugf("nas, mount NFS successful at %s", opts.NodePublishPath)
	return nil
}

func (opts *NfsOpts) createNasSubDir(mountRoot, subDir string) error {
	return opts.createNasSubDirWithMarker(mountRoot, subDir, "")
}

func (opts *NfsOpts) createDynamicNasSubDir(mountRoot, volumeID string) error {
	return opts.createNasSubDirWithMarker(mountRoot, volumeID, volumeID)
}

func (opts *NfsOpts) createNasSubDirWithMarker(mountRoot, subDir, volumeID string) (retErr error) {
	log.Debugf("nas, running creatNasSubDir: root: %s, path: %s, subDir:%s", mountRoot, opts.Path, subDir)

	localMountPath := filepath.Join(mountRoot, subDir)
	fullPath := filepath.Join(localMountPath, subDir)

	// Skip creation if the volume marker already exists. This makes dynamic
	// provisioning idempotent across controller restarts.
	if volumeID != "" {
		exists, markErr := volumeMarkerExists(fullPath, volumeID)
		if markErr == nil && exists {
			log.Infof("nas subpath marker already exists, skipping creation: %s", fullPath)
			return nil
		}
	}
	mounted, err := isMountPoint(localMountPath)
	if err != nil {
		return err
	}
	if mounted {
		if err := unmountNFS(localMountPath); err != nil {
			return fmt.Errorf("unmount existing temporary path %s: %w", localMountPath, err)
		}
	}
	if err := utils.CreateDir(localMountPath, mountPointMode); err != nil {
		return fmt.Errorf("nas, create localMountPath %s err: %s", localMountPath, err.Error())
	}
	defer func(directory string) {
		isMounted, err := isMountPoint(directory)
		if err == nil && isMounted {
			if err := unmountNFS(directory); err != nil && retErr == nil {
				retErr = fmt.Errorf("unmount temporary path %s: %w", directory, err)
			}
		}
		removeMountPoint(directory)
	}(localMountPath)

	if err := mountNFS(opts.Server, opts.Path, localMountPath, opts.Vers, opts.Options, false); err != nil {
		return err
	}
	if err := utils.CreateDir(fullPath, mountPointMode); err != nil {
		return fmt.Errorf("nas, create sub directory: %w", err)
	}
	if volumeID != "" {
		if err := ensureVolumeMarker(fullPath, volumeID); err != nil {
			return err
		}
	}
	if err := os.Chmod(fullPath, mountPointMode); err != nil {
		return fmt.Errorf("change mode for %s: %w", fullPath, err)
	}
	return nil
}

func parseConfiguredNFSServers(serverList []string) ([]*NfsServer, error) {
	servers := make([]*NfsServer, 0, len(serverList))
	for _, server := range serverList {
		addrPath := strings.SplitN(strings.TrimSpace(server), "/", 2)
		if len(addrPath) != 2 {
			return nil, fmt.Errorf("invalid NFS server entry %q", server)
		}
		address := strings.TrimSpace(addrPath[0])
		pathValue := strings.TrimSpace(addrPath[1])
		if pathValue == "" {
			pathValue = "/"
		}
		for _, part := range strings.Split(strings.TrimPrefix(pathValue, "/"), "/") {
			if part == ".." {
				return nil, fmt.Errorf("invalid NFS server entry %q", server)
			}
		}
		path := filepath.Join("/", pathValue)
		if err := validateNFSInput(address, path, "/", defaultNfsVersion, ""); err != nil {
			return nil, err
		}
		servers = append(servers, &NfsServer{Address: address, Path: path})
	}
	return servers, nil
}

func deleteNasFilesystemSubDir(mountRoot, subDir, fileSystemNasIP string) (retErr error) {
	mounted, err := isMountPoint(mountRoot)
	if err != nil {
		return err
	}
	if mounted {
		if err := unmountNFS(mountRoot); err != nil {
			return fmt.Errorf("unmount existing temporary path %s: %w", mountRoot, err)
		}
	}
	if err := utils.CreateDir(mountRoot, mountPointMode); err != nil {
		return fmt.Errorf("create temporary mount path %s: %w", mountRoot, err)
	}
	defer func() {
		isMounted, err := isMountPoint(mountRoot)
		if err == nil && isMounted {
			if err := unmountNFS(mountRoot); err != nil && retErr == nil {
				retErr = fmt.Errorf("unmount temporary path %s: %w", mountRoot, err)
			}
		}
		removeMountPoint(mountRoot)
	}()

	if err := mountNFS(fileSystemNasIP, defaultNFSRoot, mountRoot, defaultNfsVersion, defaultV4Opts, false); err != nil {
		return err
	}
	deleteDir := filepath.Join(mountRoot, strings.TrimPrefix(subDir, defaultNFSRoot))
	if err := os.RemoveAll(deleteDir); err != nil {
		return fmt.Errorf("delete NFS path %s: %w", deleteDir, err)
	}
	return nil
}

func changeNasMode(opts *PublishOptions) error {
	if opts.Mode == "" || opts.Path == "/" || opts.Readonly {
		return nil
	}
	if opts.ModeType != "" && opts.ModeType != "non-recursive" {
		return fmt.Errorf("recursive NFS mode changes are not supported")
	}
	mode, err := strconv.ParseUint(opts.Mode, 8, 32)
	if err != nil || mode > 0777 {
		return fmt.Errorf("invalid NFS mode %q", opts.Mode)
	}
	if err := os.Chmod(opts.NodePublishPath, os.FileMode(mode)); err != nil {
		return fmt.Errorf("change mode for %s: %w", opts.NodePublishPath, err)
	}
	return nil
}

func getNasPathFromPvPath(pvPath string) (nasPath string) {
	tmpPath := pvPath
	if strings.HasSuffix(pvPath, "/") {
		tmpPath = pvPath[0 : len(pvPath)-1]
	}
	pos := strings.LastIndex(tmpPath, "/")
	nasPath = pvPath[0:pos]
	if nasPath == "" {
		nasPath = "/"
	}
	return
}

func getDeleteVolumeSubpathOptions(pv *core.PersistentVolume, sc *storage.StorageClass) *DeleteVolumeSubpathOptions {
	opts := &DeleteVolumeSubpathOptions{}
	opts.Server = pv.Spec.CSI.VolumeAttributes["server"]
	opts.Path = pv.Spec.CSI.VolumeAttributes["path"]
	opts.Vers = pv.Spec.CSI.VolumeAttributes["vers"]
	if archiveOnDelete, ok := sc.Parameters["archiveOnDelete"]; ok {
		archiveBool, err := strconv.ParseBool(archiveOnDelete)
		if err != nil {
			log.Errorf("nas, failed to get archieveOnDelete value, setting to true by default: %s", err.Error())
			opts.ArchiveOnDelete = true
		} else {
			opts.ArchiveOnDelete = archiveBool
		}
	}
	return opts
}
