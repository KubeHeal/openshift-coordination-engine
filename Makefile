# Makefile for OpenShift Coordination Engine (Go)

# Variables
BINARY_NAME=coordination-engine
REGISTRY?=quay.io/tosin2013
IMAGE_NAME=$(REGISTRY)/openshift-coordination-engine
GOOS?=linux
GOARCH?=amd64

# Extract version from branch name
BRANCH_NAME ?= $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
ifeq ($(BRANCH_NAME),$(filter $(BRANCH_NAME),release-4.18 release-4.19 release-4.20))
    OCP_VERSION := $(shell echo $(BRANCH_NAME) | sed 's/release-//')
    IMAGE_TAG := ocp-$(OCP_VERSION)-$(shell git rev-parse --short=7 HEAD 2>/dev/null || echo "latest")
    VERSION := $(IMAGE_TAG)
else
    OCP_VERSION := dev
    IMAGE_TAG := dev-$(shell git rev-parse --short=7 HEAD 2>/dev/null || echo "latest")
    VERSION := latest
endif

# Build variables
BUILD_DIR=bin
CMD_DIR=cmd/coordination-engine
MAIN_GO=$(CMD_DIR)/main.go

# Test variables
COVERAGE_DIR=coverage
COVERAGE_FILE=$(COVERAGE_DIR)/coverage.out
COVERAGE_HTML=$(COVERAGE_DIR)/coverage.html

# Linting
GOLANGCI_LINT_VERSION=v1.55.2

.PHONY: all build test clean docker-build docker-push lint coverage help show-version

## help: Display this help message
help:
	@echo "OpenShift Coordination Engine - Makefile targets:"
	@echo ""
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'

## show-version: Display current version information
show-version:
	@echo "Branch: $(BRANCH_NAME)"
	@echo "OpenShift Version: $(OCP_VERSION)"
	@echo "Image Tag: $(IMAGE_TAG)"
	@echo "Full Image: $(IMAGE_NAME):$(IMAGE_TAG)"
	@echo "VERSION: $(VERSION)"

## all: Build the binary
all: build

## build: Build the coordination engine binary
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-ldflags="-w -s -X main.Version=$(VERSION)" \
		-o $(BUILD_DIR)/$(BINARY_NAME) \
		$(MAIN_GO)
	@echo "Binary built: $(BUILD_DIR)/$(BINARY_NAME)"

## run: Run the coordination engine locally
run: build
	@echo "Running $(BINARY_NAME)..."
	@$(BUILD_DIR)/$(BINARY_NAME)

## test: Run unit tests
test:
	@echo "Running unit tests..."
	@go test -v -race -timeout 5m ./...

## test-integration: Run integration tests
test-integration:
	@echo "Running integration tests..."
	INTEGRATION_TEST=true go test -v -tags=integration -timeout 10m ./test/integration/...

## test-e2e: Run end-to-end tests
test-e2e:
	@echo "Running e2e tests..."
	@go test -v -tags=e2e -timeout 20m ./test/e2e/...

## coverage: Generate test coverage report
coverage:
	@echo "Generating coverage report..."
	@mkdir -p $(COVERAGE_DIR)
	@go test -v -race -coverprofile=$(COVERAGE_FILE) -covermode=atomic ./...
	@go tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "Coverage report: $(COVERAGE_HTML)"

## lint: Run linters
lint:
	@echo "Running linters..."
	@which golangci-lint > /dev/null || \
		(echo "Installing golangci-lint..." && \
		 go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION))
	@golangci-lint run --timeout 5m

## fmt: Format Go code
fmt:
	@echo "Formatting code..."
	@go fmt ./...
	@goimports -w .

## vet: Run go vet
vet:
	@echo "Running go vet..."
	@go vet ./...

## mod-tidy: Tidy Go modules
mod-tidy:
	@echo "Tidying Go modules..."
	@go mod tidy

## mod-verify: Verify Go modules
mod-verify:
	@echo "Verifying Go modules..."
	@go mod verify

## docker-build: Build Docker image
docker-build:
	@echo "Building Docker image: $(IMAGE_NAME):$(VERSION)"
	@docker build -t $(IMAGE_NAME):$(VERSION) .
	@docker tag $(IMAGE_NAME):$(VERSION) $(IMAGE_NAME):latest
	@echo "Docker image built: $(IMAGE_NAME):$(VERSION)"

## docker-push: Push Docker image to registry
docker-push: docker-build
	@echo "Pushing Docker image: $(IMAGE_NAME):$(VERSION)"
	@docker push $(IMAGE_NAME):$(VERSION)
	@docker push $(IMAGE_NAME):latest
	@echo "Docker image pushed: $(IMAGE_NAME):$(VERSION)"

## docker-run: Run Docker container locally
docker-run:
	@echo "Running Docker container..."
	@docker run --rm -it \
		-e KUBECONFIG=/root/.kube/config \
		-e ML_SERVICE_URL=http://host.docker.internal:8080 \
		-v $(HOME)/.kube:/root/.kube:ro \
		$(IMAGE_NAME):$(VERSION)

## helm-lint: Lint Helm chart
helm-lint:
	@echo "Linting Helm chart..."
	@helm lint charts/coordination-engine

## helm-template: Render Helm chart templates
helm-template:
	@echo "Rendering Helm chart templates..."
	@helm template coordination-engine charts/coordination-engine

## clean: Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR) $(COVERAGE_DIR)
	@go clean -cache -testcache
	@echo "Clean complete"

## install-tools: Install development tools
install-tools:
	@echo "Installing development tools..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@go install golang.org/x/tools/cmd/goimports@latest
	@go install github.com/onsi/ginkgo/v2/ginkgo@latest
	@echo "Development tools installed"

## ci: Run CI checks (lint, test, build)
ci: lint test build
	@echo "CI checks passed"

.DEFAULT_GOAL := help

