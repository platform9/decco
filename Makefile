
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
VERSION ?= $(shell cat ${SRC_DIR}/VERSION)
BUILD_NUMBER ?= 000
BUILD_ID := $(BUILD_NUMBER)
IMAGE_TAG ?= $(VERSION)-$(BUILD_ID)
FULL_TAG := $(REPO_TAG):$(IMAGE_TAG)
TAG_FILE := $(BUILD_DIR)/container-full-tag

STUNNEL_CONTAINER_TAG ?= platform9/stunnel:5.56-102
SPRINGBOARD_REPO_TAG ?= platform9/$(SPRINGBOARD_IMAGE_NAME)
SPRINGBOARD_FULL_TAG := $(SPRINGBOARD_REPO_TAG):$(IMAGE_TAG)

# Binaries
GORELEASER ?= ${SRC_DIR}/hack/goreleaser-docker.sh
CONTROLLER_GEN := "controller-gen"

.PHONY: all
all: operator springboard

.PHONY: release
release: release-clean verify test $(BUILD_DIR) ## Build and release Decco, publishing the artifacts on Github and Dockerhub.
	$(GORELEASER) release --rm-dist

	# Output a file with the published Docker image tag exactly the same as `make container-full-tag`
	echo -n "$(REPO_TAG):$(VERSION)" > $(BUILD_DIR)/container-full-tag

.PHONY: release-dry-run
release-dry-run: release-clean verify test $(BUILD_DIR)
	$(GORELEASER) release --rm-dist --skip-publish --snapshot

.PHONY: release-clean
release-clean:
	rm -rf ${SRC_DIR}/dist

.PHONY: test
test: ## Run all unit tests.
	go test -v ./api/... ./cmd/... ./pkg/... ./test/...

.PHONY: verify
verify: verify-go verify-goreleaser ## Run all static analysis checks.

.PHONY: verify-goreleaser
verify-goreleaser:
	$(GORELEASER) check

.PHONY: verify-go
verify-go:
	# Check if codebase is formatted.
	@bash -c "[ -z $$(gofmt -l ./api ./cmd ./pkg ./test) ] && echo 'OK' || (echo 'ERROR: files are not formatted:' && gofmt -l . && false)"
	# Run static checks on codebase.
	go vet ./api/... ./cmd/... ./pkg/... ./test/...

.PHONY: format
format: ## Run all formatters on the codebase.
	# Format the Go codebase.
	gofmt -s -w api cmd pkg test
	# Format the go.mod file.
	go mod tidy

.PHONY: generate
generate: generate-crds manifests

.PHONY: clean
clean: operator-clean clean-tag-file springboard-clean release-clean
	rm -rf $(BUILD_DIR)

.PHONY: version
version: ## Print the version used in the Makefile
	@echo $(VERSION)
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
	go build -o $@ $(SRC_DIR)/cmd/springboard

# Build manager binary
operator: generate verify
	go build -o bin/operator ./cmd/operator

operator-debug: generate verify
	go build -gcflags="all=-N -l" -o bin/operator ./cmd/operator

springboard-image: $(SPRINGBOARD_IMAGE_MARKER)

springboard-push: $(SPRINGBOARD_IMAGE_MARKER)
	(docker push $(SPRINGBOARD_FULL_TAG) || \
		(echo -n $${DOCKER_PASSWORD} | docker login --password-stdin -u $${DOCKER_USERNAME} && \
		docker push $(SPRINGBOARD_FULL_TAG) && docker logout))
	docker rmi $(SPRINGBOARD_FULL_TAG)
	rm -f $(SPRINGBOARD_IMAGE_MARKER)

springboard-clean:
	rm -rf $(SPRINGBOARD_STAGE_DIR)
	rm -f $(SRC_DIR)/support/stunnel-with-springboard/springboard

springboard:
	go build -o $(SRC_DIR)/support/stunnel-with-springboard/springboard $(SRC_DIR)/cmd/springboard

#
# Decco-operator
#

$(OPERATOR_STAGE_DIR):
	mkdir -p $@

$(OPERATOR_EXE): $(GO_TOOLCHAIN) $(SRC_DIR)/cmd/operator/*.go $(SRC_DIR)/pkg/*/*.go | $(OPERATOR_STAGE_DIR)
	go build -o $(OPERATOR_EXE) $(SRC_DIR)/cmd/operator

operator: $(OPERATOR_EXE)

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

.PHONY: run
run: generate ## Run against the configured Kubernetes cluster in ~/.kube/config
	go run ./cmd/operator


.PHONY: install
install: ## Install CRDs into a cluster
	kubectl apply -f config/crd/bases

.PHONY: deploy
deploy: manifests ## Deploy controller in the configured Kubernetes cluster in ~/.kube/config
	kubectl apply -f config/crd/bases
	kustomize build config/default | kubectl apply -f -

.PHONY: manifests
manifests: controller-gen ## Generate manifests e.g. CRD, RBAC, etc.
	$(CONTROLLER_GEN) crd:trivialVersions=true \
		rbac:roleName=manager-role \
		webhook \
		paths="./api/..." \
		output:crd:artifacts:config=config/crd/bases

		# Work around an upstream bug that generates an invalid PodSpec validation set.
		sed -i'.tmp' -E 's/^                        - protocol/#                        - protocol/g' config/crd/bases/decco.platform9.com_apps.yaml
		rm -f config/crd/bases/decco.platform9.com_apps.yaml.tmp

.PHONY: generate-crds
generate-crds: controller-gen ## generates code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile=./hack/boilerplate.go.txt paths="./..."

# Build the docker image
# TODO(erwin) reconcile commented out kubebuilder docker-build with existing
#docker-build: test
#	docker build . -t ${IMG}
#	@echo "updating kustomize image patch file for manager resource"
#	sed -i'' -e 's@image: .*@image: '"${IMG}"'@' ./config/default/manager_image_patch.yaml

# find or download controller-gen
# download controller-gen if necessary
controller-gen:
ifeq (, $(shell which controller-gen))
        @{ \
        set -e ;\
        CONTROLLER_GEN_TMP_DIR=$$(mktemp -d) ;\
        cd $$CONTROLLER_GEN_TMP_DIR ;\
        go mod init tmp ;\
        go get sigs.k8s.io/controller-tools/cmd/controller-gen@v0.2.4 ;\
        rm -rf $$CONTROLLER_GEN_TMP_DIR ;\
        }
CONTROLLER_GEN=$(GOBIN)/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif