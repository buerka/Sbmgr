.PHONY: fmt fmt-check vet test test-race coverage frontend-install frontend-test frontend-check privacy-check deploy-check check release-check build build-linux

fmt:
	gofmt -w ./cmd/sbmgr ./internal/mesh

fmt-check:
	@test -z "$$(gofmt -l ./cmd ./internal)" || (gofmt -l ./cmd ./internal && exit 1)

vet:
	go vet ./...

test:
	go test ./...

test-race:
	go test -race ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

frontend-install:
	npm --prefix frontend ci

frontend-test:
	npm --prefix frontend test

frontend-check:
	npm --prefix frontend run format:check
	npm --prefix frontend run typecheck

privacy-check:
	python3 ./scripts/test_check_public_tree.py
	python3 ./scripts/check_public_tree.py

deploy-check:
	sh ./deploy/test-deploy-scripts.sh

check: privacy-check fmt-check vet test frontend-check frontend-test deploy-check

release-check: check
	go test -race ./...

compile: test

build:
	go build -trimpath -o sbmgr ./cmd/sbmgr

build-linux:
	./deploy/build-linux.sh
