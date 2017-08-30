
SRC_DIR=$(shell pwd)
BUILD_DIR=$(SRC_DIR)/build
BIN_DIR=$(BUILD_DIR)/bin
BUILD_SRC_DIR=$(BUILD_DIR)/src/github.com/platform9
DECCO_SRC_DIR=$(BUILD_SRC_DIR)/decco
OPERATOR_EXE=$(BIN_DIR)/decco-operator

# Override with your own Docker registry tag(s)
OPERATOR_IMAGE_TAG ?= platform9systems/decco-operator
OPERATOR_DEVEL_IMAGE_TAG ?= platform9systems/decco-operator-devel

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

$(OPERATOR_EXE): deps | $(BIN_DIR)
	cd $(SRC_DIR)/cmd/operator && \
	export GOPATH=$(BUILD_DIR) && \
	go build -o $(OPERATOR_EXE)

operator: $(OPERATOR_EXE)

clean:
	rm -rf $(BUILD_DIR)

operator-clean:
	rm -f $(OPERATOR_EXE)

operator-image: $(OPERATOR_EXE)
	docker build --tag $(OPERATOR_IMAGE_TAG) -f support/operator/Dockerfile .
	docker push $(OPERATOR_IMAGE_TAG)

operator-image-devel: $(OPERATOR_EXE)
	docker build --tag $(OPERATOR_DEVEL_IMAGE_TAG) -f support/operator-devel/Dockerfile .
	docker push $(OPERATOR_DEVEL_IMAGE_TAG)
