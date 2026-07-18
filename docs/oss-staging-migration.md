# OSS node staging migration

Version `v2.2.0` changes OSS mounts from one s3fs process per pod publish target
to one s3fs process per volume and node.

## StorageClass requirements

New dynamic StorageClasses should reference the same Secret for controller,
node-stage, and node-publish operations:

```yaml
parameters:
  csi.storage.k8s.io/provisioner-secret-name: oss-csi-credentials
  csi.storage.k8s.io/provisioner-secret-namespace: default
  csi.storage.k8s.io/node-stage-secret-name: oss-csi-credentials
  csi.storage.k8s.io/node-stage-secret-namespace: default
  csi.storage.k8s.io/node-publish-secret-name: oss-csi-credentials
  csi.storage.k8s.io/node-publish-secret-namespace: default
```

PV objects retain their secret references when their StorageClass changes.
Existing PVs without a node-stage Secret therefore use a compatibility path:
NodeStage succeeds without mounting, and the first NodePublish stages the volume
using its node-publish Secret before creating the bind mount.

Do not remove node-publish Secret references until all pre-v2.2.0 PVs have been
recreated or retired.

## Node rollout

The new DaemonSet does not install `/srv/oss-server`, an `oss.service` unit, or a
host s3fs package. Before rollout, verify every target node has `/dev/fuse` and
supports bidirectional mount propagation for `/var/lib/kubelet`.

After the DaemonSet is healthy, the old host service can be removed during a
separate node-maintenance operation. The driver deliberately does not delete
host packages or systemd units during rollout.
