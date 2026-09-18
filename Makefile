.PHONY: fmt fmt-check vet compile privacy-check check build build-linux

fmt:
	gofmt -w ./cmd/sbmgr ./internal/mesh

fmt-check:
	@test -z "$$(gofmt -l ./cmd ./internal)" || (gofmt -l ./cmd ./internal && exit 1)

vet:
	go vet ./...

compile:
	go test ./...

privacy-check:
	python3 ./scripts/check_public_tree.py

check: privacy-check fmt-check vet compile

build:
	go build -trimpath -o sbmgr ./cmd/sbmgr

build-linux:
	./deploy/build-linux.sh
