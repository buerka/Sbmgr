#!/bin/sh
set -eu

# Deploy one externally built Git release. The running application owns data,
# while this script owns only the atomic deployment boundary and rollback.
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
# shellcheck source=path-lib.sh
. "$script_dir/path-lib.sh"

home_override_set=0
home_override=
sing_box_override=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --home)
            [ "$#" -ge 2 ] || { echo "--home 需要一个绝对路径" >&2; exit 2; }
            home_override_set=1
            home_override=$2
            shift 2
            ;;
        --sing-box-bin)
            [ "$#" -ge 2 ] || { echo "--sing-box-bin 需要一个绝对路径" >&2; exit 2; }
            sing_box_override=$2
            shift 2
            ;;
        --help|-h)
            set -- --help
            break
            ;;
        --)
            shift
            break
            ;;
        -*) echo "未知参数：$1" >&2; exit 2 ;;
        *) break ;;
    esac
done

if [ "$home_override_set" -eq 1 ]; then
    app_dir=$(sbmgr_resolve_home "$script_dir" "$home_override")
else
    app_dir=$(sbmgr_resolve_home "$script_dir")
fi
binary="$app_dir/sbmgr"
base_config="$app_dir/config.base.json"
runtime_config="$app_dir/sing-box.json"
database_state="$app_dir/state.db"
candidate="$app_dir/.sbmgr-release.candidate"
backup_root="$app_dir/backups/state-config"

rollback_binary=
rollback_binary_sha256=
snapshot_dir=
preflight_dir=
restore_dir=
rollback_needed=0
sbmgr_stopped=0
app_state_lock_held=0
legacy_state_lock_held=0
snapshot_keep=20

usage() {
    echo "用法: $0 [--home 绝对安装目录] [--sing-box-bin 绝对路径] <SHA256值或SHA256校验文件>" >&2
    echo "也可通过 SBMGR_HOME 指定目录；默认取本脚本所在 deploy/ 的父目录。" >&2
    echo "候选程序固定放在: $candidate" >&2
}

valid_state_name() {
    case "$1" in
        state.db|state.db-wal|state.db-journal|state.json|state.json.migrated) return 0 ;;
        *) return 1 ;;
    esac
}

same_sha256() {
    [ "$#" -eq 2 ] || return 2
    [ "$(sha256sum "$1" | awk '{print $1}')" = "$(sha256sum "$2" | awk '{print $1}')" ]
}

verify_installed_release() {
    [ -f "$binary" ] && [ ! -L "$binary" ] || {
        echo "已安装的 sbmgr 不存在或不是普通文件" >&2
        return 1
    }
    installed_release_sha256=$(sha256sum "$binary" | awk '{print $1}') || {
        echo "无法校验已安装的 sbmgr" >&2
        return 1
    }
    [ "$installed_release_sha256" = "$expected_sha256" ] || {
        echo "稳定性检查期间已安装的 sbmgr 内容发生变化" >&2
        return 1
    }
    installed_release_version=$("$binary" version) || {
        echo "稳定性检查期间无法读取已安装的 sbmgr 版本" >&2
        return 1
    }
    [ "$installed_release_version" = "sbmgr $new_version" ] || {
        echo "稳定性检查期间已安装的 sbmgr 版本发生变化" >&2
        return 1
    }
}

systemd_nonnegative_integer() {
    systemd_integer_value=$(sbmgr_systemd_value "$1" "$2") || {
        echo "无法读取 $1 的 $2" >&2
        return 1
    }
    case "$systemd_integer_value" in
        ''|*[!0-9]*)
            echo "$1 的 $2 不是非负整数" >&2
            return 1
            ;;
    esac
    printf '%s\n' "$systemd_integer_value"
}

wait_for_post_start_stability() {
    # Type=simple can be reported active before initialization failures surface.
    # Observe across more than two RestartSec=5s windows. No duration knobs are
    # exposed; isolated regression tests provide a controlled sleep via PATH.
    post_start_observations=9
    post_start_delay_seconds=2
    post_start_observation=1
    post_start_main_pid=
    post_start_restart_baseline=
    command -v timeout >/dev/null 2>&1 || {
        echo "缺少 timeout，无法建立有界的部署后业务自检" >&2
        return 1
    }

    while [ "$post_start_observation" -le "$post_start_observations" ]; do
        systemctl is-active --quiet sbmgr.service || {
            echo "部署后稳定性检查失败：sbmgr.service 未保持 active" >&2
            return 1
        }
        systemctl is-active --quiet sing-box.service || {
            echo "部署后稳定性检查失败：sing-box.service 未保持 active" >&2
            return 1
        }

        post_start_current_pid=$(systemd_nonnegative_integer sbmgr.service MainPID) || return 1
        [ "$post_start_current_pid" -gt 0 ] || {
            echo "部署后稳定性检查失败：sbmgr.service 没有有效 MainPID" >&2
            return 1
        }
        post_start_current_restarts=$(systemd_nonnegative_integer sbmgr.service NRestarts) || return 1

        if [ "$post_start_observation" -eq 1 ]; then
            # NRestarts may include an old unit lifecycle on some systemd
            # versions, so the first new-process observation is the baseline.
            post_start_main_pid=$post_start_current_pid
            post_start_restart_baseline=$post_start_current_restarts
        else
            [ "$post_start_current_pid" = "$post_start_main_pid" ] || {
                echo "部署后稳定性检查失败：sbmgr.service MainPID 发生变化" >&2
                return 1
            }
            [ "$post_start_current_restarts" = "$post_start_restart_baseline" ] || {
                echo "部署后稳定性检查失败：sbmgr.service NRestarts 发生变化" >&2
                return 1
            }
        fi

        verify_installed_release || return 1
        if [ "$post_start_observation" -lt "$post_start_observations" ]; then
            sleep "$post_start_delay_seconds" || {
                echo "部署后稳定性等待被中断" >&2
                return 1
            }
        fi
        post_start_observation=$((post_start_observation + 1))
    done

    # This is deliberately a CLI business check rather than a fixed-port probe:
    # it reloads the deployed SQLite state, renders the managed configuration,
    # and asks the configured sing-box binary to validate that configuration.
    timeout 30 "$binary" --state "$database_state" admin check >/dev/null || {
        echo "部署后业务自检失败" >&2
        return 1
    }

    # Close the race between the business check and the deployment commit.
    systemctl is-active --quiet sbmgr.service || {
        echo "业务自检后 sbmgr.service 不再 active" >&2
        return 1
    }
    systemctl is-active --quiet sing-box.service || {
        echo "业务自检后 sing-box.service 不再 active" >&2
        return 1
    }
    post_start_final_pid=$(systemd_nonnegative_integer sbmgr.service MainPID) || return 1
    post_start_final_restarts=$(systemd_nonnegative_integer sbmgr.service NRestarts) || return 1
    [ "$post_start_final_pid" = "$post_start_main_pid" ] || {
        echo "业务自检后 sbmgr.service MainPID 发生变化" >&2
        return 1
    }
    [ "$post_start_final_restarts" = "$post_start_restart_baseline" ] || {
        echo "业务自检后 sbmgr.service NRestarts 发生变化" >&2
        return 1
    }
    verify_installed_release || return 1
    return 0
}

acquire_state_locks() {
    command -v flock >/dev/null 2>&1 || {
        echo "缺少 flock，无法建立安全部署边界" >&2
        return 1
    }
    # New releases use one directory-wide state.lock and may then lock the old
    # state.json during first import. v0.22 uses only state.json.lock. Taking
    # them in that order serializes both generations without an inversion.
    app_lock_acquired_here=0
    if [ "$app_state_lock_held" -eq 0 ]; then
        exec 9>"$app_dir/state.lock"
        if ! flock -w 30 -x 9; then
            echo "30 秒内无法取得应用状态部署锁" >&2
            exec 9>&-
            return 1
        fi
        app_state_lock_held=1
        app_lock_acquired_here=1
    fi
    if [ "$legacy_state_lock_held" -eq 0 ]; then
        exec 8>"$app_dir/state.json.lock"
        if ! flock -w 30 -x 8; then
            echo "30 秒内无法取得旧 state.json 部署锁" >&2
            exec 8>&-
            if [ "$app_lock_acquired_here" -eq 1 ]; then
                flock -u 9 || true
                exec 9>&-
                app_state_lock_held=0
            fi
            return 1
        fi
        legacy_state_lock_held=1
    fi
}

release_legacy_state_lock() {
    [ "$legacy_state_lock_held" -eq 1 ] || return 0
    flock -u 8 || true
    exec 8>&-
    legacy_state_lock_held=0
}

release_state_locks() {
    if [ "$legacy_state_lock_held" -eq 1 ]; then
        flock -u 8 || true
        exec 8>&-
        legacy_state_lock_held=0
    fi
    if [ "$app_state_lock_held" -eq 1 ]; then
        flock -u 9 || true
        exec 9>&-
        app_state_lock_held=0
    fi
}

validate_snapshot() {
    target_snapshot=$1
    [ -d "$target_snapshot" ] && [ ! -L "$target_snapshot" ] || {
        echo "快照目录无效：$target_snapshot" >&2
        return 1
    }
    for required_name in STATE_FILES SHA256SUMS config.base.json sing-box.json; do
        [ -f "$target_snapshot/$required_name" ] && [ ! -L "$target_snapshot/$required_name" ] || {
            echo "快照缺少普通文件：$required_name" >&2
            return 1
        }
    done
    [ -s "$target_snapshot/STATE_FILES" ] || {
        echo "快照状态清单为空" >&2
        return 1
    }

    state_count=0
    has_primary_state=0
    seen_state_names=
    while IFS= read -r state_name; do
        valid_state_name "$state_name" || {
            echo "快照包含不安全的状态文件名：$state_name" >&2
            return 1
        }
        case " $seen_state_names " in
            *" $state_name "*) echo "快照状态清单有重复项：$state_name" >&2; return 1 ;;
        esac
        seen_state_names="$seen_state_names $state_name"
        [ -f "$target_snapshot/$state_name" ] && [ ! -L "$target_snapshot/$state_name" ] || {
            echo "快照状态文件无效：$state_name" >&2
            return 1
        }
        case "$state_name" in state.db|state.json) has_primary_state=1 ;; esac
        state_count=$((state_count + 1))
    done <"$target_snapshot/STATE_FILES"
    [ "$has_primary_state" -eq 1 ] || {
        echo "快照没有 state.db 或 state.json" >&2
        return 1
    }

    checksum_names=$(awk '
        NF != 2 || $1 !~ /^[0-9a-f]{64}$/ { bad = 1; next }
        { name = $2; sub(/^\*/, "", name); print name }
        END { if (bad) exit 1 }
    ' "$target_snapshot/SHA256SUMS") || {
        echo "快照 SHA256SUMS 格式无效" >&2
        return 1
    }
    expected_names=$(
        {
            sed '/^$/d' "$target_snapshot/STATE_FILES"
            printf '%s\n' config.base.json sing-box.json
        } | LC_ALL=C sort
    )
    actual_names=$(printf '%s\n' "$checksum_names" | LC_ALL=C sort)
    [ "$actual_names" = "$expected_names" ] || {
        echo "快照校验清单与白名单不一致" >&2
        return 1
    }
    [ "$(printf '%s\n' "$checksum_names" | sed '/^$/d' | wc -l | tr -d ' ')" -eq $((state_count + 2)) ] || {
        echo "快照校验清单数量不正确" >&2
        return 1
    }
    (cd "$target_snapshot" && sha256sum -c SHA256SUMS >/dev/null) || {
        echo "快照 SHA256 校验失败" >&2
        return 1
    }
}

create_snapshot() {
    install -d -m 0700 "$backup_root"
    snapshot_stamp=$(date -u +%Y%m%dT%H%M%SZ)
    snapshot_dir=$(mktemp -d "$backup_root/pre-$snapshot_stamp-$new_version.XXXXXX")
    chmod 0700 "$snapshot_dir"
    state_manifest="$snapshot_dir/STATE_FILES"
    : >"$state_manifest"
    chmod 0600 "$state_manifest"

    # state.db-shm is disposable shared memory and must never be restored.
    # With both cooperative locks held and the daemon stopped, DB/WAL/journal
    # cannot change while these exact bytes are copied.
    for state_name in state.db state.db-wal state.db-journal state.json state.json.migrated; do
        if [ -f "$app_dir/$state_name" ] && [ ! -L "$app_dir/$state_name" ]; then
            install -m 0600 "$app_dir/$state_name" "$snapshot_dir/$state_name"
            printf '%s\n' "$state_name" >>"$state_manifest"
        elif [ -e "$app_dir/$state_name" ] || [ -L "$app_dir/$state_name" ]; then
            echo "拒绝快照非普通状态文件：$app_dir/$state_name" >&2
            return 1
        fi
    done
    [ -s "$state_manifest" ] || {
        echo "停止服务后未找到任何状态文件，拒绝继续部署" >&2
        return 1
    }
    install -m 0600 "$base_config" "$snapshot_dir/config.base.json"
    install -m 0600 "$runtime_config" "$snapshot_dir/sing-box.json"
    (
        cd "$snapshot_dir"
        while IFS= read -r state_name; do
            sha256sum "$state_name"
        done <STATE_FILES
        sha256sum config.base.json sing-box.json
    ) >"$snapshot_dir/SHA256SUMS"
    chmod 0600 "$snapshot_dir/SHA256SUMS"
    validate_snapshot "$snapshot_dir"
}

prepare_preflight_state() {
    validate_snapshot "$snapshot_dir"
    preflight_dir=$(mktemp -d "$app_dir/.sbmgr-preflight.XXXXXX")
    chmod 0700 "$preflight_dir"
    while IFS= read -r state_name; do
        valid_state_name "$state_name" || return 1
        install -m 0600 "$snapshot_dir/$state_name" "$preflight_dir/$state_name"
    done <"$snapshot_dir/STATE_FILES"
    # Always request the future canonical database path. A legacy JSON snapshot
    # is migrated only inside this shadow directory during candidate preflight.
    preflight_state="$preflight_dir/state.db"
    "$candidate" --state "$preflight_state" admin check
    "$candidate" --state "$preflight_state" admin rate check
    "$candidate" --state "$preflight_state" admin proxy list >/dev/null
}

restore_previous_state() {
    echo "部署失败，正在恢复部署前的程序、状态和配置" >&2
    systemctl stop sbmgr.service sing-box.service >/dev/null 2>&1 || true

    # Validate every recovery input before deleting or replacing any live file.
    validate_snapshot "$snapshot_dir" || return 1
    [ -f "$rollback_binary" ] && [ ! -L "$rollback_binary" ] || {
        echo "回滚程序不存在或不是普通文件" >&2
        return 1
    }
    [ "$(sha256sum "$rollback_binary" | awk '{print $1}')" = "$rollback_binary_sha256" ] || {
        echo "回滚程序 SHA256 校验失败" >&2
        return 1
    }

    restore_dir=$(mktemp -d "$app_dir/.sbmgr-restore.XXXXXX") || return 1
    chmod 0700 "$restore_dir" || return 1
    install -m 0700 "$rollback_binary" "$restore_dir/sbmgr" || return 1
    install -m 0600 "$snapshot_dir/config.base.json" "$restore_dir/config.base.json" || return 1
    install -m 0600 "$snapshot_dir/sing-box.json" "$restore_dir/sing-box.json" || return 1
    while IFS= read -r state_name; do
        valid_state_name "$state_name" || return 1
        install -m 0600 "$snapshot_dir/$state_name" "$restore_dir/$state_name" || return 1
    done <"$snapshot_dir/STATE_FILES"
    same_sha256 "$rollback_binary" "$restore_dir/sbmgr" || return 1
    same_sha256 "$snapshot_dir/config.base.json" "$restore_dir/config.base.json" || return 1
    same_sha256 "$snapshot_dir/sing-box.json" "$restore_dir/sing-box.json" || return 1
    while IFS= read -r state_name; do
        same_sha256 "$snapshot_dir/$state_name" "$restore_dir/$state_name" || return 1
    done <"$snapshot_dir/STATE_FILES"

    acquire_state_locks || return 1
    rm -f -- \
        "$app_dir/state.db" "$app_dir/state.db-wal" "$app_dir/state.db-shm" \
        "$app_dir/state.db-journal" "$app_dir/state.json" "$app_dir/state.json.migrated" || {
        release_state_locks
        return 1
    }
    while IFS= read -r state_name; do
        mv -f "$restore_dir/$state_name" "$app_dir/$state_name" || {
            release_state_locks
            return 1
        }
    done <"$snapshot_dir/STATE_FILES"
    mv -f "$restore_dir/config.base.json" "$base_config" || { release_state_locks; return 1; }
    mv -f "$restore_dir/sing-box.json" "$runtime_config" || { release_state_locks; return 1; }
    mv -f "$restore_dir/sbmgr" "$binary" || { release_state_locks; return 1; }
    release_state_locks

    rm -rf -- "$restore_dir"
    restore_dir=
    if ! sbmgr_assert_core_systemd_layout "$app_dir" "$sing_box_bin"; then
        return 1
    fi
    if ! systemctl start sing-box.service; then
        systemctl stop sbmgr.service sing-box.service >/dev/null 2>&1 || true
        return 1
    fi
    if ! systemctl start sbmgr.service; then
        systemctl stop sbmgr.service sing-box.service >/dev/null 2>&1 || true
        return 1
    fi
    if ! systemctl is-active --quiet sing-box.service || ! systemctl is-active --quiet sbmgr.service; then
        systemctl stop sbmgr.service sing-box.service >/dev/null 2>&1 || true
        return 1
    fi
    return 0
}

prune_data_snapshots() {
    find "$backup_root" -mindepth 1 -maxdepth 1 -type d -name 'pre-*' -print |
        LC_ALL=C sort -r |
        awk -v keep="$snapshot_keep" 'NR > keep' |
        while IFS= read -r old_snapshot; do
            case "$old_snapshot" in
                "$backup_root"/pre-*) rm -rf -- "$old_snapshot" ;;
                *) echo "拒绝清理越界快照：$old_snapshot" >&2; return 1 ;;
            esac
        done
}

on_exit() {
    status=$?
    trap - EXIT HUP INT TERM
    preserve_rollback_binary=0

    if [ "$rollback_needed" -eq 1 ]; then
        if ! restore_previous_state; then
            preserve_rollback_binary=1
            release_state_locks
            systemctl stop sbmgr.service sing-box.service >/dev/null 2>&1 || true
            echo "自动恢复未成功；为防止进一步写入，sbmgr 与 sing-box 已保持停止。" >&2
            echo "请核验后从此快照手动恢复：$snapshot_dir" >&2
            echo "部署前程序保留在：$rollback_binary" >&2
        fi
    elif [ "$sbmgr_stopped" -eq 1 ]; then
        release_state_locks
        if ! systemctl start sbmgr.service >/dev/null 2>&1; then
            echo "无法重新启动原 sbmgr 服务，请手动检查" >&2
        fi
    fi

    if [ -n "$preflight_dir" ]; then
        rm -rf -- "$preflight_dir"
    fi
    if [ -n "$restore_dir" ]; then
        rm -rf -- "$restore_dir"
    fi
    if [ -n "$rollback_binary" ] && [ "$preserve_rollback_binary" -eq 0 ]; then
        rm -f -- "$rollback_binary"
    fi
    rm -f -- "$candidate" "$app_dir/.sbmgr.new"
    exit "$status"
}

trap on_exit EXIT
trap 'exit 130' HUP INT TERM

if [ "$(id -u)" -ne 0 ]; then
    echo "必须以 root 运行部署脚本" >&2
    exit 1
fi
sbmgr_assert_root_trusted_path "$app_dir"
if [ "$#" -ne 1 ] || [ "${1:-}" = --help ]; then
    usage
    [ "${1:-}" = --help ] && exit 0
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

state=$(sbmgr_select_state_file "$app_dir")
for required_file in "$candidate" "$binary" "$state" "$base_config" "$runtime_config"; do
    if [ ! -f "$required_file" ] || [ -L "$required_file" ]; then
        echo "部署文件不完整或不是普通文件：$required_file" >&2
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
    *) echo "无法识别候选程序版本" >&2; exit 1 ;;
esac
if ! printf '%s\n' "$new_version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo "候选程序不是正式 Git 标签版本：$new_version" >&2
    exit 1
fi

old_version_output=$("$binary" version)
case "$old_version_output" in
    "sbmgr "*) old_version=${old_version_output#sbmgr } ;;
    *) echo "无法识别当前程序版本" >&2; exit 1 ;;
esac
case "$old_version" in
    ''|*[!0-9A-Za-z._+-]*) echo "当前程序版本包含不安全字符" >&2; exit 1 ;;
esac

if [ -n "$sing_box_override" ]; then
    sing_box_bin=$(sbmgr_resolve_executable "$sing_box_override")
else
    sing_box_bin=$(sbmgr_resolve_executable sing-box)
fi
sbmgr_assert_core_systemd_layout "$app_dir" "$sing_box_bin"
systemctl is-active --quiet sbmgr.service || { echo "sbmgr.service 当前未运行" >&2; exit 1; }
systemctl is-active --quiet sing-box.service || { echo "sing-box.service 当前未运行" >&2; exit 1; }

install -d -m 0700 "$backup_root"
rollback_binary=$(mktemp "$app_dir/.sbmgr-deploy-rollback.XXXXXX")
install -m 0700 "$binary" "$rollback_binary"
rollback_binary_sha256=$(sha256sum "$rollback_binary" | awk '{print $1}')

# Stop every known state writer before taking either lock. The obsolete timer
# is disabled so it cannot return after reboot; install-systemd.sh archives and
# removes its legacy unit files when systemd templates are next installed.
systemctl disable --now sbmgr-sync.timer >/dev/null 2>&1 || true
systemctl stop sbmgr-sync.service >/dev/null 2>&1 || true
if systemctl is-active --quiet sbmgr-sync.timer \
    || systemctl is-active --quiet sbmgr-sync.service; then
    echo "旧 sbmgr-sync 写入器仍在运行，拒绝制作状态快照" >&2
    exit 1
fi
if systemctl is-enabled --quiet sbmgr-sync.timer; then
    echo "旧 sbmgr-sync.timer 仍处于启用状态，拒绝继续部署" >&2
    exit 1
fi
sbmgr_stopped=1
systemctl stop sbmgr.service
if systemctl is-active --quiet sbmgr.service; then
    echo "sbmgr.service 停止失败" >&2
    exit 1
fi
command -v pgrep >/dev/null 2>&1 || {
    echo "缺少 pgrep，无法排除交互式旧 sbmgr 进程" >&2
    exit 1
}
if pgrep -x sbmgr >/dev/null 2>&1; then
    echo "检测到 systemd 之外仍有 sbmgr 进程；请退出其他 CUI 后重试部署" >&2
    exit 1
fi
acquire_state_locks
create_snapshot

# The candidate's first state access is against the verified shadow snapshot.
# It cannot migrate, checkpoint, or create WAL files beside the live database.
prepare_preflight_state
rm -rf -- "$preflight_dir"
preflight_dir=

# From the binary switch onward, every failure restores the verified snapshot.
rollback_needed=1
install -m 0700 "$candidate" "$app_dir/.sbmgr.new"
mv -f "$app_dir/.sbmgr.new" "$binary"
if [ "$("$binary" version)" != "sbmgr $new_version" ]; then
    echo "安装后的版本检查失败" >&2
    exit 1
fi

# This command synchronously imports legacy state.json into state.db when
# needed. All validation completes before sbmgr.service is allowed to start.
# The importer takes the legacy lock itself. No old process can appear now that
# the old executable has been replaced; state.lock remains held throughout.
release_legacy_state_lock
"$binary" --state "$database_state" admin check
[ -s "$database_state" ] && [ ! -L "$database_state" ] || {
    echo "JSON → SQLite 迁移没有生成有效的 state.db" >&2
    exit 1
}
"$binary" --state "$database_state" admin check
sbmgr_assert_core_systemd_layout "$app_dir" "$sing_box_bin"

systemctl start sbmgr.service
release_state_locks
wait_for_post_start_stability

rollback_needed=0
sbmgr_stopped=0
rm -f -- "$rollback_binary"
rollback_binary=
rm -f -- "$candidate"
if ! prune_data_snapshots; then
    echo "部署成功，但旧数据快照清理失败；请检查 $backup_root" >&2
fi
trap - EXIT HUP INT TERM

echo "sbmgr $new_version 部署完成（发布版本由 Git 标签 v$new_version 管理）"
echo "数据快照：$snapshot_dir"
echo "SQLite 状态已在服务启动前迁移并校验；旧程序 $old_version 的临时副本已删除"
