#!/bin/sh
set -eu

usage() {
    echo "用法: $0 PUBLIC_IP [HTTPS_PORT] [EMAIL]" >&2
    echo "示例: $0 203.0.113.10 8443 admin@example.com" >&2
    exit 2
}

public_ip=${1:-}
https_port=${2:-8443}
email=${3:-}
[ -n "$public_ip" ] || usage
[ "$(id -u)" -eq 0 ] || {
    echo "必须以 root 运行此脚本" >&2
    exit 1
}

url_host=$(python3 - "$public_ip" "$https_port" <<'PY'
import ipaddress
import sys

address = ipaddress.ip_address(sys.argv[1])
port = int(sys.argv[2])
if not 1024 <= port <= 65535:
    raise SystemExit("HTTPS_PORT 必须是 1024-65535 的高位端口")
print(f"[{address}]" if address.version == 6 else address)
PY
)
case "$public_ip" in
    *:*) listen_host='[::]' ;;
    *) listen_host='0.0.0.0' ;;
esac

app_dir=/root/sbmgr
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
venv="$app_dir/certbot-venv"
certbot="$venv/bin/certbot"
config_dir="$app_dir/letsencrypt"
work_dir="$app_dir/letsencrypt-work"
logs_dir="$app_dir/logs/certbot"
certificate_dir="$config_dir/live/$public_ip"
renew_source="$script_dir/renew-ip-https.sh"
renew_target="$app_dir/deploy/renew-ip-https.sh"

if [ ! -x "$app_dir/sbmgr" ] || [ ! -f "$app_dir/state.json" ]; then
    echo "找不到 $app_dir/sbmgr 或 state.json" >&2
    exit 1
fi
if ss -ltnH "sport = :80" | grep -q .; then
    echo "端口 80 已被占用；Certbot standalone 无法完成 HTTP-01 验证" >&2
    exit 1
fi
if ss -ltnpH "sport = :$https_port" | grep -q . \
    && ! ss -ltnpH "sport = :$https_port" | grep -q '"sbmgr"'; then
    echo "HTTPS 端口 $https_port 已被其他程序占用" >&2
    exit 1
fi

install -d -m 0700 "$app_dir" "$app_dir/deploy" "$config_dir" "$work_dir" "$logs_dir"
if [ "$renew_source" != "$renew_target" ]; then
    install -m 0700 "$renew_source" "$renew_target"
else
    chmod 0700 "$renew_target"
fi
if [ ! -x "$certbot" ]; then
    if ! python3 -m venv "$venv"; then
        if ! command -v apt-get >/dev/null 2>&1; then
            echo "当前系统缺少 Python venv 支持；请先安装对应的 python3-venv 软件包" >&2
            exit 1
        fi
        echo "正在安装 Certbot 所需的 python3-venv 软件包"
        apt-get update
        apt-get install -y python3-venv
        python3 -m venv "$venv"
    fi
    "$venv/bin/pip" install --disable-pip-version-check --no-cache-dir 'certbot>=5.4,<6'
fi

certbot_version=$($certbot --version 2>&1 | awk '{print $2}')
"$venv/bin/python" - "$certbot_version" <<'PY'
import sys

parts = tuple(int(x) for x in sys.argv[1].split(".")[:2])
if parts < (5, 4):
    raise SystemExit("需要 Certbot 5.4 或更高版本")
PY

set -- \
    --non-interactive \
    --agree-tos \
    --keep-until-expiring \
    --preferred-profile shortlived \
    --standalone \
    --preferred-challenges http \
    --ip-address "$public_ip" \
    --cert-name "$public_ip" \
    --config-dir "$config_dir" \
    --work-dir "$work_dir" \
    --logs-dir "$logs_dir"
if [ -n "$email" ]; then
    "$certbot" certonly "$@" --email "$email"
else
    "$certbot" certonly "$@" --register-unsafely-without-email
fi

if [ ! -r "$certificate_dir/fullchain.pem" ] || [ ! -r "$certificate_dir/privkey.pem" ]; then
    echo "证书已签发但预期文件不存在：$certificate_dir" >&2
    exit 1
fi

"$venv/bin/python" - "$certificate_dir/fullchain.pem" "$public_ip" <<'PY'
import datetime
import ipaddress
import sys

from cryptography import x509

certificate = x509.load_pem_x509_certificate(open(sys.argv[1], "rb").read())
expected = ipaddress.ip_address(sys.argv[2])
try:
    addresses = certificate.extensions.get_extension_for_class(
        x509.SubjectAlternativeName
    ).value.get_values_for_type(x509.IPAddress)
except x509.ExtensionNotFound:
    addresses = []
if expected not in addresses:
    raise SystemExit(f"签发的证书不包含预期 IP SAN：{expected}")
now = datetime.datetime.now(datetime.timezone.utc)
if not certificate.not_valid_before_utc <= now < certificate.not_valid_after_utc:
    raise SystemExit("签发的证书当前不在有效期内")
PY

install -m 0644 "$script_dir/sbmgr-ip-cert-renew.service" /etc/systemd/system/sbmgr-ip-cert-renew.service
install -m 0644 "$script_dir/sbmgr-ip-cert-renew.timer" /etc/systemd/system/sbmgr-ip-cert-renew.timer

"$app_dir/sbmgr" --state "$app_dir/state.json" admin subscription set \
    --enabled true \
    --listen "$listen_host:$https_port" \
    --base-url "https://$url_host:$https_port" \
    --tls-cert "$certificate_dir/fullchain.pem" \
    --tls-key "$certificate_dir/privkey.pem" \
    --restart

systemctl daemon-reload
systemctl enable --now sbmgr-ip-cert-renew.timer

echo "订阅 HTTPS 已启用：https://$url_host:$https_port"
echo "短期 IP 证书由 sbmgr-ip-cert-renew.timer 自动检查续期。"
