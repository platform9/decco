#!/usr/bin/env bash

# goreleaser-docker.sh - run goreleaser within a Docker container.

set -o nounset
set -o errexit
set -o pipefail

GORELEASER_IMAGE=${GORELEASER_IMAGE:=goreleaser/goreleaser}
GORELEASER_VERSION=${GORELEASER_VERSION:=v1.24.0}

docker run --rm --privileged \
  -v "$PWD:$PWD" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -w "$PWD" \
  -e GITHUB_TOKEN \
  -e DOCKER_USERNAME \
  -e DOCKER_PASSWORD \
  -e DOCKER_REGISTRY \
  ${GORELEASER_IMAGE}:${GORELEASER_VERSION} "$@"