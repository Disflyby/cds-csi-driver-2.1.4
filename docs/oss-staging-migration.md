# OSS GeeseFS host-mounter rollout

Version `v2.3.0` replaces the old mount implementation with one GeeseFS mount
per volume and node. GeeseFS and `geesefs-mount-agent` run on the Kubernetes
host under systemd; the CSI Node Pod reaches the agent through a Unix socket.

This release does not support old StorageClasses or PVs. Recreate test storage
resources with the current parameter and Secret format before rollout.

## StorageClass requirements

Use only a service endpoint, bucket, and optional base path. The driver probes
path-style and virtual-host-style addressing automatically.

```yaml
parameters:
  endpoint: https://s3.example.com
  bucket: project-data
  path: /
  csi.storage.k8s.io/provisioner-secret-name: oss-csi-credentials
  csi.storage.k8s.io/provisioner-secret-namespace: default
  csi.storage.k8s.io/node-stage-secret-name: oss-csi-credentials
  csi.storage.k8s.io/node-stage-secret-namespace: default
  csi.storage.k8s.io/node-publish-secret-name: oss-csi-credentials
  csi.storage.k8s.io/node-publish-secret-namespace: default
```

The referenced Secret must contain `akId` and `akSecret`.

## Node rollout

On every schedulable node:

1. Remove the legacy mount package.
2. Install FUSE3 and enable `user_allow_other` in `/etc/fuse.conf`.
3. Install `/usr/local/bin/geesefs` and
   `/usr/local/bin/geesefs-mount-agent`.
4. Enable and start `geesefs-mount-agent.service`.
5. Verify `/var/run/geesefs-mount-agent/geesefs.sock` exists.
6. Deploy the `v2.3.0` CSI controller and node manifests.

The Node Pod does not mount `/dev/fuse`. It mounts the agent runtime directory
and the kubelet root with bidirectional propagation. Active GeeseFS processes
remain in the host systemd cgroup when the CSI Node Pod restarts.
