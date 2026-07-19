# CDS CSI Driver 快速导读

这是一套面向 Capitalonline CDS 云存储的 Kubernetes CSI 驱动。它不是六个独立服务，而是一个 Go 二进制；部署时由 `--driver` 参数选择一个后端实现。

适合先读完本文，再按需要进入对应的 `pkg/driver/<backend>` 目录。

## 30 秒建立模型

```text
PVC + StorageClass
        |
Kubernetes CSI sidecar (provisioner / attacher)
        |
Unix socket
        |
cmd/main.go --driver=<driver name>
        |
CSI Identity / Controller / Node Server
        |----------------------------|
   CDS Cloud API                 Linux mount / format / bind mount
```

节点侧的挂载结果最终位于 kubelet 管理的路径；因此驱动 DaemonSet 需要特权权限、`/var/lib/kubelet` 的双向挂载传播，以及块存储场景下的 `/dev`。

## 支持的后端

| 驱动名 | 模型 | 核心用途 | 关键差异 |
| --- | --- | --- | --- |
| `nas.csi.cds.net` | NFS，共享读写 | NAS 静态/动态卷 | `subpath` 创建目录；`filesystem` 创建独占 CDS NAS |
| `oss.csi.cds.net` | GeeseFS，共享读写 | 将 OSS Bucket 挂载到节点 staging path，再 bind mount 到 Pod | 宿主机 GeeseFS 由 systemd mount agent 管理 |
| `disk.csi.cds.net` | 云块盘 | 传统 CDS Disk | 控制器会轮询云任务；可定时同步 PV/Node topology |
| `ccs-disk.csi.cds.net` | 云块盘，单节点写 | CCS 集群磁盘 | ConfigMap 持久化卷记录，并使用每卷锁 |
| `ebs-disk.csi.cds.net` | 云块盘，单节点写 | EBS 磁盘 | 格式化状态保存到 `kube-system/ebs-formated` |
| `eks-disk.csi.cds.net` | 云块盘 | EKS 场景块存储 | 内置 HMAC 签名 OpenAPI 客户端，云端记录格式化状态 |

除 NAS、OSS 外，其他四种后端都使用相同的块存储流程：

```text
CreateVolume -> 云 API 创建卷 -> ControllerPublishVolume 附着到节点
-> NodeStageVolume 扫描设备、格式化、挂载到 staging path
-> NodePublishVolume bind mount 到 Pod
-> NodeUnpublish/Unstage -> ControllerUnpublishVolume -> DeleteVolume
```

NAS、OSS 不需要控制器附着：`CSIDriver.spec.attachRequired: false`。NAS 节点直接 NFS 挂载；OSS 的 `NodeStageVolume` 通过 Unix socket 请求宿主机 mount agent 启动 GeeseFS，再由 `NodePublishVolume` bind mount 到 Pod。

## 从哪里开始读

| 目标 | 文件/目录 | 说明 |
| --- | --- | --- |
| 驱动选择与启动 | `cmd/main.go` | 解析 `--endpoint`、`--driver`、`--nodeid`、`--rootdir` 并启动 gRPC 服务 |
| 统一能力与系统操作 | `pkg/driver/utils/` | 节点 ID、shell 命令、挂载检查、目录、容量指标、EKS HTTP 签名客户端 |
| NAS | `pkg/driver/nas/` | NFS 参数、NAS API 调用、服务器选择和目录生命周期 |
| OSS | `pkg/driver/oss/`、`cmd/geesefs-mount-agent/`、`Dockerfile.oss` | Endpoint 规范化、宿主机 GeeseFS 代理和 staging 生命周期 |
| 传统 Disk | `pkg/driver/disk/` | CDS Disk SDK 与块设备操作 |
| CCS Disk | `pkg/driver/ccsdisk/` | 持久化卷记录、并发锁、CCS Disk SDK |
| EBS Disk | `pkg/driver/ebs_disk/` | EBS SDK、设备 order 定位、格式化 ConfigMap |
| EKS Disk | `pkg/driver/eks_block/` | 自研 API models/client 与 EKS 块存储流程 |
| 部署清单 | `deploy/<backend>/` | controller、node、CSIDriver、Kustomize overlay、RBAC |
| 示例 | `example/<backend>/` | StorageClass、PV/PVC、Pod 清单 |

每个后端通常由四类文件组成：

- `identityserver.go`：声明 CSI 能力和访问模式。
- `controllerserver.go`：创建、删除、附着和卸载云卷。
- `nodeserver.go`：节点上的格式化、挂载、bind mount 和清理。
- `utils.go` / `types.go` / `const.go`：参数校验、后端状态与设备操作。

## 重要调用链

### 入口和身份

`cmd/main.go` 会根据 `--driver` 构造 `NewDriver(...)`。所有后端均使用 `github.com/kubernetes-csi/drivers/pkg/csi-common` 提供的 gRPC server 和默认 CSI service，再覆写所需 RPC。

未显式传入 `--nodeid` 时，普通后端从 `/host/etc/cds/node-meta` 读取节点 ID；读取失败会尝试从 cloud-init 磁盘恢复。CCS Disk 读取 DMI `product_uuid`。

### NAS

`volumeAs=subpath` 在已有 NFS 服务器的 `/nfsshare` 下创建 PVC 对应目录。StorageClass 可配置多个服务器，按 `RoundRobin` 或 `Random` 选择，并通过 CDS NAS 使用率 API 排除超过阈值的服务器。

`volumeAs=filesystem` 会调用 CDS API 创建 NAS、挂载到指定集群，然后在其上创建目录。删除时由 `deleteNas` 决定是保留还是卸载并删除整个 NAS。

### 块存储节点操作

块存储都会先让云平台附着磁盘，节点再扫描设备并按 `fsType` 执行 `mkfs.xfs`、`mkfs.ext3` 或 `mkfs.ext4`。之后磁盘被挂到 staging path，再对每个 Pod target path 做 `mount --bind`。

设备定位并不相同：传统 Disk/CCS Disk 按云盘 UUID 扫描；EBS/EKS 根据 QEMU SCSI order 在 `/dev/disk/by-id` 定位设备。

## 部署形态与配置

- NAS、Disk、EBS、EKS：Kustomize 的 `base` + `overlays/release`；生成文件为 `deploy/<backend>/deploy.yaml`。
- CCS：主要使用平铺的 `deploy/ccs_disk/deploy.yaml`。
- 控制器使用 StatefulSet（1 副本）；节点使用 DaemonSet。
- 块存储控制器会使用 provisioner 和 attacher sidecar；EBS 清单还包含 resizer，但驱动的扩容 RPC 当前仍返回 `Unimplemented`。

常见运行时配置：

| 后端 | Secret / ConfigMap | 用途 |
| --- | --- | --- |
| NAS、Disk、EBS | `cck-secrets`、`cds-properties` | Access Key/Secret、海外标记、部分集群 ID |
| CCS | `ccs-secrets`、`cds-properties` | Access Key/Secret、站点和海外标记 |
| EKS | `eks-secrets`、`cds-properties` | `CDS_ACCESS_KEY_*`、集群 ID、OpenAPI host |
| OSS | StorageClass + Secret | endpoint、bucket、path；AK/SK 仅保存在 Secret 中 |

## 本地开发与验证

项目目标平台是 Linux。OSS CSI 镜像只包含驱动本身；GeeseFS 与 mount agent 由 manager 安装到宿主机。

在 Linux 或 Linux 容器中：

```bash
go test ./...
make build
make unit-test
```

在 Windows 主机上，不能直接运行完整测试：共享容量统计使用 Linux 专属 `unix.Statfs`。可只交叉编译验证：

```powershell
cmd /c "set GOOS=linux&&set GOARCH=amd64&&set CGO_ENABLED=0&&go build ./..."
```

已验证上面的 Linux 目标交叉编译可通过。`make integration-test` 会依次运行 NAS 和 OSS 集成脚本，需要可访问的 Kubernetes 集群；NAS 还需要 NFS 服务器，OSS 需要真实 Bucket、宿主机 FUSE3/GeeseFS/mount agent，以及 `/var/lib/kubelet` 的双向 mount propagation。

## 先知道的限制和风险

1. OSS 发布版本为 `v2.3.0`；其他后端仍有自己的历史版本。部署前必须确认实际镜像与代码版本。
2. CSI/Kubernetes 依赖较旧：CSI spec 为 `v1.2.0`，Kubernetes client 为 `v0.17`，sidecar 版本也并不统一。升级集群前需要做兼容性验证。
3. 自动化测试覆盖很少：仅有一个 Go 单测；集成脚本主要覆盖 NAS 和 OSS，Disk/EBS 测试脚本为空。
4. OSS 测试脚本中曾提交明文访问凭证。不要复用；应轮换凭证并改为通过 Secret 或 CI 密钥注入。
5. 节点组件具有高权限和宿主机挂载权限。任何涉及 `utils.RunCommand`、挂载路径、设备扫描或 RBAC 的改动都应在隔离集群验证。
6. 多数后端依赖内存状态或云端查询来恢复幂等性；CCS 和 EBS 额外使用 ConfigMap 保存部分状态。修改重试或并发逻辑时要验证 Controller 重启场景。

## 修改前检查清单

- 新增后端时，先在 `cmd/main.go` 注册驱动名，再补齐 Identity、Controller、Node、部署和 RBAC。
- 修改 StorageClass 参数时，同时更新 `parse*VolumeOptions`、README 和 `example/`。
- 修改 CSI 能力时，检查 identity server、实际 RPC、CSIDriver 的 `attachRequired` 与 sidecar 是否一致。
- 修改格式化或卸载逻辑时，验证重复调用、节点重启、设备忙碌和云盘已附着到异常节点的场景。
- 发版前，运行 `make sync-version`/`make kustomize` 或等价流程，并检查所有生成的 `deploy.yaml` 镜像标签。
