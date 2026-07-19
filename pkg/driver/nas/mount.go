package nas

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/capitalonline/cds-csi-driver/pkg/driver/utils"
)

const (
	dynamicSubpathContextKey = "csi.cds.net/dynamic-subpath"
	dynamicSubpathMarker     = ".csi-volume"
)

var (
	nfsServerPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	nfsMountOptionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._=:-]*(,[A-Za-z0-9][A-Za-z0-9._=:-]*)*$`)
	runNFSCommand         = utils.RunCommandArgs
)

func validateVolumeID(volumeID string) error {
	if volumeID == "" || strings.ContainsAny(volumeID, "\\/\x00\r\n") || volumeID == "." || volumeID == ".." {
		return fmt.Errorf("invalid volume ID %q", volumeID)
	}
	return nil
}

func validateDynamicSubDir(subDir string) error {
	if subDir == "" || subDir == "." || strings.Contains(subDir, "..") || strings.ContainsAny(subDir, "/\\\x00\r\n") {
		return fmt.Errorf("invalid dynamic NFS subdirectory %q: must be a single directory name", subDir)
	}
	if filepath.Clean(subDir) != subDir || filepath.Base(subDir) != subDir {
		return fmt.Errorf("invalid dynamic NFS subdirectory %q: must be a single directory name", subDir)
	}
	return nil
}

func resolveDynamicVolumePath(basePath, subDir string) (string, error) {
	if err := validateNFSPath(basePath, "base path"); err != nil {
		return "", err
	}
	if err := validateDynamicSubDir(subDir); err != nil {
		return "", err
	}
	cleanBasePath := filepath.Clean(basePath)
	volumePath := filepath.Join(cleanBasePath, subDir)
	if volumePath == cleanBasePath {
		return "", fmt.Errorf("dynamic NFS volume path cannot equal its base path")
	}
	relativePath, err := filepath.Rel(cleanBasePath, volumePath)
	if err != nil || relativePath == "." || filepath.IsAbs(relativePath) || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("dynamic NFS volume path %q escapes base path %q", volumePath, cleanBasePath)
	}
	return volumePath, nil
}

func splitDynamicVolumePath(volumePath string) (string, string, error) {
	if err := validateNFSPath(volumePath, "volume path"); err != nil {
		return "", "", err
	}
	cleanVolumePath := filepath.Clean(volumePath)
	if cleanVolumePath == string(filepath.Separator) {
		return "", "", fmt.Errorf("volume path cannot be the export root")
	}
	basePath := filepath.Dir(cleanVolumePath)
	subDir := filepath.Base(cleanVolumePath)
	if _, err := resolveDynamicVolumePath(basePath, subDir); err != nil {
		return "", "", err
	}
	return basePath, subDir, nil
}

func validateNFSInput(server, remotePath, targetPath, vers, options string) error {
	if !nfsServerPattern.MatchString(server) {
		return fmt.Errorf("invalid NFS server %q", server)
	}
	if err := validateNFSPath(remotePath, "remote path"); err != nil {
		return err
	}
	if err := validateNFSPath(targetPath, "target path"); err != nil {
		return err
	}
	if vers != "3" && vers != "4.0" && vers != "4.1" {
		return fmt.Errorf("unsupported NFS version %q", vers)
	}
	if options != "" && !nfsMountOptionPattern.MatchString(options) {
		return fmt.Errorf("invalid NFS mount options %q", options)
	}
	return nil
}

func validateNFSPath(path, field string) error {
	if path == "" || strings.ContainsAny(path, "\x00\r\n") || !filepath.IsAbs(path) {
		return fmt.Errorf("invalid NFS %s %q", field, path)
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return fmt.Errorf("invalid NFS %s %q", field, path)
		}
	}
	return nil
}

func nfsMountArgs(server, remotePath, targetPath, vers, options string, readonly bool) ([]string, error) {
	if err := validateNFSInput(server, remotePath, targetPath, vers, options); err != nil {
		return nil, err
	}

	mountOptions := []string{"vers=" + vers}
	if options != "" {
		for _, option := range strings.Split(options, ",") {
			if readonly && option == "rw" {
				continue
			}
			mountOptions = append(mountOptions, option)
		}
	}
	if readonly && !containsMountOption(mountOptions, "ro") {
		mountOptions = append(mountOptions, "ro")
	}
	return []string{"-t", "nfs", "-o", strings.Join(mountOptions, ","), server + ":" + remotePath, targetPath}, nil
}

func containsMountOption(options []string, wanted string) bool {
	for _, option := range options {
		if option == wanted {
			return true
		}
	}
	return false
}

func mountNFS(server, remotePath, targetPath, vers, options string, readonly bool) error {
	args, err := nfsMountArgs(server, remotePath, targetPath, vers, options, readonly)
	if err != nil {
		return err
	}
	if _, err := runNFSCommand("mount", args...); err != nil {
		return fmt.Errorf("mount NFS %s:%s at %s: %w", server, remotePath, targetPath, err)
	}
	return nil
}

func unmountNFS(targetPath string) error {
	if err := validateNFSPath(targetPath, "target path"); err != nil {
		return err
	}
	if _, err := runNFSCommand("umount", targetPath); err != nil {
		return fmt.Errorf("unmount NFS at %s: %w", targetPath, err)
	}
	return nil
}

func isMountPoint(targetPath string) (bool, error) {
	if err := validateNFSPath(targetPath, "target path"); err != nil {
		return false, err
	}
	contents, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, fmt.Errorf("read mountinfo: %w", err)
	}
	return isMountPointInMountInfo(string(contents), targetPath), nil
}

func isMountPointInMountInfo(mountInfo, targetPath string) bool {
	for _, line := range strings.Split(mountInfo, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 5 && unescapeMountInfoPath(fields[4]) == targetPath {
			return true
		}
	}
	return false
}

func unescapeMountInfoPath(path string) string {
	replacer := strings.NewReplacer(
		`\040`, " ",
		`\011`, "\t",
		`\012`, "\n",
		`\134`, `\`,
	)
	return replacer.Replace(path)
}

func removeMountPoint(targetPath string) {
	mounted, err := isMountPoint(targetPath)
	if err != nil || mounted {
		return
	}
	if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
		return
	}
}

func volumeMarkerExists(directory, volumeID string) (bool, error) {
	if volumeID == "" {
		return false, fmt.Errorf("volume ID is empty")
	}
	markerPath := filepath.Join(directory, dynamicSubpathMarker)
	markerContents := volumeID + "\n"
	contents, err := os.ReadFile(markerPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read volume marker %s: %w", markerPath, err)
	}
	if string(contents) != markerContents {
		return false, fmt.Errorf("volume marker %s belongs to a different volume", markerPath)
	}
	return true, nil
}

func ensureVolumeMarker(directory, volumeID string) error {
	exists, err := volumeMarkerExists(directory, volumeID)
	if err != nil || exists {
		return err
	}
	markerPath := filepath.Join(directory, dynamicSubpathMarker)
	marker, err := os.OpenFile(markerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		_, err := volumeMarkerExists(directory, volumeID)
		return err
	}
	if err != nil {
		return fmt.Errorf("create volume marker %s: %w", markerPath, err)
	}
	_, writeErr := marker.WriteString(volumeID + "\n")
	closeErr := marker.Close()
	if writeErr != nil {
		return fmt.Errorf("write volume marker %s: %w", markerPath, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close volume marker %s: %w", markerPath, closeErr)
	}
	return nil
}

func selectDeterministicNfsServer(servers []*NfsServer, volumeID string) *NfsServer {
	if len(servers) == 0 || volumeID == "" {
		return nil
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(volumeID))
	return servers[int(hash.Sum32()%uint32(len(servers)))]
}

func deleteNFSSubpath(server, basePath, subDir, vers, mountRoot string, archiveOnDelete bool) (retErr error) {
	if _, err := resolveDynamicVolumePath(basePath, subDir); err != nil {
		return err
	}
	if vers == "" {
		vers = defaultNfsVersion
	}
	mountPoint := filepath.Join(mountRoot, subDir+"-delete")
	mounted, err := isMountPoint(mountPoint)
	if err != nil {
		return err
	}
	if mounted {
		if err := unmountNFS(mountPoint); err != nil {
			return fmt.Errorf("unmount existing temporary path %s: %w", mountPoint, err)
		}
	}
	if err := utils.CreateDir(mountPoint, mountPointMode); err != nil {
		return fmt.Errorf("create temporary mount path %s: %w", mountPoint, err)
	}
	defer func() {
		isMounted, err := isMountPoint(mountPoint)
		if err == nil && isMounted {
			if err := unmountNFS(mountPoint); err != nil && retErr == nil {
				retErr = fmt.Errorf("unmount temporary path %s: %w", mountPoint, err)
			}
		}
		removeMountPoint(mountPoint)
	}()

	if err := mountNFS(server, basePath, mountPoint, vers, "", false); err != nil {
		return err
	}
	deletePath := filepath.Join(mountPoint, subDir)
	if _, err := os.Lstat(deletePath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat NFS path %s: %w", deletePath, err)
	}
	markerMatches, err := volumeMarkerExists(deletePath, subDir)
	if err != nil {
		return err
	}
	if !markerMatches {
		return fmt.Errorf("refusing to remove NFS path %s: volume marker is missing", deletePath)
	}
	if archiveOnDelete {
		archivePath := filepath.Join(mountPoint, "archived-"+subDir+time.Now().Format(".2006-01-02-15-04-05"))
		if err := os.Rename(deletePath, archivePath); err != nil {
			return fmt.Errorf("archive NFS path %s to %s: %w", deletePath, archivePath, err)
		}
		return nil
	}
	if err := os.RemoveAll(deletePath); err != nil {
		return fmt.Errorf("remove NFS path %s: %w", deletePath, err)
	}
	return nil
}
