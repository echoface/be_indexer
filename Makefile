.PHONY: all build test clean

GO := go

all: build test

build:
	@echo "Building project (CGO Disabled)..."
	CGO_ENABLED=0 $(GO) build ./...

test:
	@echo "Running tests (CGO Disabled)..."
	CGO_ENABLED=0 $(GO) test -v -cover ./...

clean:
	@echo "Cleaning..."
	$(GO) clean
