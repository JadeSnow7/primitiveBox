.PHONY: build test clean run sdk-test sandbox-image sandbox-browser-image ui-build demo

build:
	go build -o bin/pb ./cmd/pb/

run: build
	./bin/pb server start --workspace .

test:
	go test ./... -v

sdk-test:
	python3 -m pytest sdk/python/tests -q

ui-build:
	cd cmd/pb-ui && npm run build

sandbox-image: build
	GOOS=linux GOARCH=arm64 go build -o bin/pb-linux-arm64 ./cmd/pb
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
	go fmt ./...
