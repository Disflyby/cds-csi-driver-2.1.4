package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	pathpkg "path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/capitalonline/cds-csi-driver/pkg/driver/oss/mountagent"
)

const (
	geesefsBinary = "/usr/local/bin/geesefs"
	mountTimeout  = 60 * time.Second
)

var runGeeseFS = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, geesefsBinary, args...).CombinedOutput()
}

func main() {
	if err := serve(mountagent.SocketPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(socketPath string) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0755); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	defer listener.Close()
	if err := os.Chmod(socketPath, 0660); err != nil {
		return fmt.Errorf("set socket permissions: %w", err)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		_ = listener.Close()
	}()

	for {
		connection, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept mount request: %w", err)
		}
		go handleConnection(connection)
	}
}

func handleConnection(connection net.Conn) {
	defer connection.Close()
	request := mountagent.MountRequest{}
	if err := json.NewDecoder(connection).Decode(&request); err != nil {
		writeResponse(connection, mountagent.Response{Error: "invalid mount request"})
		return
	}
	writeResponse(connection, executeMount(request))
}

func executeMount(request mountagent.MountRequest) mountagent.Response {
	if err := validateMountRequest(request); err != nil {
		return mountagent.Response{Error: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), mountTimeout)
	defer cancel()
	output, err := runGeeseFS(ctx, geesefsArgs(request)...)
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return mountagent.Response{Error: "geesefs mount failed: " + message}
	}
	return mountagent.Response{Success: true}
}

func validateMountRequest(request mountagent.MountRequest) error {
	endpoint, err := url.Parse(request.Endpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return errors.New("invalid endpoint")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return errors.New("invalid endpoint")
	}
	if !validBucket(request.Bucket) {
		return errors.New("invalid bucket")
	}
	if request.AddressingStyle != "path" && request.AddressingStyle != "virtual" {
		return errors.New("invalid addressing style")
	}
	if !safePrefix(request.Prefix) {
		return errors.New("invalid prefix")
	}
	if !safeMountTarget(request.Target) {
		return errors.New("invalid mount target")
	}
	if !safeCredentialFile(request.CredentialFile) {
		return errors.New("invalid credential file")
	}
	return nil
}

func geesefsArgs(request mountagent.MountRequest) []string {
	source := request.Bucket + ":" + strings.TrimPrefix(request.Prefix, "/")
	args := []string{
		"--endpoint", request.Endpoint,
		"--shared-config", request.CredentialFile,
		"--profile", "default",
		"--list-type", "2",
		"--memory-limit", "256",
		"--use-enomem",
		"--sdk-max-retries", "2",
		"--http-timeout", "30s",
		"--dir-mode", "0750",
		"--file-mode", "0640",
	}
	if request.AddressingStyle == "virtual" {
		args = append(args, "--subdomain")
	}
	args = append(args, "-o", "allow_other", source, request.Target)
	return args
}

func validBucket(bucket string) bool {
	if bucket == "" || strings.ContainsAny(bucket, "\x00\r\n") {
		return false
	}
	for _, char := range bucket {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '.' && char != '-' {
			return false
		}
	}
	return true
}

func safePrefix(prefix string) bool {
	if strings.ContainsAny(prefix, "\x00\r\n") {
		return false
	}
	for _, segment := range strings.Split(strings.Trim(prefix, "/"), "/") {
		if segment == ".." {
			return false
		}
	}
	return true
}

func safeMountTarget(target string) bool {
	clean := pathpkg.Clean(target)
	return strings.HasPrefix(clean, "/") &&
		strings.Contains(clean, "/plugins/kubernetes.io/csi/oss.csi.cds.net/") &&
		pathpkg.Base(clean) == "globalmount"
}

func safeCredentialFile(credentialFile string) bool {
	clean := pathpkg.Clean(credentialFile)
	return strings.HasPrefix(clean, "/") &&
		strings.Contains(clean, "/plugins/oss.csi.cds.net/credentials/") &&
		strings.HasSuffix(clean, ".credentials")
}

func writeResponse(connection net.Conn, response mountagent.Response) {
	_ = json.NewEncoder(connection).Encode(response)
}
