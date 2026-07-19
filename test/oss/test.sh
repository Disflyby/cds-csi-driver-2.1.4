#!/usr/bin/env bash
set -euo pipefail

: "${BUCKET:?set BUCKET to the S3 bucket name}"
: "${ENDPOINT:?set ENDPOINT to the S3 service endpoint}"
: "${AKID:?set AKID to the access key ID}"
: "${AKSECRET:?set AKSECRET to the access key secret}"

NAMESPACE="${NAMESPACE:-csi-test}"
NODE_NAME="${NODE_NAME:-worker001}"
SECRET_NAME="oss-csi-test-credentials"
PV_NAME="oss-csi-test-pv"
PVC_NAME="oss-csi-test-pvc"
POD_NAME="oss-csi-test"

cleanup() {
    kubectl -n "${NAMESPACE}" delete pod "${POD_NAME}" --ignore-not-found --wait=true || true
    kubectl -n "${NAMESPACE}" delete pvc "${PVC_NAME}" --ignore-not-found --wait=true || true
    kubectl delete pv "${PV_NAME}" --ignore-not-found --wait=true || true
    kubectl -n "${NAMESPACE}" delete secret "${SECRET_NAME}" --ignore-not-found || true
}
trap cleanup EXIT

kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "${NAMESPACE}" create secret generic "${SECRET_NAME}" \
    --from-literal=akId="${AKID}" \
    --from-literal=akSecret="${AKSECRET}" \
    --dry-run=client -o yaml | kubectl apply -f -

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: PersistentVolume
metadata:
  name: ${PV_NAME}
spec:
  capacity:
    storage: 1Gi
  accessModes:
    - ReadWriteMany
  persistentVolumeReclaimPolicy: Retain
  storageClassName: ""
  csi:
    driver: oss.csi.cds.net
    volumeHandle: ${PV_NAME}
    volumeAttributes:
      bucket: ${BUCKET}
      endpoint: ${ENDPOINT}
      path: /
    nodeStageSecretRef:
      name: ${SECRET_NAME}
      namespace: ${NAMESPACE}
    nodePublishSecretRef:
      name: ${SECRET_NAME}
      namespace: ${NAMESPACE}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ${PVC_NAME}
  namespace: ${NAMESPACE}
spec:
  accessModes:
    - ReadWriteMany
  storageClassName: ""
  volumeName: ${PV_NAME}
  resources:
    requests:
      storage: 1Gi
---
apiVersion: v1
kind: Pod
metadata:
  name: ${POD_NAME}
  namespace: ${NAMESPACE}
spec:
  nodeName: ${NODE_NAME}
  restartPolicy: Never
  containers:
    - name: test
      image: busybox:1.36.1
      command:
        - /bin/sh
        - -ec
        - |
          value="geesefs-$(date +%s)"
          printf '%s' "$value" > /data/csi-rw-test
          test "$(cat /data/csi-rw-test)" = "$value"
          rm -f /data/csi-rw-test
      volumeMounts:
        - name: storage
          mountPath: /data
  volumes:
    - name: storage
      persistentVolumeClaim:
        claimName: ${PVC_NAME}
EOF

kubectl -n "${NAMESPACE}" wait --for=jsonpath='{.status.phase}'=Succeeded \
    "pod/${POD_NAME}" --timeout=120s
kubectl -n "${NAMESPACE}" logs "${POD_NAME}"
ansible "${NODE_NAME}" -m shell -a "findmnt -t fuse.geesefs"
