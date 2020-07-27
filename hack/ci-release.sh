#!/usr/bin/env bash

set -o nounset
set -o errexit
set -o pipefail

# ci-release.sh - automated a release of Decco for CI systems. The version to
#                 release is set in the VERSION file.
#
# The script assumes a Linux host with Docker and Make installed. It
# installs all further prerequisites (like Go, goreleaser) and does the
# git tagging. If you want to create a release without all this, you can use
# `make release`.
#
# Expected environment variables:
#
# - DOCKER_PASSWORD       The password for the Docker account to push the images too.
# - DOCKER_USERNAME       The username for the Docker account to push the images too.
# - DRY_RUN               If non-empty, no resulting artifacts will actually be published.
# - GIT_USER_EMAIL        The email of the user creating the git tag.
# - GIT_USER_NAME         The name of the user creating the git tag.
# - GITHUB_TOKEN          A Github access token to publish the release.
# - GO_VERSION            The version of Go to use.
# - GORELEASER_VERSION    The version of goreleaser to use.

# Configure Docker
echo -n "${DOCKER_PASSWORD}" | docker login --password-stdin -u "${DOCKER_USERNAME}"

# Install gimme
mkdir -p ./bin
export PATH="$(pwd)/bin:${PATH}"
curl -sL -o ./bin/gimme https://raw.githubusercontent.com/travis-ci/gimme/master/gimme
chmod +x ./bin/gimme
gimme --version

# Install go
eval "$(GIMME_GO_VERSION=${GO_VERSION} gimme)"
mkdir -p ./build/gopath
export GOPATH="$(pwd)/build/gopath"
go version

# Gimme sets GOROOT. The Makefile redeclares GOROOT based on GO_TOOLCHAIN,
# so we need this roundabout hack
export GO_TOOLCHAIN=${GOROOT}

# Install goreleaser
curl -L -o ./bin/goreleaser.tar.gz https://github.com/goreleaser/goreleaser/releases/download/${GORELEASER_VERSION}/goreleaser_$(uname)_$(uname -m).tar.gz
(cd ./bin && tar -xvf goreleaser.tar.gz)
goreleaser -v
export GORELEASER="goreleaser"

# Determine the VERSION.
if stat VERSION > /dev/null ; then
  export VERSION=$(head -n 1 ./VERSION)
else
  export VERSION="$(grep 'VERSION ?= ' ./Makefile | sed -E 's/^VERSION \?= (.+)$/\1/g')"
fi
echo "VERSION=${VERSION}"

# Make the release
if [ -z "$DRY_RUN" ]
then
  # Tag the current/last commit with the VERSION (required by make release)
  git config user.email "${GIT_USER_EMAIL}"
  git config user.name "${GIT_USER_NAME}"
  git status -b
  git tag -a "${VERSION}" -m "${VERSION}"

  make release
else
  make release-dry-run
fi
