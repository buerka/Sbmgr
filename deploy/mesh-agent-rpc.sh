#!/bin/sh
# Use as a fixed authorized_keys command with a dedicated deployment key.
# The bounded, versioned RPC accepts typed node plans, never shell commands.
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
. "$script_dir/path-lib.sh"
[ "$#" -eq 2 ] && [ "$1" = --home ] || {
    echo "Usage: mesh-agent-rpc.sh --home /absolute/app/directory" >&2
    exit 2
}
app_dir=$(sbmgr_resolve_home "$script_dir" "$2")
sbmgr_assert_root_trusted_path "$app_dir"
unset SSH_ORIGINAL_COMMAND
SBMGR_HOME=$app_dir
export SBMGR_HOME
exec "$app_dir/sbmgr" admin mesh rpc
