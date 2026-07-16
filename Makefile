PKG=github.com/capitalonline/cds-csi-driver
HARBOR_REPOSITORY?=harbor-dev.yun-paas.com/csi_agent
IMAGE?=$(HARBOR_REPOSITORY)/cds-csi-driver
OSS_SERVER_IMAGE?=$(HARBOR_REPOSITORY)/oss-server
VERSION?=v2.1.5
OSS_SERVER_VERSION?=v1.0.3
GIT_COMMIT?=$(shell git rev-parse HEAD)
BUILD_DATE?=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS?="-X ${PKG}/pkg/common.version=${VERSION} -X ${PKG}/pkg/common.gitCommit=${GIT_COMMIT} -X ${PKG}/pkg/common.buildDate=${BUILD_DATE} -s -w"
NAS_DEPLOY_PATH=./deploy/nas
NAS_KUSTOMIZATION_RELEASE_PATH=${NAS_DEPLOY_PATH}/overlays/release
NAS_KUSTOMIZATION_TEST_PATH=${NAS_DEPLOY_PATH}/overlays/test
OSS_DEPLOY_PATH=./deploy/oss
OSS_KUSTOMIZATION_RELEASE_PATH=${OSS_DEPLOY_PATH}/overlays/release
OSS_KUSTOMIZATION_TEST_PATH=${OSS_DEPLOY_PATH}/overlays/test
DISK_DEPLOY_PATH=./deploy/disk
DISK_KUSTOMIZATION_RELEASE_PATH=${DISK_DEPLOY_PATH}/overlays/release
DISK_KUSTOMIZATION_TEST_PATH=${DISK_DEPLOY_PATH}/overlays/test
EBS_DISK_DEPLOY_PATH=./deploy/ebs_disk
EBS_DISK_KUSTOMIZATION_RELEASE_PATH=${EBS_DISK_DEPLOY_PATH}/overlays/release
EBS_DISK_KUSTOMIZATION_TEST_PATH=${EBS_DISK_DEPLOY_PATH}/overlays/test
EBS_DISK_KUSTOMIZATION_FILE=${EBS_DISK_KUSTOMIZATION_RELEASE_PATH}/kustomization.yaml
.EXPORT_ALL_VARIABLES:

.PHONY: build
build:
	mkdir -p bin
	CGO_ENABLED=0 go build -ldflags ${LDFLAGS} -o bin/cds-csi-driver ./cmd/

.PHONY: container-binary
container-binary:
	CGO_ENABLED=0 GOARCH="amd64" GOOS="linux" go build -ldflags ${LDFLAGS} -o /cds-csi-driver ./cmd/

.PHONY: image-release
image-release:
	docker build -t $(IMAGE):$(VERSION) .

.PHONY: image
image:
	docker build -t $(IMAGE):latest .

.PHONY: oss-server-image
oss-server-image:
	docker build -f dist/Dockerfile -t $(OSS_SERVER_IMAGE):$(OSS_SERVER_VERSION) dist

.PHONY: release
release: image-release oss-server-image
	docker push $(IMAGE):$(VERSION)
	docker push $(OSS_SERVER_IMAGE):$(OSS_SERVER_VERSION)

.PHONY: kustomize
kustomize:
	kubectl kustomize ${NAS_KUSTOMIZATION_RELEASE_PATH} > ${NAS_DEPLOY_PATH}/deploy.yaml
	kubectl kustomize ${OSS_KUSTOMIZATION_RELEASE_PATH} > ${OSS_DEPLOY_PATH}/deploy.yaml
	kubectl kustomize ${DISK_KUSTOMIZATION_RELEASE_PATH} > ${DISK_DEPLOY_PATH}/deploy.yaml

.PHONY: unit-test
unit-test:
	@echo "**************************** running unit test ****************************"
	go test -v -race ./pkg/...

.PHONY: test-prerequisite
test-prerequisite:
	docker build -t $(IMAGE):test . && docker push $(IMAGE):test
	kubectl kustomize ${NAS_KUSTOMIZATION_TEST_PATH} | kubectl apply -f -
	kubectl kustomize ${OSS_KUSTOMIZATION_TEST_PATH} | kubectl apply -f -
	kubectl kustomize ${DISK_KUSTOMIZATION_TEST_PATH} | kubectl apply -f -

.PHONY: integration-test
integration-test:
	@echo "**************************** running integration test ****************************"
	@./test.sh

.PHONE: test
test: unit-test integration-test
	@echo "**************************** all tests passed ****************************"

.PHONE: oss-test
oss-test:
	@echo "**************************** running oss unit test ****************************"
	go test -v -race ./pkg/driver/oss/...
	@echo "**************************** running oss integration test ****************************"
	@./test/oss/test.sh
	@echo "**************************** all tests passed ****************************"
