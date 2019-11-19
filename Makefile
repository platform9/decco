
SHELL := bash
SRC_DIR=$(shell pwd)
BUILD_DIR=$(SRC_DIR)/build
GOPATH_DIR=$(BUILD_DIR)/gopath
CLIENTGO=$(GOPATH_DIR)/src/k8s.io/client-go
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

KLOG := $(GOSRC)/k8s.io/klog

GO_DEPS := \
	$(GOSRC)/k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1 \
	$(GOSRC)/k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset \
	$(GOSRC)/github.com/coreos/etcd-operator/pkg/util/retryutil \
	$(GOSRC)/github.com/aws/aws-sdk-go \
	$(GOSRC)/github.com/golang/glog \
	$(GOSRC)/github.com/pborman/uuid \
	$(GOSRC)/github.com/cenkalti/backoff \
	$(GOSRC)/github.com/sirupsen/logrus \
	$(GOSRC)/k8s.io/federation/pkg/dnsprovider

IMAGE_NAME := decco-operator

# Override with your own Docker registry tag(s)
REPO_TAG ?= platform9/$(IMAGE_NAME)
VERSION ?= 1.0.0
BUILD_NUMBER ?= 000
BUILD_ID := $(BUILD_NUMBER)
IMAGE_TAG ?= $(VERSION)-$(BUILD_ID)
FULL_TAG := $(REPO_TAG):$(IMAGE_TAG)
TAG_FILE := $(BUILD_DIR)/container-full-tag

SPRINGBOARD_REPO_TAG ?= platform9/$(SPRINGBOARD_IMAGE_NAME)
SPRINGBOARD_FULL_TAG := $(SPRINGBOARD_REPO_TAG):$(IMAGE_TAG)

DEFAULT_HTTP_IMAGE_TAG ?= platform9systems/decco-default-http

export GOPATH:=$(GOPATH_DIR)

$(BUILD_DIR):
	mkdir -p $@

$(GOPATH_DIR):| $(BUILD_DIR)
	mkdir -p $@

$(PF9_DIR):| $(BUILD_DIR)
	mkdir -p $@

$(GODEP):
	go get github.com/tools/godep

$(DECCO_SYMLINKS):| $(PF9_DIR)
	for x in `find pkg -type f` ; do d=`dirname $$x`; f=`basename $$x`; \
	mkdir -p $@/$$d; ln -s $(SRC_DIR)/$$x $@/$$x ; done

$(KLOG): | $(GOPATH_DIR)
	go get k8s.io/klog
	cd $@ && git checkout v0.4.0

$(CLIENTGO): | $(GODEP) $(GOPATH_DIR) $(KLOG)
	go get k8s.io/client-go/...

symlinks: | $(DECCO_SYMLINKS)

clientgo: | $(CLIENTGO)

$(GO_DEPS): $(GOPATH_DIR)
	go get $(subst $(GOSRC)/,,$@)

godeps: | $(GO_DEPS)

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
	docker build --tag $(SPRINGBOARD_FULL_TAG) $(SPRINGBOARD_STAGE_DIR)
	touch $@

springboard-image: $(SPRINGBOARD_IMAGE_MARKER)

springboard-push: $(SPRINGBOARD_IMAGE_MARKER)
	(docker push $(SPRINGBOARD_FULL_TAG) || \
		(echo -n $${DOCKER_PASSWORD} | docker login --password-stdin -u $${DOCKER_USERNAME} && \
		docker push $(SPRINGBOARD_FULL_TAG) && docker logout))
	docker rmi $(SPRINGBOARD_FULL_TAG)
	rm -f $(SPRINGBOARD_IMAGE_MARKER)

springboard:
	cd $(SRC_DIR)/cmd/springboard && go build -o $(SRC_DIR)/support/stunnel-with-springboard/springboard

local-default-http:
	cd $(SRC_DIR)/cmd/default-http && go build -o $${GOPATH}/bin/decco-default-http

local-dns-test:
	cd $(SRC_DIR)/cmd/dns-test && go build -o $${GOPATH}/bin/dns-test

$(OPERATOR_EXE): $(SRC_DIR)/cmd/operator/*.go $(SRC_DIR)/pkg/*/*.go | $(CLIENTGO) $(OPERATOR_STAGE_DIR) $(GO_DEPS) $(DECCO_SYMLINKS)
	cd $(SRC_DIR)/cmd/operator && \
	go build -o $(OPERATOR_EXE)

$(GO_ENV_TARBALL): $(OPERATOR_EXE)
	export TMP_TARBALL=$(shell mktemp --tmpdir gopath.XXX.tgz) && \
	# include symlink targets in tarball, this can fail on some files, ignore errors \
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
