package oss

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/capitalonline/cds-csi-driver/pkg/driver/oss/mountagent"
)

type Mounter interface {
	Mount(context.Context, *PublishOptions, string) error
	BindMount(context.Context, string, string, bool) error
	Unmount(context.Context, string) error
	UnmountLazy(context.Context, string) error
	CheckReady(context.Context, string) error
	IsMounted(string) (bool, error)
}

type commandMounter struct {
	run        func(context.Context, string, ...string) error
	socketPath string
}

func newGeeseFSMounter() Mounter {
	return &commandMounter{run: runCommand, socketPath: mountagent.SocketPath}
}

func runCommand(ctx context.Context, name string, args ...string) error {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("run %s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (m *commandMounter) Mount(ctx context.Context, opts *PublishOptions, credentialFile string) error {
	request := mountagent.MountRequest{
		Endpoint:        opts.Endpoint,
		Bucket:          opts.Bucket,
		Prefix:          opts.Path,
		Target:          opts.NodePublishPath,
		CredentialFile:  credentialFile,
		AddressingStyle: opts.AddressingStyle,
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", m.socketPath)
	if err != nil {
		return fmt.Errorf("connect to GeeseFS mount agent: %w", err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return fmt.Errorf("send GeeseFS mount request: %w", err)
	}
	response := mountagent.Response{}
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		return fmt.Errorf("read GeeseFS mount response: %w", err)
	}
	if !response.Success {
		if response.Error == "" {
			response.Error = "unknown error"
		}
		return fmt.Errorf("GeeseFS mount agent: %s", response.Error)
	}
	return nil
}

func (m *commandMounter) BindMount(ctx context.Context, source, target string, readOnly bool) error {
	if err := m.run(ctx, "mount", "--bind", source, target); err != nil {
		return err
	}
	if readOnly {
		if err := m.run(ctx, "mount", "-o", "remount,bind,ro", target); err != nil {
			_ = m.run(ctx, "umount", target)
			return err
		}
	}
	return nil
}

func (m *commandMounter) Unmount(ctx context.Context, target string) error {
	return m.run(ctx, "umount", target)
}

func (m *commandMounter) UnmountLazy(ctx context.Context, target string) error {
	return m.run(ctx, "umount", "-l", target)
}

func (m *commandMounter) CheckReady(ctx context.Context, target string) error {
	return m.run(ctx, "ls", "-A", "--", target)
}

func (m *commandMounter) IsMounted(target string) (bool, error) {
	mountInfo, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false, err
	}
	wanted := filepath.Clean(target)
	for _, line := range strings.Split(string(mountInfo), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		if filepath.Clean(unescapeMountInfoPath(fields[4])) == wanted {
			return true, nil
		}
	}
	return false, nil
}

func unescapeMountInfoPath(value string) string {
	replacer := strings.NewReplacer("\\040", " ", "\\011", "\t", "\\012", "\n", "\\134", "\\")
	return replacer.Replace(value)
}
