.PHONY: build install test clean fmt vet lint vuln help

# Build variables
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS = -X github.com/noderings/cli/internal/cli.version=$(VERSION) \
          -X github.com/noderings/cli/internal/cli.commit=$(COMMIT) \
          -X github.com/noderings/cli/internal/cli.buildDate=$(BUILD_DATE)

# Go parameters
GOCMD = go
GOBUILD = $(GOCMD) build
GOTEST = $(GOCMD) test
GOMOD = $(GOCMD) mod
GOFMT = $(GOCMD) fmt
GOVET = $(GOCMD) vet

# Binary name
BINARY_NAME = nr
BINARY_UNIX = $(BINARY_NAME)_unix
BINARY_DARWIN = $(BINARY_NAME)_darwin
BINARY_WINDOWS = $(BINARY_NAME).exe

# Directories
CMD_DIR = cmd/nr
BUILD_DIR = ./build

help: ## Show this help message
	@echo 'Usage:'
	@echo '  make [target]'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the binary
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) ./$(CMD_DIR)

build-linux: build-linux-amd64 ## Build for Linux (AMD64, alias for build-linux-amd64)

build-linux-amd64: ## Build for Linux AMD64
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)_linux_amd64 ./$(CMD_DIR)

build-linux-arm64: ## Build for Linux ARM64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)_linux_arm64 ./$(CMD_DIR)

build-darwin: build-darwin-amd64 build-darwin-arm64 ## Build for macOS (AMD64 and ARM64)

build-darwin-amd64: ## Build for macOS AMD64
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)_darwin_amd64 ./$(CMD_DIR)

build-darwin-arm64: ## Build for macOS ARM64
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)_darwin_arm64 ./$(CMD_DIR)

build-windows: ## Build for Windows AMD64
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_WINDOWS) ./$(CMD_DIR)

build-all: clean build-linux-amd64 build-linux-arm64 build-darwin-amd64 build-darwin-arm64 build-windows ## Build for all platforms

install: build ## Install the binary
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(GOPATH)/bin/$(BINARY_NAME) ./$(CMD_DIR)

test: ## Run tests
	$(GOTEST) -race -count=1 ./...

test-coverage: ## Run tests with coverage
	@mkdir -p $(BUILD_DIR)
	$(GOTEST) -coverprofile=$(BUILD_DIR)/coverage.out -covermode=atomic $$(go list ./... | grep -v '/internal/api/generated')
	$(GOCMD) tool cover -html=$(BUILD_DIR)/coverage.out -o $(BUILD_DIR)/coverage.html

fmt: ## Format code
	$(GOFMT) ./...

vet: ## Run go vet
	$(GOVET) ./...

lint: ## Run golangci-lint
	@which golangci-lint > /dev/null || (echo "golangci-lint not installed. Install: https://golangci-lint.run/welcome/install/" && exit 1)
	golangci-lint run --timeout=5m

vuln: ## Run govulncheck
	@which govulncheck > /dev/null || go install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

clean: ## Clean build artifacts
	rm -f $(BUILD_DIR)/$(BINARY_NAME) \
		$(BUILD_DIR)/$(BINARY_NAME)_linux_amd64 $(BUILD_DIR)/$(BINARY_NAME)_linux_arm64 \
		$(BUILD_DIR)/$(BINARY_NAME)_darwin_amd64 $(BUILD_DIR)/$(BINARY_NAME)_darwin_arm64 \
		$(BUILD_DIR)/$(BINARY_WINDOWS)
	rm -f $(BUILD_DIR)/coverage.out $(BUILD_DIR)/coverage.html

deps: ## Download dependencies
	$(GOMOD) download
	$(GOMOD) tidy

tidy: ## Tidy go.mod
	$(GOMOD) tidy

# oapi-codegen variables (maintainers: set SWAGGER_SPEC to a Swagger 2.0 JSON file)
SWAGGER_SPEC ?= proto.swagger.json
OPENAPI3_SPEC = internal/api/generated/openapi3.yaml
GENERATED_DIR = internal/api/generated
OAPI_CODEGEN = oapi-codegen

generate-api-client: ## Generate API client from OpenAPI spec using oapi-codegen
	@echo "Generating API client from OpenAPI spec with oapi-codegen..."
	@if [ ! -f $(SWAGGER_SPEC) ]; then \
		echo "Error: OpenAPI spec not found at $(SWAGGER_SPEC)"; \
		exit 1; \
	fi
	@which $(OAPI_CODEGEN) > /dev/null || (echo "oapi-codegen not installed. Install via: go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest" && exit 1)
	@mkdir -p $(GENERATED_DIR)
	@echo "Converting Swagger 2.0 to OpenAPI 3.0..."
	@npx -y swagger2openapi $(SWAGGER_SPEC) -o $(OPENAPI3_SPEC) || (echo "Error converting Swagger 2.0 to OpenAPI 3.0. Install swagger2openapi: npm install -g swagger2openapi" && exit 1)
	@echo "Generating types..."
	@$(OAPI_CODEGEN) \
		-generate types \
		-package api \
		-o $(GENERATED_DIR)/types.gen.go \
		$(OPENAPI3_SPEC)
	@echo "Generating client..."
	@$(OAPI_CODEGEN) \
		-generate client \
		-package api \
		-o $(GENERATED_DIR)/client.gen.go \
		$(OPENAPI3_SPEC)
	@echo "✓ API client generated successfully in $(GENERATED_DIR)"
	@echo "Run 'make tidy' to update go.mod with new dependencies"

# https://groups.google.com/g/golang-nuts/c/FrWNhWsLDVY/m/CVd_iRedBwAJ
update-direct-deps:
	@go list -f '{{if not (or .Main .Indirect)}}{{.Path}}{{end}}' -m all | xargs -n1 go get
	@go mod tidy
