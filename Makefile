# Variables
BINARY_NAME=gh-contribution-summarizer
BINARY_DIR=bin
GO=go
GOFLAGS=-ldflags="-s -w"

# Get the current git commit hash and date
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE=$(shell date +%FT%T%z)
VERSION?=0.1.0

# Define the build flags to include version information
LD_FLAGS=-ldflags "-X main.Version=$(VERSION) -X main.GitCommit=$(GIT_COMMIT) -X main.BuildDate=$(BUILD_DATE)"

# Default target
.PHONY: all
all: clean build

# Clean build artifacts
.PHONY: clean
clean:
	@echo "Cleaning..."
	@rm -rf $(BINARY_DIR)
	@go clean

# Build the binary
.PHONY: build
build:
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BINARY_DIR)
	$(GO) build $(GOFLAGS) $(LD_FLAGS) -o $(BINARY_DIR)/$(BINARY_NAME) .

# Run tests
.PHONY: test
test:
	@echo "Running tests..."
	$(GO) test -v ./...

# Install dependencies
.PHONY: deps
deps:
	@echo "Installing dependencies..."
	$(GO) get -v github.com/spf13/cobra
	$(GO) mod tidy

# Generate version info file
.PHONY: version
version:
	@echo "Version: $(VERSION)"
	@echo "Git commit: $(GIT_COMMIT)"
	@echo "Build date: $(BUILD_DATE)"

# Run the binary
.PHONY: run
run: build
	@echo "Running $(BINARY_NAME)..."
	@$(BINARY_DIR)/$(BINARY_NAME)

# Install the binary to $GOPATH/bin
.PHONY: install
install: build
	@echo "Installing $(BINARY_NAME)..."
	@cp $(BINARY_DIR)/$(BINARY_NAME) $(GOPATH)/bin/
	@echo "Installed to $(GOPATH)/bin/$(BINARY_NAME)"

# Help output
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  all       - Clean and build the binary"
	@echo "  clean     - Remove build artifacts"
	@echo "  build     - Build the binary"
	@echo "  test      - Run tests"
	@echo "  deps      - Install dependencies"
	@echo "  version   - Show version information"
	@echo "  run       - Build and run the binary"
	@echo "  install   - Install the binary to GOPATH/bin"
	@echo "  help      - Show this help message"