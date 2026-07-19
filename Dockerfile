FROM golang:1.18-alpine AS build-env
RUN apk update && apk add git make
#COPY cck-sdk-go /go/src/github.com/capitalonline/cck-sdk-go
#COPY cds-csi-driver /go/src/github.com/capitalonline/cds-csi-driver

COPY . /go/src/github.com/capitalonline/cds-csi-driver
RUN go env -w GO111MODULE=on && go env -w GOPROXY=https://goproxy.cn,direct
RUN cd /go/src/github.com/capitalonline/cds-csi-driver && go mod tidy && make container-binary


FROM alpine:3.6

RUN apk --no-cache update && apk --no-cache add --virtual build-dependencies \
    build-base alpine-sdk \
    fuse fuse-dev \
    automake autoconf git \
    libressl-dev \
    curl-dev libxml2-dev \
    ca-certificates \
    udev e2fsprogs xfsprogs nvme-cli nfs-utils


COPY --from=build-env /cds-csi-driver /cds-csi-driver

ENTRYPOINT ["/cds-csi-driver"]
