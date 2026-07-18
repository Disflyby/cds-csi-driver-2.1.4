package oss

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	run func(context.Context, string, ...string) error
}

func newS3FSMounter() Mounter {
	return &commandMounter{run: runCommand}
}

func runCommand(ctx context.Context, name string, args ...string) error {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("run %s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (m *commandMounter) Mount(ctx context.Context, opts *PublishOptions, credentialFile string) error {
	return m.run(ctx, "s3fs", s3fsMountArgs(opts, credentialFile)...)
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

func s3fsMountArgs(opts *PublishOptions, credentialFile string) []string {
	args := []string{
		fmt.Sprintf("%s:%s", opts.Bucket, opts.Path),
		opts.NodePublishPath,
		"-o", "passwd_file=" + credentialFile,
		"-o", "url=" + opts.Endpoint,
	}
	if opts.AddressingStyle == ossAddressingStylePath {
		args = append(args, "-o", "use_path_request_style")
	}
	if opts.Region != "" {
		args = append(args, "-o", "region="+opts.Region)
	}
	if opts.SignatureType == ossSignatureTypeV2 {
		args = append(args, "-o", "sigv2")
	}
	for _, option := range defaultS3fsOptions {
		args = append(args, "-o", option)
	}
	return args
}
