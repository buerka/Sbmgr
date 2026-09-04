.PHONY: fmt fmt-check vet test deploy-check check build build-linux

fmt:
	gofmt -w ./cmd/sbmgr

fmt-check:
	@test -z "$$(gofmt -l ./cmd)" || (gofmt -l ./cmd && exit 1)

vet:
	go vet ./...

test:
	go test ./...

deploy-check:
	sh ./deploy/test-deploy-scripts.sh

check: fmt-check vet test deploy-check

build:
	go build -trimpath -o sbmgr ./cmd/sbmgr

build-linux:
	./deploy/build-linux.sh
