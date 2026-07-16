FROM golang:1.18-alpine AS build-env
WORKDIR /build
COPY server.go ./
RUN CGO_ENABLED=0 GOARCH="amd64" GOOS="linux" go build -o /oss-server server.go

FROM alpine:3.16
COPY --from=build-env /oss-server /oss-server
COPY oss.service install-oss.sh /
RUN chmod 0755 /install-oss.sh
