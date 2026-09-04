.PHONY: fmt fmt-check vet test privacy-check deploy-check check build build-linux

fmt:
	gofmt -w ./cmd/sbmgr

fmt-check:
	@test -z "$$(gofmt -l ./cmd)" || (gofmt -l ./cmd && exit 1)

vet:
	go vet ./...

test:
	go test ./...

privacy-check:
	python3 ./scripts/check_public_tree.py

deploy-check:
	sh ./deploy/test-deploy-scripts.sh

check: privacy-check fmt-check vet test deploy-check

build:
	go build -trimpath -o sbmgr ./cmd/sbmgr

build-linux:
	./deploy/build-linux.sh
