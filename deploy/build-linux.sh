#!/bin/sh
set -eu

# Build an immutable Linux release from the Git tag attached to HEAD. This is
# deliberately external to sbmgr: Git owns source/version history, while the
# running application only owns state and configuration backups.
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_root"

if ! git_tag=$(git describe --tags --exact-match HEAD 2>/dev/null); then
    echo "当前提交没有精确 Git 标签；请先创建 vX.Y.Z 标签" >&2
    exit 1
fi
if ! printf '%s\n' "$git_tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo "发布标签必须使用 vX.Y.Z 格式，当前为 $git_tag" >&2
    exit 1
fi

if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
    echo "工作区不干净；请先提交或清理改动再构建发布包" >&2
    exit 1
fi

version=${git_tag#v}
commit=$(git rev-parse --verify HEAD)
output_dir="$repo_root/.dist"
output="$output_dir/sbmgr-$version-linux-amd64"
checksum_file="$output.sha256"

mkdir -p "$output_dir"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags "-s -w -X main.appVersion=$version -X main.gitCommit=$commit" \
    -o "$output" \
    ./cmd/sbmgr
chmod 0755 "$output"
sha256sum "$output" >"$checksum_file"

echo "已从 $git_tag 构建：$output"
echo "校验文件：$checksum_file"
