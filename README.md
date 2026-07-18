# cds-csi-driver 

中文项目速读与架构说明：[ONBOARDING.zh-CN.md](ONBOARDING.zh-CN.md)

A Container Storage Interface (CSI) Driver for Capitalonline(aka. CDS) cloud. This CSI plugin allows you to use CDS storage with the kubernetes cluster hosted or managed by CDS.

It currently supports CDS File Storage(NAS) and CDS Object Storage Service(OSS).
The support for the Block Storage will be added soon.

## To deploy

To deploy the CSI NAS driver to your k8s, simply run:
```bash
kubectl create -f https://raw.githubusercontent.com/capitalonline/cds-csi-driver/master/deploy/nas/deploy.yaml
```

To deploy the CSI OSS driver to your k8s, simply run:

```bash
# need install s3fs in host
# ubuntu OS
apt install s3fs -y

# centos OS
yum install s3fs -y

# install csi 
kubectl create -f https://raw.githubusercontent.com/capitalonline/cds-csi-driver/master/deploy/oss/deploy.yaml
```

To deploy the CSI EBS-DISK driver to your k8s, simply run:
- deploy and update the driver settings
```bash
kubectl create -f https://raw.githubusercontent.com/capitalonline/cds-csi-driver/master/deploy/ebs_disk/base.yaml
```

- set base64 access_key_id/access_key_secret into secret cck-secrets
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cck-secrets
  namespace: kube-system
type: Opaque
data:
  access_key_id: ''
  access_key_secret: ''
```

PS: generate base64 ak/sk
```python
import base64

# write here
ak = ''
sk = ''
access_key_id = str(base64.b64encode(str.encode(ak)), encoding="utf-8")
print('access_key_id:\n', access_key_id)
access_key_secret = str(base64.b64encode(str.encode(sk)), encoding="utf-8")
print('access_key_secret:\n', access_key_secret)

```

- deploy driver service
```bash
kubectl create -f https://raw.githubusercontent.com/capitalonline/cds-csi-driver/master/deploy/ebs_disk/deploy.yaml
```

To deploy the CSI CCS-DISK driver to your k8s, simply run:
- deploy and update the driver settings
```bash
kubectl create -f https://raw.githubusercontent.com/capitalonline/cds-csi-driver/master/deploy/ccs_disk/base.yaml
```

- set base64 access_key_id/access_key_secret into secret ccs-secrets
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: ccs-secrets
  namespace: kube-system
type: Opaque
data:
  access_key_id: ''
  access_key_secret: ''
```

PS: generate base64 ak/sk
```python
import base64

# write here
ak = ''
sk = ''
access_key_id = str(base64.b64encode(str.encode(ak)), encoding="utf-8")
print('access_key_id:\n', access_key_id)
access_key_secret = str(base64.b64encode(str.encode(sk)), encoding="utf-8")
print('access_key_secret:\n', access_key_secret)

```

- deploy driver service
```bash
kubectl create -f https://raw.githubusercontent.com/capitalonline/cds-csi-driver/master/deploy/ccs_disk/deploy.yaml
```

## To run tests

**NAS:**

1. Make sure that you have a Kubernetes cluster accessible with `kubectl`
2. Provision a nfs server with exports `/nfsshare *(insecure,rw,async,no_root_squash,fsid=1000)`
3. Run `make test-prerequisite` to build the image and deploy the driver to your k8s cluster
4. Run `sudo NFS=<your nfs server ip> make test`

**OSS:**

1. Make sure that you have a Kubernetes cluster accessible with `kubectl`
2. Register a Storage Service in CDS 
3. Run `make test-prerequisite` to build the image and deploy the driver to your k8s cluster
4. Run `make oss-test`


## To use the NAS driver
Examples can be found [here](!https://github.com/capitalonline/cds-csi-driver/tree/master/example/nas)

### Static PV

pv.yaml

```yaml
spec:
  csi:
    driver: nas.csi.cds.net
    volumeHandle: <name of the pv>
    volumeAttributes:
      server: "140.210.75.196"
      path: "/nfsshare/nas-csi-cds-pv"
```

Description:

|     Key      | Value                              | Required | Description                                                  |
| :----------: | :--------------------------------- | :------: | :----------------------------------------------------------- |
|    driver    | nas.csi.cds.net                    |   yes    | CDS csi driver's name                                        |
| volumeHandle | <pv_name>                          |   yes    | Should be same with pv name                                  |
|    server    | e.g. <br />140.210.75.195          |   yes    | the mount point of the nfs server,can be found on NAS product list |
|     path     | e.g.<br />/nfsshare/division1      |   yes    | the path that the pv will use on the nfs server. It must start with `/nfsshare`. Each pv will use a separate directory(e.g `/nfsshare/division1/volumeHandleValue"`when `allowShared` is `false`. Otherwsise, the path of pv will be the same to `path`, which means all PVs with the same path will share data |
|     vers     | `3` or `4.0`                       |    no    | the protocol version to use for the nfs mount. if not provided, `4.0` will be used by default |
|   options    | e.g. `noresvport`                  |    no    | options for the nfs mount. If not provided it will be set to `noresvport` for `vers=4.0` and `noresvport,nolock,tcp` for `vers=3` |
|     mode     | e.g. `0777`                        |    no    | to customize the mode of the path created on nfs server. If not provided, the server's default mask will be used |
|   modeType   | e.g `recursive` or `non-recursive` |    no    | This tells the driver if the chmod should be done recursively or not. Only works when `mode` is specified. Default to `non-recursive` |
| allowShared  | `"true"` or `"false"`              |    no    | default to `"false`. If set, the data of all PVs with the same path will be shared |

### Dynamic PV

#### volumeAs: subpath 

sc.yaml

```yaml
provisioner: nas.csi.cds.net
reclaimPolicy: Delete
parameters:
  volumeAs: subpath
  servers: 140.210.75.194/nfsshare/nas-csi-cds-pv, 140.210.75.195/nfsshare/nas-csi-cds-pv
  server: 140.210.75.195
  path: /nfsshare
  vers: 4.0
  threshold: "90"
  options: noresvport
  archiveOnDelete: true
```

Description:

|       Key       | Value                                                        | Required | Description                                                  |
| :-------------: | :----------------------------------------------------------- | :------: | :----------------------------------------------------------- |
|   provisioner   | nas.csi.cds.net                                              |   yes    | CDS csi driver's name                                        |
|  reclaimPolicy  | Delete \| Retain                                             |   yes    | `Delete` means that PV will be deleted with PVC delete<br />`Retain` means that PV will be retained when PVC delete |
|    volumeAs     | subpath                                                      |    no    | Default is `subpath`. Each PV is provisioned into an isolated directory beneath the configured NFS export. |
|     servers     | eg.<br />140.210.75.194/nfsshare/nas-csi-cds-pv, 140.210.75.195/nfsshare/nas-csi-cds-pv |    no    | multi servers are supported by a StorageClass. It should be separated by "," between each server/path. |
|     server      | e.g. <br />140.210.75.195                                    |    no    | the mount point of the nfs server,can be found on NAS product list |
|    threshold    | `0.9` or `90`                                                |    no    | Maximum eligible NAS usage. Values in `[0,1]` are fractions; values in `[0,100]` are percentages. Default `1` means 100%. |
|      path       | e.g. <br />/nfsshare                                         |    no    | the root path that the pv will dynamically provisioned on the NAS server. It must start with `/nfsshare`. |
|      vers       | `3` or `4.0`                                                 |    no    | the protocol version to use for the nfs mount. if not provided, `4.0` will be used by default |
|     options     | e.g. `noresvport`                                            |    no    | options for the nfs mount. If not provided it will be set to `noresvport` for `vers=4.0` and `noresvport,nolock,tcp` for `vers=3` |
|      mode       | e.g. `0777`                                                  |    no    | to customize the mode of the path created on nfs server. If not provided, the server's default mask will be used |
|    modeType     | e.g `recursive` or `non-recursive`                           |    no    | This tells the driver if the chmod should be done recursively or not. Only works when `mode` is specified. Default to `non-recursive` |
| archiveOnDelete | `"true"` or `"false"`                                        |    no    | only works when the `reclaim police` is `delete`. if `true`, the content of the pv will be archived on nfs instead of be deleted. Default to `false` |

Kindly Remind: 

​	a) server and path are as a whole to use.

​	b) servers and server cant be empty together. It means that servers or server is not empty at least in one yaml. 

​	c) With multiple eligible servers, the driver selects the server deterministically from the generated PV name. The remote directory and its `.csi-volume` marker make a retried CreateVolume response stable after a controller restart.

​	d) `modeType: recursive` is not supported. Use the default `non-recursive` mode change or manage recursive permissions outside CSI.

#### volumeAs: filesystem

Dynamic `volumeAs: filesystem` provisioning is disabled. Its create workflow depended on controller memory and returned intentional errors between NAS API calls, so it could not provide CSI idempotency after a controller restart. Existing filesystem PVs remain mountable and retain their legacy delete path; create new dynamic NAS volumes with `volumeAs: subpath`.

## To use the OSS driver

Examples can be found [here](!https://github.com/capitalonline/cds-csi-driver/tree/master/example/oss)

### Dynamic PV

Dynamic OSS volumes allocate one generated prefix per PVC beneath the StorageClass `path`.
Create the credential Secret before applying the StorageClass, then apply the PVC. The
StorageClass must set `csi.storage.k8s.io/provisioner-secret-*` and
`csi.storage.k8s.io/node-stage-secret-*`. Keep
`csi.storage.k8s.io/node-publish-secret-*` during migration so PVs created by an
older driver can still stage during publish. The external provisioner uses the
provisioner Secret for both CreateVolume and DeleteVolume. Credentials are not
stored in the generated PV.
With `reclaimPolicy: Delete`, the driver deletes only the generated prefix for that
PVC. Use `Retain` to preserve its objects after the PVC is removed.

Two endpoint forms are supported:

* `endpointMode: service`: set a service endpoint plus `bucket`.
* `endpointMode: bucket`: set a standard virtual-host endpoint such as
  `https://bucket.oss.example.com`, or a path endpoint such as
  `https://minio.example.com/bucket`. The driver derives the bucket and stores a
  canonical service endpoint internally.

Opaque CNAME and access-point endpoints cannot be reversed reliably. Configure
their service endpoint and explicit bucket instead. `url` remains an alias for
`endpoint` for existing PVs and StorageClasses.

Set `addressingStyle: auto` to probe path and virtual-host addressing during
dynamic provisioning and persist the result in the PV. Explicit `path` and
`virtual` remain available; omitted values default to `path` for backward
compatibility.

The OSS node plugin advertises CSI stage/unstage support. It mounts s3fs once per
volume and node, then bind-mounts the staging path into each pod. Existing PVs
without a node-stage Secret are staged during the first NodePublish call using
their node-publish Secret.

Runnable manifests are available in `example/oss/dynamic/`.

### Static PV 

pv.yaml

```yaml
spec:
  csi:
    driver: oss.csi.cds.net
    # set volumeHandle same value pv name
    volumeHandle: <name of the pv>
    volumeAttributes:
      bucket: "***"
      endpoint: "http://oss-cnbj01.cdsgss.com"
      endpointMode: "service"
      akId: "***"
      akSecret: "***"
      path: "***"
```

Description:

| Key          | Value                          | Required | Description                              |
| ------------ | ------------------------------ | -------- | ---------------------------------------- |
| driver       | oss.csi.cds.net                | yes      | CDS csi driver's name                    |
| volumeHandle | <name of the pv>               | yes      | Should be same with pv name              |
| bucket       | <bucket name>                  | yes      | Register in CDS and get one unique name  |
| endpoint     | `http://oss-cnbj01.cdsgss.com` | yes      | S3 service endpoint; legacy `url` is also accepted |
| akId         | <access_key>                   | yes      | Get it from your own bucket's in CDS web |
| akSecret     | <acckedd_key_secret>           | yes      | Get it from your own bucket's in CDS web |
| path         | <bucket_path>                  | yes      | Bucket path, default is `/`              |

### OSS node image

The OSS deployment uses a dedicated image containing the CSI binary, FUSE tools,
and a checksum-pinned s3fs build. It no longer installs packages or a systemd
service on Kubernetes nodes. Build the image with:

```bash
make oss-image OSS_IMAGE=harbor-dev.yun-paas.com/storage_image/cds-csi-driver-oss OSS_VERSION=v2.2.0
```

The node must expose `/dev/fuse`, and `/var/lib/kubelet` must be mounted into the
node plugin with `mountPropagation: Bidirectional`, as configured by the release
manifest.

## To use the EBS-DISK driver
Examples can be found [here](!https://github.com/capitalonline/cds-csi-driver/tree/master/example/ebs_disk)

### Dynamic Pv

sc.yaml

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: ebs-disk-csi-cds-sc
parameters:
  fsType: ext4
  region: SR_SaoPaulo
  zone: SR_SaoPaulo_A
  storageType: SSD
provisioner: ebs-disk.csi.cds.net
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
allowedTopologies:
- matchLabelExpressions:
  - key: topology.kubernetes.io/region
    values:
    - SR_SaoPaulo
  - key: topology.kubernetes.io/zone
    values:
    - SR_SaoPaulo_A
```

Description:

| Key               | Value                | Required | Description                                                                                                        |
|-------------------|----------------------|----------|--------------------------------------------------------------------------------------------------------------------|
| fsType            | [ xfs\|ext4 ]        | yes      | Linux filesystem type                                                                                              |
| storageType       | [ ssd_disk ]         | yes      | Only support `ssd_disk`.<br />                                                                                     |
| zong              | Eg. "SR_SaoPaulo_A"  | yes      | Cluster's az code.                                                                                                   |
| region            | Eg. "SR_SaoPaulo"    | yes      | Cluster's region code |
| provisioner       | ebs-disk.csi.cds.net | yes      | Disk driver which installed default.                                                                               |
| reclaimPolicy     | [ Delete\|Retain ]   | yes      | `Delete` means that PV will be deleted with PVC delete<br/>`Retain` means that PV will be retained when PVC delete |
| volumeBindingMode | WaitForFirstConsumer | yes      | Only suport `WaitForFirstConsumer` pollicy for disk.csi.cds.net driver.                                            |
| allowedTopologies | matchLabelExpressions | no      | Only suport `topology.kubernetes.io/zone`, `topology.kubernetes.io/region` pollicy for disk.csi.cds.net driver.                                            |

Kindly Remind:

​	For disk storage, recommending using `volumeBindingMode:` `WaitForFirstConsumer ` in SC.yaml.

​	If not, please apply your `ebs-disk.csi.cds.net` csi driver's `csi-provisioner` in k8s with following:

## To use the EKS-DISK driver

Examples can be found [here](!https://github.com/capitalonline/cds-csi-driver/tree/master/example/ebs_disk)

### Dynamic Pv

sc.yaml

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: test-sc-retain
parameters:
  fsType: ext4
  storageType: SSD
  region: CN_Suqian
  zone: CN_Suqian_B
provisioner: eks-disk.csi.cds.net
reclaimPolicy: Retain
volumeBindingMode: WaitForFirstConsumer    
```

Description:

| Key               | Value                | Required | Description                                                  |
| ----------------- | -------------------- | -------- | ------------------------------------------------------------ |
| fsType            | [ xfs\|ext4 ]        | yes      | Linux filesystem type                                        |
| storageType       | [ SSD ]              | yes      | Only support `ssd_disk`                                      |
| zone              | Eg. " CN_Suqian_B"   | yes      | Cluster's az code.                                             |
| region            | Eg. " CN_Suqian"   | yes      | Cluster's region code.                                             |
| provisioner       | eks-disk.csi.cds.net | yes      | Disk driver which installed default.                         |
| reclaimPolicy     | [ Delete\|Retain ]   | yes      | `Delete` means that PV will be deleted with PVC delete<br/>`Retain` means that PV will be retained when PVC delete |
| volumeBindingMode | WaitForFirstConsumer | yes      | Only suport `WaitForFirstConsumer` pollicy for disk.csi.cds.net driver. |

Kindly Remind:

​	For disk storage, recommending using `volumeBindingMode:` `WaitForFirstConsumer ` in SC.yaml.

​	If not, please apply your `eks-disk.csi.cds.net` csi driver's `csi-provisioner` in k8s with following:


## To use the Disk driver 

### Dynamic PV

sc.yaml

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: disk-csi-cds-sc
parameters:
  fsType: "ext4"			
  storageType: "high_disk"
  iops: "3000"
  siteId: "beijing001"
  zoneId: "WuxiA-POD10-CLU02"
provisioner: disk.csi.cds.net
reclaimPolicy: Retain
volumeBindingMode: WaitForFirstConsumer    
```

Description:

| Key               | Value                     | Required | Description                                                  |
| ----------------- | ------------------------- | -------- | ------------------------------------------------------------ |
| fsType            | [ xfs\|ext4\|ext3 ]       | yes      | Linux filesystem type                                        |
| storageType       | [ high_disk\|ssd_disk ]   | yes      | Only support `high_disk` and `ssd_disk`.<br />`high_disk` shoud be with iops `3000`.<br />`ssd_disk` should be with iops `[5000|7500|10000]`. |
| iops              | [3000\|5000\|7500\|10000] | yes      | Only support `3000` `5000` `7500` and `1000`.<br />Should combined with `storageType`. |
| siteId            | Eg. "Beijing001"          | yes      | Cluster's site_id.                                           |
| zoneId            | Eg. "Beijing-POD10-CLU02" | yes      | Cluster's id.                                                |
| provisioner       | disk.csi.cds.net          | yes      | Disk driver which installed default.                         |
| reclaimPolicy     | [ Delete\|Retain ]        | yes      | `Delete` means that PV will be deleted with PVC delete<br/>`Retain` means that PV will be retained when PVC delete |
| volumeBindingMode | WaitForFirstConsumer      | yes      | Only suport `WaitForFirstConsumer` pollicy for disk.csi.cds.net driver. |

Kindly Remind: 

​	For disk storage, recommending using `volumeBindingMode:` `WaitForFirstConsumer ` in SC.yaml. 

​	If not, please apply your `disk.csi.cds.net` csi driver's `csi-provisioner` in k8s with following:

```yaml
- args: 
    - "--csi-address=$(ADDRESS)"
    - "--v=5"
    - "--feature-gates=Topology=true"			# 添加这个参数，开启 Topology 
  env: 
    - 
      name: ADDRESS
      value: /socketDir/csi.sock
  image: "harbor-dev.yun-paas.com/csi_agent/csi-provisioner:v3.1.0"
  name: csi-provisioner
  volumeMounts: 
    - 
      mountPath: /socketDir
      name: socket-dir
```

and then refer the following SC.yaml

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: disk
parameters:
  fstype: "ext4" 
  storageType: "high_disk"
  iops: "3000"
  siteId: "ca0bd848-9b59-40a2-9f57-d64fbc72a9df"
provisioner: disk.csi.cds.net
reclaimPolicy: Delete
volumeBindingMode: Immediate    # Immediate policy in SC 
allowedTopologies:				# using allowedTopologies in sc 
- matchLabelExpressions:
  - key: topology.kubernetes.io/zone	
    values:
    - WuxiA-POD10-CLU02
```



## To use the CCS-DISK driver
Examples can be found [here](!https://github.com/capitalonline/cds-csi-driver/tree/master/example/ccs_disk)

### Dynamic Pv

sc.yaml

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: ccs-disk-csi-cds-sc
parameters:
  fsType: ext4
  iops: "3000"
  siteId: "xxx"
  zoneId: Cluster_Name1
  storageType: "ssd_disk"
provisioner: ccs-disk.csi.cds.net
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
```

Description:

| Key               | Value                     | Required | Description                                                                                                        |
|-------------------|---------------------------|----------|--------------------------------------------------------------------------------------------------------------------|
| fsType            | [ xfs\|ext4\|ext3 ]       | yes      | Linux filesystem type                                                                                              |
| storageType       | [    ssd_disk     ]       | yes      | Only support `ssd_disk`                                  |
| iops              | [5000\|7500\|10000]       | yes      | Only support `5000` `7500` and `10000`.<br />Should combined with `storageType`.                            |
| siteId            | Eg. "Beijing001"          | yes      | Cluster's site_id.                                                                                                 |
| zoneId            | Eg. "Beijing-POD10-CLU02" | yes      | Cluster's id.                                                                                                      |
| provisioner       | ccs-disk.csi.cds.net      | yes      | Disk driver which installed default.                                                                               |
| reclaimPolicy     | [ Delete\|Retain ]        | yes      | `Delete` means that PV will be deleted with PVC delete<br/>`Retain` means that PV will be retained when PVC delete |
| volumeBindingMode | WaitForFirstConsumer      | yes      | Only suport `WaitForFirstConsumer` pollicy for disk.csi.cds.net driver.                                            |

Kindly Remind:

​	For disk storage, recommending using `volumeBindingMode:` `WaitForFirstConsumer ` in SC.yaml.

​	If not, please apply your `ccs-disk.csi.cds.net` csi driver's `csi-provisioner` in k8s with following:
