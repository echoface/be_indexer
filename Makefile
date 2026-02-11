.PHONY: all build test clean proto

GO := go
PROTOC := protoc

all: proto build test

proto:
	@echo "Generating protobuf files..."
	@mkdir -p codegen/cache codegen/indexstore
	# Generating cache.proto (using relative path behavior from codegen.sh)
	$(PROTOC) -I=static/idl/proto --go_out=codegen/cache --go_opt=paths=source_relative static/idl/proto/cache.proto
	# Generating indexstore.proto
	$(PROTOC) -I=static/idl/proto --go_out=codegen/indexstore --go_opt=paths=source_relative static/idl/proto/indexstore.proto

build:
	@echo "Building project (CGO Disabled)..."
	CGO_ENABLED=0 $(GO) build ./...

test:
	@echo "Running tests (CGO Disabled)..."
	CGO_ENABLED=0 $(GO) test -v -cover ./...

dump_test:
	@echo "Running dump/load tests only..."
	CGO_ENABLED=0 $(GO) test -v -run TestBEIndex_DumpLoad ./...

clean:
	@echo "Cleaning..."
	$(GO) clean
