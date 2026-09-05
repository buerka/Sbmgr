#!/bin/sh
# Install as an authorized_keys forced command, with a fixed --home argument.
# The SSH client's original command is never evaluated or passed to a shell.
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
. "$script_dir/path-lib.sh"
[ "$#" -eq 2 ] && [ "$1" = --home ] || {
    echo "用法: $0 --home 绝对安装目录" >&2
    exit 2
}
app_dir=$(sbmgr_resolve_home "$script_dir" "$2")
sbmgr_assert_root_trusted_path "$app_dir"
unset SSH_ORIGINAL_COMMAND
SBMGR_HOME=$app_dir
export SBMGR_HOME
exec "$app_dir/sbmgr" admin snapshot
