IMAGE_NAME ?= yourregistry/tiphys
IMAGE_TAG  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BINARY     := webhook-server

.PHONY: all build run test lint docker-build docker-push deploy clean help tidy

all: build

# Auto-generate go.sum if it doesn't exist
go.sum:
	@echo ">> go.sum missing — running go mod tidy..."
	go mod tidy

## build: Compile the binary
build: go.sum
	@echo ">> Building $(BINARY)..."
	CGO_ENABLED=0 go build -ldflags="-w -s" -o bin/$(BINARY) ./cmd/server

## run: Run the server locally (requires env vars to be set)
run: go.sum
	go run ./cmd/server

## test: Run all tests
test: go.sum
	go test ./... -v -race -count=1

## lint: Run golangci-lint
lint:
	golangci-lint run ./...

## tidy: Tidy go modules
tidy:
	go mod tidy

## docker-build: Build the Docker image
docker-build:
	docker build -t $(IMAGE_NAME):$(IMAGE_TAG) -t $(IMAGE_NAME):latest .

## docker-push: Push the Docker image
docker-push:
	docker push $(IMAGE_NAME):$(IMAGE_TAG)
	docker push $(IMAGE_NAME):latest

## deploy: Apply Kubernetes manifests
deploy:
	kubectl apply -f deploy/k8s/

## undeploy: Delete Kubernetes resources
undeploy:
	kubectl delete -f deploy/k8s/ --ignore-not-found

## clean: Remove build artifacts
clean:
	rm -rf bin/

## help: Print this help
help:
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'
