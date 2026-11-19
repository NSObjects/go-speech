# Prebuilt tools for ASR integration and audio codec CI.
# Go itself is provided by actions/setup-go per job, so this image stays
# independent of the Go toolchain version in go.mod.
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      bash ca-certificates curl git tar gzip unzip zstd \
      python3 build-essential pkg-config \
      ffmpeg espeak-ng libopus-dev libopencore-amrnb-dev; \
    rm -rf /var/lib/apt/lists/*

WORKDIR /workspace
