
SRC_DIR=$(shell pwd)
BUILD_DIR=$(SRC_DIR)/build
BIN_DIR=$(BUILD_DIR)/bin
BUILD_SRC_DIR=$(BUILD_DIR)/src/github.com/platform9
DECCO_SRC_DIR=$(BUILD_SRC_DIR)/decco
OPERATOR_EXE=$(BIN_DIR)/decco-operator
DEFAULT_HTTP_EXE=$(BIN_DIR)/decco-default-http

# Override with your own Docker registry tag(s)
OPERATOR_IMAGE_TAG ?= platform9systems/decco-operator
OPERATOR_DEVEL_IMAGE_TAG ?= platform9systems/decco-operator-devel
DEFAULT_HTTP_IMAGE_TAG ?= platform9systems/decco-default-http
DEFAULT_HTTP_DEVEL_IMAGE_TAG ?= platform9systems/decco-default-http-devel

$(BUILD_SRC_DIR):
	mkdir -p $@

$(DECCO_SRC_DIR): | $(BUILD_SRC_DIR)
	mkdir -p $@
	cp -a $(SRC_DIR)/{cmd,pkg} $@/

$(BIN_DIR):
	mkdir -p $@

deps: $(DECCO_SRC_DIR)
	cd $(SRC_DIR)/cmd/operator && \
	export GOPATH=$(BUILD_DIR) && \
	go get -d

local-deps:
	cd $(SRC_DIR)/cmd/operator && go get -d

local-operator:
	cd $(SRC_DIR)/cmd/operator && go build -o $${GOPATH}/bin/decco-operator

local-operator-dbg:
	cd $(SRC_DIR)/cmd/operator && go build -gcflags='-N -l' -o $${GOPATH}/bin/decco-operator-dbg

springboard:
	cd $(SRC_DIR)/cmd/springboard && go build -o $(SRC_DIR)/support/stunnel-with-springboard/springboard


local-default-http:
	cd $(SRC_DIR)/cmd/default-http && go build -o $${GOPATH}/bin/decco-default-http

local-dns-test:
	cd $(SRC_DIR)/cmd/dns-test && go build -o $${GOPATH}/bin/dns-test


$(OPERATOR_EXE): deps | $(BIN_DIR)
	cd $(SRC_DIR)/cmd/operator && \
	export GOPATH=$(BUILD_DIR) && \
	go build -o $(OPERATOR_EXE)

$(DEFAULT_HTTP_EXE): | $(BIN_DIR)
	cd $(SRC_DIR)/cmd/default-http && \
	export GOPATH=$(BUILD_DIR) && \
	go build -o $(DEFAULT_HTTP_EXE)

operator: $(OPERATOR_EXE)

default-http: $(DEFAULT_HTTP_EXE)

clean:
	rm -rf $(BUILD_DIR)

operator-clean:
	rm -f $(OPERATOR_EXE)

default-http-clean:
	rm -f $(DEFAULT_HTTP_EXE)

operator-image: $(OPERATOR_EXE)
	docker build --tag $(OPERATOR_IMAGE_TAG) -f support/operator/Dockerfile .
	docker push $(OPERATOR_IMAGE_TAG)

operator-image-devel: $(OPERATOR_EXE)
	docker build --tag $(OPERATOR_DEVEL_IMAGE_TAG) -f support/operator-devel/Dockerfile .
	docker push $(OPERATOR_DEVEL_IMAGE_TAG)

default-http-image: $(DEFAULT_HTTP_EXE)
	docker build --tag $(DEFAULT_HTTP_IMAGE_TAG) -f support/default-http/Dockerfile .
	docker push $(DEFAULT_HTTP_IMAGE_TAG)
