#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
# shellcheck source=path-lib.sh
. "$script_dir/path-lib.sh"

home_override_set=0
home_override=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --home)
            [ "$#" -ge 2 ] || {
                echo "--home 需要一个绝对路径" >&2
                exit 2
            }
            home_override_set=1
            home_override=$2
            shift 2
            ;;
        --help|-h)
            echo "用法: $0 [--home 绝对安装目录]" >&2
            exit 0
            ;;
        *)
            echo "未知参数：$1" >&2
            exit 2
            ;;
    esac
done

[ "$(id -u)" -eq 0 ] || {
    echo "必须以 root 运行此脚本" >&2
    exit 1
}

if [ "$home_override_set" -eq 1 ]; then
    app_dir=$(sbmgr_resolve_home "$script_dir" "$home_override")
else
    app_dir=$(sbmgr_resolve_home "$script_dir")
fi
sbmgr_assert_root_trusted_path "$app_dir"
certbot="$app_dir/certbot-venv/bin/certbot"
config_dir="$app_dir/letsencrypt"
work_dir="$app_dir/letsencrypt-work"
logs_dir="$app_dir/logs/certbot"

if [ ! -x "$certbot" ]; then
    echo "Certbot 未安装在 $certbot" >&2
    exit 1
fi

exec "$certbot" renew \
    --quiet \
    --config-dir "$config_dir" \
    --work-dir "$work_dir" \
    --logs-dir "$logs_dir" \
    --deploy-hook "/bin/systemctl try-restart sbmgr.service"
