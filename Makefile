
SHELL := bash
SRC_DIR=$(shell pwd)
BUILD_DIR=$(SRC_DIR)/build
GOPATH_DIR=$(BUILD_DIR)/gopath
GO_TOOLCHAIN=$(BUILD_DIR)/go
GO_DOWNLOAD_URL=https://dl.google.com/go/go1.13.7.linux-amd64.tar.gz
GOSRC=$(GOPATH_DIR)/src
PF9_DIR=$(GOSRC)/github.com/platform9
DECCO_SYMLINKS=$(PF9_DIR)/decco
OPERATOR_STAGE_DIR=$(BUILD_DIR)/operator
DEFAULT_HTTP_STAGE_DIR=$(BUILD_DIR)/default-http
GO_ENV_TARBALL=$(BUILD_DIR)/decco-go-env.tgz
OPERATOR_EXE=$(OPERATOR_STAGE_DIR)/decco-operator
OPERATOR_IMAGE_MARKER=$(OPERATOR_STAGE_DIR)/image-marker
DEFAULT_HTTP_EXE=$(DEFAULT_HTTP_STAGE_DIR)/decco-default-http
SPRINGBOARD_STAGE_DIR=$(BUILD_DIR)/springboard-stunnel
SPRINGBOARD_EXE=$(SPRINGBOARD_STAGE_DIR)/springboard
SPRINGBOARD_IMAGE_NAME := springboard-stunnel
SPRINGBOARD_IMAGE_MARKER=$(SPRINGBOARD_STAGE_DIR)/image-marker

IMAGE_NAME := decco-operator

# Override with your own Docker registry tag(s)
REPO_TAG ?= platform9/$(IMAGE_NAME)
VERSION ?= 1.0.0
BUILD_NUMBER ?= 000
BUILD_ID := $(BUILD_NUMBER)
IMAGE_TAG ?= $(VERSION)-$(BUILD_ID)
FULL_TAG := $(REPO_TAG):$(IMAGE_TAG)
TAG_FILE := $(BUILD_DIR)/container-full-tag

STUNNEL_CONTAINER_TAG ?= platform9/stunnel:5.56-102
SPRINGBOARD_REPO_TAG ?= platform9/$(SPRINGBOARD_IMAGE_NAME)
SPRINGBOARD_FULL_TAG := $(SPRINGBOARD_REPO_TAG):$(IMAGE_TAG)

DEFAULT_HTTP_IMAGE_TAG ?= platform9systems/decco-default-http

export GOPATH:=$(GOPATH_DIR)
export GOROOT:=$(GO_TOOLCHAIN)
export PATH:=$(GO_TOOLCHAIN)/bin:$(PATH)

$(BUILD_DIR):
	mkdir -p $@

$(GO_TOOLCHAIN):| $(BUILD_DIR)
	cd $(BUILD_DIR) && wget -q $(GO_DOWNLOAD_URL) && tar xf go*.tar.gz
	go version

gotoolchain: | $(GO_TOOLCHAIN)

$(GOPATH_DIR):| $(BUILD_DIR) $(GO_TOOLCHAIN)
	mkdir -p $@

$(PF9_DIR):| $(BUILD_DIR)
	mkdir -p $@

$(OPERATOR_STAGE_DIR):
	mkdir -p $@

$(DEFAULT_HTTP_STAGE_DIR):
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

local-default-http:
	cd $(SRC_DIR)/cmd/default-http && go build -o $${GOPATH}/bin/decco-default-http

local-dns-test:
	cd $(SRC_DIR)/cmd/dns-test && go build -o $${GOPATH}/bin/dns-test

$(OPERATOR_EXE): $(GO_TOOLCHAIN) $(SRC_DIR)/cmd/operator/*.go $(SRC_DIR)/pkg/*/*.go | $(OPERATOR_STAGE_DIR)
	cd $(SRC_DIR)/cmd/operator && \
	go build -o $(OPERATOR_EXE)

$(GO_ENV_TARBALL): $(OPERATOR_EXE)
	export TMP_TARBALL=$(shell mktemp --tmpdir gopath.XXX.tgz) && \
	# include symlink targets in tarball, this can fail on some files, ignore errors
	tar cfz $${TMP_TARBALL} -h --ignore-failed-read -C $(GOPATH) . &> /dev/null || true && \
	mv $${TMP_TARBALL} $@

tarball: $(GO_ENV_TARBALL)

$(DEFAULT_HTTP_EXE): | $(DEFAULT_HTTP_STAGE_DIR)
	cd $(SRC_DIR)/cmd/default-http && \
	export GOPATH=$(BUILD_DIR) && \
	go build -o $(DEFAULT_HTTP_EXE)

operator: $(OPERATOR_EXE)

default-http: $(DEFAULT_HTTP_EXE)

clean:
	rm -rf $(BUILD_DIR)

clean-vendor:
	rm -rf $(VENDOR_DIR)

operator-clean:
	rm -f $(OPERATOR_STAGE_DIR)

default-http-clean:
	rm -f $(DEFAULT_HTTP_EXE)

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

default-http-image: $(DEFAULT_HTTP_EXE)
	docker build --tag $(DEFAULT_HTTP_IMAGE_TAG) -f support/default-http/Dockerfile .
	docker push $(DEFAULT_HTTP_IMAGE_TAG)

$(TAG_FILE): operator-push
	echo -n $(FULL_TAG) > $@

container-full-tag: $(TAG_FILE)

clean-tag-file:
	rm -f $(TAG_FILE)
