#!/bin/sh
set -eu

# Deploy one externally built release. Git history and tags own the software
# version history; this script only performs one safe deployment transaction.
app_dir=/root/sbmgr
binary="$app_dir/sbmgr"
state="$app_dir/state.json"
base_config="$app_dir/config.base.json"
runtime_config="$app_dir/sing-box.json"
candidate="$app_dir/.sbmgr-release.candidate"
backup_root="$app_dir/backups/state-config"

rollback_binary=
snapshot_dir=
rollback_needed=0
sbmgr_stopped=0
snapshot_keep=20

usage() {
    echo "用法: $0 <SHA256值或SHA256校验文件>" >&2
    echo "候选程序固定放在: $candidate" >&2
}

restore_previous_state() {
    echo "部署失败，正在恢复部署前的程序、状态和配置" >&2
    restore_failed=0

    systemctl stop sbmgr >/dev/null 2>&1 || restore_failed=1
    install -m 0700 "$rollback_binary" "$binary" || restore_failed=1
    install -m 0600 "$snapshot_dir/state.json" "$state" || restore_failed=1
    install -m 0600 "$snapshot_dir/config.base.json" "$base_config" || restore_failed=1
    install -m 0600 "$snapshot_dir/sing-box.json" "$runtime_config" || restore_failed=1
    systemctl restart sing-box >/dev/null 2>&1 || restore_failed=1
    systemctl start sbmgr >/dev/null 2>&1 || restore_failed=1

    if [ "$restore_failed" -ne 0 ]; then
        echo "自动恢复未完全成功，请使用快照手动恢复：$snapshot_dir" >&2
    fi
}

prune_data_snapshots() {
    find "$backup_root" -mindepth 1 -maxdepth 1 -type d -name 'pre-*' -print |
        LC_ALL=C sort -r |
        awk -v keep="$snapshot_keep" 'NR > keep' |
        while IFS= read -r old_snapshot; do
            case "$old_snapshot" in
                "$backup_root"/pre-*) rm -rf -- "$old_snapshot" ;;
                *)
                    echo "拒绝清理越界快照：$old_snapshot" >&2
                    return 1
                    ;;
            esac
        done
}

on_exit() {
    status=$?
    trap - EXIT HUP INT TERM

    if [ "$rollback_needed" -eq 1 ]; then
        restore_previous_state
    elif [ "$sbmgr_stopped" -eq 1 ]; then
        systemctl start sbmgr >/dev/null 2>&1 || \
            echo "无法重新启动原 sbmgr 服务，请手动检查" >&2
    fi

    if [ -n "$rollback_binary" ]; then
        rm -f "$rollback_binary"
    fi
    rm -f "$candidate" "$app_dir/.sbmgr.new"
    exit "$status"
}

trap on_exit EXIT
trap 'exit 130' HUP INT TERM

if [ "$(id -u)" -ne 0 ]; then
    echo "必须以 root 运行部署脚本" >&2
    exit 1
fi
if [ "$#" -ne 1 ]; then
    usage
    exit 2
fi

checksum_source=$1
if [ -f "$checksum_source" ]; then
    expected_sha256=$(awk 'NR == 1 { print $1; exit }' "$checksum_source")
else
    expected_sha256=$checksum_source
fi
if ! printf '%s\n' "$expected_sha256" | grep -Eq '^[0-9a-f]{64}$'; then
    echo "SHA256 格式不正确" >&2
    exit 2
fi

for required_file in "$candidate" "$binary" "$state" "$base_config" "$runtime_config"; do
    if [ ! -f "$required_file" ]; then
        echo "部署文件不完整：缺少 $required_file" >&2
        exit 1
    fi
done

actual_sha256=$(sha256sum "$candidate" | awk '{print $1}')
if [ "$actual_sha256" != "$expected_sha256" ]; then
    echo "候选二进制 SHA256 不匹配" >&2
    exit 1
fi
chmod 0700 "$candidate"

new_version_output=$("$candidate" version)
case "$new_version_output" in
    "sbmgr "*) new_version=${new_version_output#sbmgr } ;;
    *)
        echo "无法识别候选程序版本" >&2
        exit 1
        ;;
esac
if ! printf '%s\n' "$new_version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo "候选程序不是正式 Git 标签版本：$new_version" >&2
    exit 1
fi

# Complete all read-only preflight checks before stopping the running daemon.
"$candidate" --state "$state" admin check
"$candidate" --state "$state" admin rate check
"$candidate" --state "$state" admin proxy list >/dev/null

old_version_output=$("$binary" version)
case "$old_version_output" in
    "sbmgr "*) old_version=${old_version_output#sbmgr } ;;
    *)
        echo "无法识别当前程序版本" >&2
        exit 1
        ;;
esac
case "$old_version" in
    ''|*[!0-9A-Za-z._+-]*)
        echo "当前程序版本包含不安全字符" >&2
        exit 1
        ;;
esac

install -d -m 0700 "$backup_root"
snapshot_stamp=$(date -u +%Y%m%dT%H%M%SZ)
snapshot_dir=$(mktemp -d "$backup_root/pre-$snapshot_stamp-$new_version.XXXXXX")
chmod 0700 "$snapshot_dir"
rollback_binary=$(mktemp "$app_dir/.sbmgr-deploy-rollback.XXXXXX")
install -m 0700 "$binary" "$rollback_binary"

# Stop the writer before taking the durable data snapshot so the three files
# represent one deployment boundary. The old executable remains temporary.
sbmgr_stopped=1
systemctl stop sbmgr
install -m 0600 "$state" "$snapshot_dir/state.json"
install -m 0600 "$base_config" "$snapshot_dir/config.base.json"
install -m 0600 "$runtime_config" "$snapshot_dir/sing-box.json"
sha256sum \
    "$snapshot_dir/state.json" \
    "$snapshot_dir/config.base.json" \
    "$snapshot_dir/sing-box.json" >"$snapshot_dir/SHA256SUMS"
chmod 0600 "$snapshot_dir/SHA256SUMS"

# From this point on, a failure must restore both the program and all data.
rollback_needed=1
install -m 0700 "$candidate" "$app_dir/.sbmgr.new"
mv -f "$app_dir/.sbmgr.new" "$binary"

if [ "$("$binary" version)" != "sbmgr $new_version" ]; then
    echo "安装后的版本检查失败" >&2
    exit 1
fi

"$binary" --state "$state" admin check
"$binary" --state "$state" admin rate check
systemctl start sbmgr
systemctl is-active --quiet sbmgr
systemctl is-active --quiet sing-box

# A successful deployment must not leave the previous executable behind.
rm -f "$rollback_binary"
rollback_binary=
rollback_needed=0
sbmgr_stopped=0
rm -f "$candidate"
if ! prune_data_snapshots; then
    echo "部署成功，但旧数据快照清理失败；请检查 $backup_root" >&2
fi
trap - EXIT HUP INT TERM

echo "sbmgr $new_version 部署完成（发布版本由 Git 标签 v$new_version 管理）"
echo "数据快照：$snapshot_dir"
echo "旧程序 $old_version 的临时回滚副本已删除"
