
SRC_DIR=$(shell pwd)
BUILD_DIR=$(SRC_DIR)/build
BIN_DIR=$(BUILD_DIR)/bin
OPERATOR_EXE=$(BIN_DIR)/decco-operator

# Override with your own Docker registry tag(s)
OPERATOR_IMAGE_TAG ?= platform9systems/decco-operator
OPERATOR_DEVEL_IMAGE_TAG ?= platform9systems/decco-operator-devel

$(BIN_DIR):
	mkdir -p $@

deps:
	cd $(SRC_DIR)/cmd/operator && \
	export GOPATH=$(BUILD_DIR) && \
	go get -d

controller-deps:
	cd $(SRC_DIR)/cmd/operator/pkg/controller && \
	export GOPATH=$(BUILD_DIR) && \
	go get -d

$(OPERATOR_EXE): | $(BIN_DIR)
	cd $(SRC_DIR)/cmd/operator && \
	export GOPATH=$(BUILD_DIR) && \
	go get -d && go build -o $(OPERATOR_EXE)

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
