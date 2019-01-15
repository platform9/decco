SHELL := bash
SRC_DIR=$(shell pwd)
BUILD_DIR=$(SRC_DIR)/build
VENDOR_DIR=$(SRC_DIR)/vendor
OPERATOR_STAGE_DIR=$(BUILD_DIR)/operator
DEFAULT_HTTP_STAGE_DIR=$(BUILD_DIR)/default-http
OPERATOR_EXE=$(OPERATOR_STAGE_DIR)/decco-operator
OPERATOR_IMAGE_MARKER=$(OPERATOR_STAGE_DIR)/image-marker
DEFAULT_HTTP_EXE=$(DEFAULT_HTTP_STAGE_DIR)/decco-default-http

# Override with your own Docker registry tag(s)
OPERATOR_IMAGE_TAG ?= platform9/decco-operator:latest
DEFAULT_HTTP_IMAGE_TAG ?= platform9systems/decco-default-http

$(BUILD_DIR):
	mkdir -p $@

$(VENDOR_DIR):
	glide install -v

$(OPERATOR_STAGE_DIR):
	mkdir -p $@

$(DEFAULT_HTTP_STAGE_DIR):
	mkdir -p $@

springboard:
	cd $(SRC_DIR)/cmd/springboard && go build -o $(SRC_DIR)/support/stunnel-with-springboard/springboard

local-default-http:
	cd $(SRC_DIR)/cmd/default-http && go build -o $${GOPATH}/bin/decco-default-http

local-dns-test:
	cd $(SRC_DIR)/cmd/dns-test && go build -o $${GOPATH}/bin/dns-test


$(OPERATOR_EXE): $(SRC_DIR)/cmd/operator/*.go $(SRC_DIR)/pkg/*/*.go | $(VENDOR_DIR) $(OPERATOR_STAGE_DIR)
	cd $(SRC_DIR)/cmd/operator && \
	go build -o $(OPERATOR_EXE)

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
	docker build --tag $(OPERATOR_IMAGE_TAG) $(OPERATOR_STAGE_DIR)
	touch $@

operator-push: $(OPERATOR_IMAGE_MARKER)
	docker push $(OPERATOR_IMAGE_TAG) && \
	docker rmi $(OPERATOR_IMAGE_TAG) && \
	rm -f $(OPERATOR_IMAGE_MARKER)

default-http-image: $(DEFAULT_HTTP_EXE)
	docker build --tag $(DEFAULT_HTTP_IMAGE_TAG) -f support/default-http/Dockerfile .
	docker push $(DEFAULT_HTTP_IMAGE_TAG)
