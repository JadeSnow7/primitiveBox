.PHONY: build test lint clean run sdk-test sandbox-image sandbox-browser-image ui-build demo

GOLANGCI_LINT_VERSION ?= $(shell cat .golangci-version 2>/dev/null)
GO_BUILD_CACHE ?= /tmp/primitivebox-go-build-cache

build:
	mkdir -p $(GO_BUILD_CACHE)
	GOCACHE=$(GO_BUILD_CACHE) go build -o bin/pb ./cmd/pb/
	GOCACHE=$(GO_BUILD_CACHE) go build -o bin/pb-runtimed ./cmd/pb-runtimed/
	GOCACHE=$(GO_BUILD_CACHE) go build -o bin/pb-os-adapter ./cmd/pb-os-adapter/
	GOCACHE=$(GO_BUILD_CACHE) go build -o bin/pb-test-adapter ./cmd/pb-test-adapter/
	GOCACHE=$(GO_BUILD_CACHE) go build -o bin/pb-repo-adapter ./cmd/pb-repo-adapter/
	GOCACHE=$(GO_BUILD_CACHE) go build -o bin/pb-data-adapter ./cmd/pb-data-adapter/

run: build
	./bin/pb server start --workspace .

test:
	mkdir -p $(GO_BUILD_CACHE)
	GOCACHE=$(GO_BUILD_CACHE) go test ./... -v

lint:
	@echo "Expected golangci-lint version: $(GOLANGCI_LINT_VERSION)"
	./scripts/lint.sh

sdk-test:
	python3 -m pytest sdk/python/tests -q

ui-build:
	cd cmd/pb-ui && npm run build

sandbox-image: build
	mkdir -p $(GO_BUILD_CACHE)
	GOCACHE=$(GO_BUILD_CACHE) GOOS=linux GOARCH=arm64 go build -o bin/pb-linux-arm64 ./cmd/pb
	GOCACHE=$(GO_BUILD_CACHE) GOOS=linux GOARCH=arm64 go build -o bin/pb-runtimed-linux-arm64 ./cmd/pb-runtimed
	GOCACHE=$(GO_BUILD_CACHE) GOOS=linux GOARCH=arm64 go build -o bin/pb-os-adapter-linux-arm64 ./cmd/pb-os-adapter
	GOCACHE=$(GO_BUILD_CACHE) GOOS=linux GOARCH=arm64 go build -o bin/pb-test-adapter-linux-arm64 ./cmd/pb-test-adapter
	GOCACHE=$(GO_BUILD_CACHE) GOOS=linux GOARCH=arm64 go build -o bin/pb-repo-adapter-linux-arm64 ./cmd/pb-repo-adapter
	docker build -f testdata/docker/Dockerfile -t primitivebox-sandbox:latest .

sandbox-browser-image: sandbox-image
	docker build -f testdata/docker/browser.Dockerfile -t primitivebox-sandbox-browser:latest .

demo:
	@echo "1. make sandbox-image"
	@echo "2. ./bin/pb sandbox create --mount ./examples/auto_fix_bug"
	@echo "3. ./bin/pb server start --workspace ."
	@echo "4. python3 examples/auto_fix_bug/agent.py http://localhost:8080 <sandbox_id>"

clean:
	rm -rf bin/
	rm -rf .primitivebox/

fmt:
	mkdir -p $(GO_BUILD_CACHE)
	GOCACHE=$(GO_BUILD_CACHE) go fmt ./...
