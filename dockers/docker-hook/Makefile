# The hook runs inside the Ubuntu x86-64 OAI images, so build a static binary.
GOOS ?= linux
GOARCH ?= amd64
HOOK_OUT ?= ../docker-build/build/hook

.PHONY: all
all: fmt vet build

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: ## Run go test against code.
	go test ./...

.PHONY: build
build: ## Build the hook into the docker-build directory used by build.sh.
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -o $(HOOK_OUT) ./cmd/hook
