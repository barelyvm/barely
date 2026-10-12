# Builder for the guest image. Native arm64 Alpine: apk --root
# stages the guest's root filesystem, and mkfs.erofs/cpio package it.
FROM alpine:3.20

RUN apk add --no-cache erofs-utils cpio gzip e2fsprogs-extra gcc musl-dev
