# FLR — Federated Lambda Registry
# Makefile for building, testing, and deploying

.PHONY: all build flrd flr-seed test proto docker deploy-local clean lint fmt vet golden-vectors

BINARY_NAME := flr
BUILD_DIR := ./bin
DOCKER_IMAGE := otap/flr
DOCKER_TAG := latest

# Default target
all: build

# Build all binaries
build: flrd flr-seed
	@echo "All FLR binaries built in $(BUILD_DIR)/"

# Build the FLR daemon (the server)
flrd:
	@echo "Building flrd..."
	@mkdir -p $(BUILD_DIR)
	go build -ldflags "-s -w" -o $(BUILD_DIR)/flrd ./cmd/flrd

# Build the schema-seeder CLI
flr-seed:
	@echo "Building flr-seed..."
	@mkdir -p $(BUILD_DIR)
	go build -ldflags "-s -w" -o $(BUILD_DIR)/flr-seed ./cmd/flr-seed

# Generate / verify cross-language golden vectors
# Use -emit-golden the first time and after schema layout changes.
golden-vectors:
	@echo "Verifying golden vectors..."
	go test ./internal/crypto -run TestEmitGoldenVectors -count=1

emit-golden-vectors:
	@echo "Emitting golden vectors..."
	go test ./internal/crypto -run TestEmitGoldenVectors -emit-golden -count=1

# Run all tests
test:
	@echo "Running tests..."
	go test -v -race -coverprofile=coverage.out ./internal/... ./pkg/... ./test/...
	@echo "Test coverage:"
	@go tool cover -func=coverage.out | tail -1

# Run unit tests only
unit-test:
	@echo "Running unit tests..."
	go test -v -race ./internal/crypto/... ./internal/registry/... ./internal/federation/... ./internal/xlat/... ./internal/api/... ./internal/audit/...

# Run integration tests
integration-test:
	@echo "Running integration tests..."
	go test -v -race ./test/integration/...

# Generate protobuf code
proto:
	@echo "Generating protobuf code..."
	@mkdir -p proto/flr/v1
	protoc \
		--go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		--grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative \
		--proto_path=proto \
		proto/flr/v1/registry.proto
	@echo "Protobuf generation complete"

# Run linter
lint:
	@echo "Running linter..."
	golangci-lint run ./...

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...

# Run go vet
vet:
	@echo "Running go vet..."
	go vet ./...

# Build Docker image
docker:
	@echo "Building Docker image..."
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .
	@echo "Docker build complete: $(DOCKER_IMAGE):$(DOCKER_TAG)"

# Start local deployment
# deploy-local:
# 	@echo "Starting local FLR stack..."
# 	docker-compose up -d
# 	@echo "FLR stack started. API: http://localhost:8080, gRPC: localhost:9090"

# Stop local deployment
stop-local:
	@echo "Stopping local FLR stack..."
	docker-compose down

# View logs
logs:
	docker-compose logs -f flr-node

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -f coverage.out coverage.html
	@go clean -cache

# Show test coverage in HTML
coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Run the CLI
run:
	go run ./cmd/flr $(ARGS)

# Initialize development environment
dev-init: build
	@echo "Initializing dev environment..."
	mkdir -p data keys certs
	$(BUILD_DIR)/$(BINARY_NAME) init --id op-dev --name "Dev Operator"

# Print help
help:
	@echo "FLR — Federated Lambda Registry"
	@echo ""
	@echo "Available targets:"
	@echo "  build          Build the flr binary"
	@echo "  test           Run all tests with coverage"
	@echo "  unit-test      Run unit tests only"
	@echo "  integration-test  Run integration tests only"
	@echo "  proto          Generate protobuf Go code"
	@echo "  fmt            Format Go code"
	@echo "  vet            Run go vet"
	@echo "  lint           Run linter"
	@echo "  docker         Build Docker image"
	@echo "  deploy-local   Start local stack (docker-compose)"
	@echo "  stop-local     Stop local stack"
	@echo "  logs           View node logs"
	@echo "  clean          Clean build artifacts"
	@echo "  coverage       Generate HTML coverage report"
	@echo "  dev-init       Initialize dev environment"
	@echo "  help           Show this help"
