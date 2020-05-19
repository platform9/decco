
SHELL := bash
SRC_DIR=$(shell pwd)
BUILD_DIR=$(SRC_DIR)/build

GO_TOOLCHAIN ?= $(BUILD_DIR)/go
GO_VERSION := 1.13.7
GO_DOWNLOAD_URL=https://dl.google.com/go/go$(GO_VERSION).$(shell uname)-amd64.tar.gz

ifneq ($(GO_TOOLCHAIN),"")
	export GOROOT:=$(GO_TOOLCHAIN)
	export PATH:=$(GO_TOOLCHAIN)/bin:$(PATH)
endif

OPERATOR_STAGE_DIR=$(BUILD_DIR)/operator
GO_ENV_TARBALL=$(BUILD_DIR)/decco-go-env.tgz
OPERATOR_EXE=$(OPERATOR_STAGE_DIR)/decco-operator
OPERATOR_IMAGE_MARKER=$(OPERATOR_STAGE_DIR)/image-marker
SPRINGBOARD_STAGE_DIR=$(BUILD_DIR)/springboard-stunnel
SPRINGBOARD_EXE=$(SPRINGBOARD_STAGE_DIR)/springboard
SPRINGBOARD_IMAGE_NAME := springboard-stunnel
SPRINGBOARD_IMAGE_MARKER=$(SPRINGBOARD_STAGE_DIR)/image-marker

IMAGE_NAME := decco-operator

# Override with your own Docker registry tag(s)
REPO_TAG ?= platform9/$(IMAGE_NAME)
VERSION ?= 1.0.1
BUILD_NUMBER ?= 000
BUILD_ID := $(BUILD_NUMBER)
IMAGE_TAG ?= $(VERSION)-$(BUILD_ID)
FULL_TAG := $(REPO_TAG):$(IMAGE_TAG)
TAG_FILE := $(BUILD_DIR)/container-full-tag

STUNNEL_CONTAINER_TAG ?= platform9/stunnel:5.56-102
SPRINGBOARD_REPO_TAG ?= platform9/$(SPRINGBOARD_IMAGE_NAME)
SPRINGBOARD_FULL_TAG := $(SPRINGBOARD_REPO_TAG):$(IMAGE_TAG)

all: operator springboard

$(BUILD_DIR):
	mkdir -p $@

$(GO_TOOLCHAIN):| $(BUILD_DIR)
	cd $(BUILD_DIR) && wget -q $(GO_DOWNLOAD_URL) && tar xf go*.tar.gz
	go version

gotoolchain: | $(GO_TOOLCHAIN)

$(GOPATH_DIR):| $(BUILD_DIR) $(GO_TOOLCHAIN)
	mkdir -p $@

$(OPERATOR_STAGE_DIR):
	mkdir -p $@

$(SPRINGBOARD_STAGE_DIR):
	mkdir -p $@

$(SPRINGBOARD_EXE): | $(SPRINGBOARD_STAGE_DIR)
	cd $(SRC_DIR)/cmd/springboard && go build -o $@

$(SPRINGBOARD_IMAGE_MARKER): $(SPRINGBOARD_EXE)
	cp -f $(SRC_DIR)/support/stunnel-instrumented-with-springboard/* $(SPRINGBOARD_STAGE_DIR)
	sed -i 's|__STUNNEL_CONTAINER_TAG__|$(STUNNEL_CONTAINER_TAG)|g' $(SPRINGBOARD_STAGE_DIR)/Dockerfile
	docker build --tag $(SPRINGBOARD_FULL_TAG) $(SPRINGBOARD_STAGE_DIR)
	touch $@

springboard-image: $(SPRINGBOARD_IMAGE_MARKER)

springboard-push: $(SPRINGBOARD_IMAGE_MARKER)
	(docker push $(SPRINGBOARD_FULL_TAG) || \
		(echo -n $${DOCKER_PASSWORD} | docker login --password-stdin -u $${DOCKER_USERNAME} && \
		docker push $(SPRINGBOARD_FULL_TAG) && docker logout))
	docker rmi $(SPRINGBOARD_FULL_TAG)
	rm -f $(SPRINGBOARD_IMAGE_MARKER)

springboard-clean:
	rm -rf $(SPRINGBOARD_STAGE_DIR)

springboard:
	cd $(SRC_DIR)/cmd/springboard && go build -o $(SRC_DIR)/support/stunnel-with-springboard/springboard

$(OPERATOR_EXE): $(GO_TOOLCHAIN) $(SRC_DIR)/cmd/operator/*.go $(SRC_DIR)/pkg/*/*.go | $(OPERATOR_STAGE_DIR)
	cd $(SRC_DIR)/cmd/operator && \
	go build -o $(OPERATOR_EXE)

$(GO_ENV_TARBALL): $(OPERATOR_EXE)
	export TMP_TARBALL=$(shell mktemp --tmpdir gopath.XXX.tgz) && \
	# include symlink targets in tarball, this can fail on some files, ignore errors
	tar cfz $${TMP_TARBALL} -h --ignore-failed-read -C $(GOPATH) . &> /dev/null || true && \
	mv $${TMP_TARBALL} $@

tarball: $(GO_ENV_TARBALL)

operator: $(OPERATOR_EXE)

clean: clean-gopath
	rm -rf $(BUILD_DIR)

clean-gopath:
	echo GOPATH is $(GOPATH_DIR)
	go clean -modcache

operator-clean:
	rm -rf $(OPERATOR_STAGE_DIR)

operator-image: $(OPERATOR_IMAGE_MARKER)

$(OPERATOR_IMAGE_MARKER): $(OPERATOR_EXE)
	cp -f support/operator/Dockerfile $(OPERATOR_STAGE_DIR)
	docker build --tag $(FULL_TAG) $(OPERATOR_STAGE_DIR)
	touch $@

operator-push: $(OPERATOR_IMAGE_MARKER)
	(docker push $(FULL_TAG) || \
		(echo -n $${DOCKER_PASSWORD} | docker login --password-stdin -u $${DOCKER_USERNAME} && \
		docker push $(FULL_TAG) && docker logout))
	docker rmi $(FULL_TAG)
	rm -f $(OPERATOR_IMAGE_MARKER)

$(TAG_FILE): operator-push
	echo -n $(FULL_TAG) > $@

container-full-tag: $(TAG_FILE)

clean-tag-file:
	rm -f $(TAG_FILE)

generate:
	# nop

