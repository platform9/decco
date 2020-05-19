
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
OPERATOR_EXE=$(OPERATOR_STAGE_DIR)/decco-operator
OPERATOR_IMAGE_NAME := decco-operator
OPERATOR_IMAGE_MARKER=$(OPERATOR_STAGE_DIR)/image-marker
SPRINGBOARD_STAGE_DIR=$(BUILD_DIR)/springboard-stunnel
SPRINGBOARD_EXE=$(SPRINGBOARD_STAGE_DIR)/springboard
SPRINGBOARD_IMAGE_NAME := springboard-stunnel
SPRINGBOARD_IMAGE_MARKER=$(SPRINGBOARD_STAGE_DIR)/image-marker

# Override with your own Docker registry tag(s)
REPO_TAG ?= platform9/$(OPERATOR_IMAGE_NAME)
VERSION ?= 1.0.1
BUILD_NUMBER ?= 000
BUILD_ID := $(BUILD_NUMBER)
IMAGE_TAG ?= $(VERSION)-$(BUILD_ID)
FULL_TAG := $(REPO_TAG):$(IMAGE_TAG)
TAG_FILE := $(BUILD_DIR)/container-full-tag

STUNNEL_CONTAINER_TAG ?= platform9/stunnel:5.56-102
SPRINGBOARD_REPO_TAG ?= platform9/$(SPRINGBOARD_IMAGE_NAME)
SPRINGBOARD_FULL_TAG := $(SPRINGBOARD_REPO_TAG):$(IMAGE_TAG)

GORELEASER ?= ${SRC_DIR}/hack/goreleaser-docker.sh

.PHONY: all
all: operator springboard

.PHONY: release
release: verify test ## Build and release Decco, publishing the artifacts on Github and Dockerhub.
	$(GORELEASER) release --rm-dist

.PHONY: test
test: ## Run all unit tests.
	go test ./...

.PHONY: verify
verify: verify-go verify-goreleaser ## Run all static analysis checks.

.PHONY: verify-goreleaser
verify-goreleaser:
	$(GORELEASER) check

.PHONY: verify-go
verify-go:
	# Check if codebase is formatted.
	@bash -c "[ -z $$(gofmt -l .) ] || (echo 'ERROR: files are not formatted:' && gofmt -l . && false)"
	# Run static checks on codebase.
	go vet ./...

.PHONY: format
format: ## Run all formatters on the codebase.
	# Format the Go codebase.
	gofmt -s -w .
	# Format the go.mod file.
	go mod tidy

.PHONY: generate
generate:
	# nop

#
# Go toolchain
#

$(BUILD_DIR):
	mkdir -p $@

$(GO_TOOLCHAIN):| $(BUILD_DIR)
	cd $(BUILD_DIR) && wget -q $(GO_DOWNLOAD_URL) && tar xf go*.tar.gz
	go version

gotoolchain: | $(GO_TOOLCHAIN)

$(GOPATH_DIR):| $(BUILD_DIR) $(GO_TOOLCHAIN)
	mkdir -p $@

#
# Springboard
#

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

#
# Decco-operator
#

$(OPERATOR_STAGE_DIR):
	mkdir -p $@

$(OPERATOR_EXE): $(GO_TOOLCHAIN) $(SRC_DIR)/cmd/operator/*.go $(SRC_DIR)/pkg/*/*.go | $(OPERATOR_STAGE_DIR)
	cd $(SRC_DIR)/cmd/operator && \
	go build -o $(OPERATOR_EXE)

operator: $(OPERATOR_EXE)

clean: operator-clean clean-tag-file springboard-clean
	rm -rf $(BUILD_DIR)

clean-gopath:
	@echo "GOPATH is $(GOPATH_DIR)"
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
