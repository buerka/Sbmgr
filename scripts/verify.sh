#!/bin/sh
set -eu
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$repo_dir"
[ -z "$(gofmt -l ./cmd ./internal)" ] || {
    echo 'Run gofmt -w cmd/sbmgr internal/mesh first.' >&2
    exit 1
}
npm --prefix frontend ci
npm --prefix frontend run format:check
npm --prefix frontend test
npm --prefix frontend run build
go vet ./...
go test ./...
python3 scripts/test_check_public_tree.py
python3 scripts/check_public_tree.py
for script in deploy/*.sh scripts/*.sh; do sh -n "$script"; done
mkdir -p .dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o .dist/sbmgr-check-linux-amd64 ./cmd/sbmgr
