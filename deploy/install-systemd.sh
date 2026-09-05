#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
# shellcheck source=path-lib.sh
. "$script_dir/path-lib.sh"

home_override_set=0
home_override=
component=all
output_dir=
sing_box_override=

usage() {
    cat >&2 <<EOF
用法: $0 [--home 绝对安装目录] [--component all|core|https] [--sing-box-bin 绝对路径]
       $0 [--home 绝对安装目录] [--component all|core|https] [--sing-box-bin 绝对路径] --output-dir 目录

默认取本脚本所在 deploy/ 的父目录，也可使用 SBMGR_HOME 或 --home 覆盖。
--output-dir 只渲染 unit 到指定目录，不调用 systemctl（供检查/打包使用）。
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --home)
            [ "$#" -ge 2 ] || { echo "--home 需要一个绝对路径" >&2; exit 2; }
            home_override_set=1
            home_override=$2
            shift 2
            ;;
        --component)
            [ "$#" -ge 2 ] || { echo "--component 需要参数" >&2; exit 2; }
            component=$2
            shift 2
            ;;
        --output-dir)
            [ "$#" -ge 2 ] || { echo "--output-dir 需要参数" >&2; exit 2; }
            output_dir=$2
            shift 2
            ;;
        --sing-box-bin)
            [ "$#" -ge 2 ] || { echo "--sing-box-bin 需要参数" >&2; exit 2; }
            sing_box_override=$2
            shift 2
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            echo "未知参数：$1" >&2
            usage
            exit 2
            ;;
    esac
done

case "$component" in
    all|core|https) ;;
    *) echo "--component 只能是 all、core 或 https" >&2; exit 2 ;;
esac

if [ "$home_override_set" -eq 1 ]; then
    app_dir=$(sbmgr_resolve_home "$script_dir" "$home_override")
else
    app_dir=$(sbmgr_resolve_home "$script_dir")
fi

sing_box_bin=
case "$component" in
    all|core)
        if [ -n "$sing_box_override" ]; then
            sing_box_bin=$(sbmgr_resolve_executable "$sing_box_override")
        else
            sing_box_bin=$(sbmgr_resolve_executable sing-box)
        fi
        ;;
esac

if [ -n "$output_dir" ]; then
    case "$output_dir" in
        /*) ;;
        *) echo "--output-dir 必须是绝对路径" >&2; exit 2 ;;
    esac
    unit_root=$output_dir
    run_systemctl=0
else
    [ "$(id -u)" -eq 0 ] || {
        echo "安装 systemd unit 必须以 root 运行" >&2
        exit 1
    }
    unit_root=/etc/systemd/system
    run_systemctl=1
fi

render_root=$(mktemp -d "${TMPDIR:-/tmp}/sbmgr-units.XXXXXX")
unit_backup=
unit_manifest=
unit_transaction=0
preserve_unit_backup=0

valid_unit_destination() {
    case "$1" in
        sbmgr.service|sing-box.service.d/sbmgr.conf|sbmgr-ip-cert-renew.service|sbmgr-ip-cert-renew.timer) return 0 ;;
        *) echo "拒绝未知 systemd 目标：$1" >&2; return 1 ;;
    esac
}

restore_units() {
    [ -n "$unit_manifest" ] && [ -f "$unit_manifest" ] || return 1
    restore_status=0
    while IFS='|' read -r previous_type relative_path; do
        valid_unit_destination "$relative_path" || { restore_status=1; continue; }
        destination_path="$unit_root/$relative_path"
        case "$previous_type" in
            file)
                install -d -m 0755 "$(dirname -- "$destination_path")" || restore_status=1
                install -m 0644 "$unit_backup/$relative_path" "$destination_path" || restore_status=1
                ;;
            link)
                install -d -m 0755 "$(dirname -- "$destination_path")" || restore_status=1
                rm -f -- "$destination_path" || restore_status=1
                cp -P -- "$unit_backup/$relative_path" "$destination_path" || restore_status=1
                ;;
            absent) rm -f -- "$destination_path" || restore_status=1 ;;
            *) echo "非法 unit 备份清单：$previous_type|$relative_path" >&2; restore_status=1 ;;
        esac
    done <"$unit_manifest"
    systemctl daemon-reload >/dev/null 2>&1 || restore_status=1
    return "$restore_status"
}

cleanup() {
    status=$?
    trap - EXIT HUP INT TERM
    if [ "$unit_transaction" -eq 1 ]; then
        if ! restore_units; then
            echo "systemd unit 安装失败，且自动恢复未完全成功；服务未被启动" >&2
            echo "原 unit 和清单保留在：$unit_backup" >&2
            preserve_unit_backup=1
        fi
    fi
    rm -rf -- "$render_root"
    if [ -n "$unit_backup" ] && [ "$preserve_unit_backup" -eq 0 ]; then
        rm -rf -- "$unit_backup"
    fi
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM

render_unit() {
    source_name=$1
    destination=$2
    valid_unit_destination "$destination"
    source_path="$script_dir/$source_name"
    destination_path="$render_root/$destination"
    [ -f "$source_path" ] || {
        echo "缺少 unit 模板：$source_path" >&2
        exit 1
    }
    install -d -m 0755 "$(dirname -- "$destination_path")"
    sed \
        -e "s|@SBMGR_HOME@|$app_dir|g" \
        -e "s|@SING_BOX_BIN@|$sing_box_bin|g" \
        "$source_path" >"$destination_path"
    if grep -Eq '@[A-Z][A-Z_]*@' "$destination_path"; then
        echo "unit 模板仍含未替换变量：$source_path" >&2
        exit 1
    fi
    chmod 0644 "$destination_path"
}

case "$component" in
    all|core)
        render_unit sbmgr.service.in sbmgr.service
        render_unit sing-box-sbmgr.conf.in sing-box.service.d/sbmgr.conf
        ;;
esac
case "$component" in
    all|https)
        render_unit sbmgr-ip-cert-renew.service.in sbmgr-ip-cert-renew.service
        render_unit sbmgr-ip-cert-renew.timer sbmgr-ip-cert-renew.timer
        ;;
esac

install_rendered_unit() {
    relative_path=$1
    valid_unit_destination "$relative_path"
    source_path="$render_root/$relative_path"
    [ -f "$source_path" ] || return 0
    destination_path="$unit_root/$relative_path"
    if [ "$run_systemctl" -eq 1 ]; then
        if [ -L "$destination_path" ]; then
            install -d -m 0700 "$(dirname -- "$unit_backup/$relative_path")"
            cp -P -- "$destination_path" "$unit_backup/$relative_path"
            printf 'link|%s\n' "$relative_path" >>"$unit_manifest"
            rm -f -- "$destination_path"
        elif [ -e "$destination_path" ]; then
            [ -f "$destination_path" ] || { echo "unit 目标不是普通文件：$destination_path" >&2; exit 1; }
            install -d -m 0700 "$(dirname -- "$unit_backup/$relative_path")"
            install -m 0600 "$destination_path" "$unit_backup/$relative_path"
            printf 'file|%s\n' "$relative_path" >>"$unit_manifest"
        else
            printf 'absent|%s\n' "$relative_path" >>"$unit_manifest"
        fi
    fi
    install -d -m 0755 "$(dirname -- "$destination_path")"
    install -m 0644 "$source_path" "$destination_path"
}

if [ "$run_systemctl" -eq 1 ]; then
    install -d -m 0700 "$app_dir"
    sbmgr_assert_root_trusted_path "$app_dir"
    case "$component" in
        all|core) sbmgr_ensure_subscription_account ;;
    esac
    unit_backup=$(mktemp -d "$app_dir/.systemd-unit-backup.XXXXXX")
    chmod 0700 "$unit_backup"
    unit_manifest="$unit_backup/MANIFEST"
    : >"$unit_manifest"
    chmod 0600 "$unit_manifest"
    unit_transaction=1
fi

install_rendered_unit sbmgr.service
install_rendered_unit sing-box.service.d/sbmgr.conf
install_rendered_unit sbmgr-ip-cert-renew.service
install_rendered_unit sbmgr-ip-cert-renew.timer

if [ "$run_systemctl" -eq 1 ]; then
    systemctl daemon-reload
    case "$component" in
        all|core) sbmgr_assert_core_systemd_layout "$app_dir" "$sing_box_bin" ;;
    esac
    case "$component" in
        all|https) sbmgr_assert_https_systemd_layout "$app_dir" ;;
    esac
    case "$component" in
        all|core)
            legacy_backup=
            for legacy_name in sbmgr-sync.service sbmgr-sync.timer; do
                legacy_path="/etc/systemd/system/$legacy_name"
                if [ -e "$legacy_path" ] || [ -L "$legacy_path" ]; then
                    if [ -z "$legacy_backup" ]; then
                        legacy_stamp=$(date -u +%Y%m%dT%H%M%SZ)
                        legacy_backup="$app_dir/backups/systemd/legacy-sync-$legacy_stamp"
                        install -d -m 0700 "$legacy_backup"
                    fi
                    cp -P -- "$legacy_path" "$legacy_backup/$legacy_name"
                    if [ ! -L "$legacy_backup/$legacy_name" ]; then
                        chmod 0600 "$legacy_backup/$legacy_name"
                    fi
                fi
            done
            systemctl disable --now sbmgr-sync.timer >/dev/null 2>&1 || true
            systemctl stop sbmgr-sync.service >/dev/null 2>&1 || true
            if systemctl is-active --quiet sbmgr-sync.timer \
                || systemctl is-active --quiet sbmgr-sync.service; then
                echo "旧 sbmgr-sync 写入器仍在运行，拒绝完成 unit 安装" >&2
                exit 1
            fi
            if systemctl is-enabled --quiet sbmgr-sync.timer; then
                echo "旧 sbmgr-sync.timer 仍处于启用状态" >&2
                exit 1
            fi
            rm -f -- /etc/systemd/system/sbmgr-sync.service /etc/systemd/system/sbmgr-sync.timer
            systemctl daemon-reload
            if [ -n "$legacy_backup" ]; then
                echo "旧 sbmgr-sync unit 已停用，原文件备份在 $legacy_backup"
            fi
            ;;
    esac
    unit_transaction=0
    echo "systemd unit 已安装并验证，SBMGR_HOME=$app_dir，sing-box=$sing_box_bin"
else
    echo "systemd unit 已渲染到 $unit_root，SBMGR_HOME=$app_dir"
fi

trap - EXIT HUP INT TERM
rm -rf -- "$render_root"
if [ -n "$unit_backup" ]; then
    rm -rf -- "$unit_backup"
fi
