#!/bin/sh
set -eu

[ "$(id -u)" -eq 0 ] || {
    echo "必须以 root 运行此脚本" >&2
    exit 1
}

app_dir=/root/sbmgr
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
