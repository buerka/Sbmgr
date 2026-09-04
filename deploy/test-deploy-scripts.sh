#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd -P)
# shellcheck source=path-lib.sh
. "$script_dir/path-lib.sh"

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

assert_rejected() {
    rejected_value=$1
    if sbmgr_resolve_home "$script_dir" "$rejected_value" >/dev/null 2>&1; then
        fail "本应拒绝安装目录：$rejected_value"
    fi
}

for shell_script in "$script_dir"/*.sh; do
    sh -n "$shell_script"
done

unset SBMGR_HOME
[ "$(sbmgr_resolve_home "$script_dir")" = "$repo_root" ] || \
    fail "默认目录没有从 deploy/ 的父目录推导"
[ "$(sbmgr_resolve_home "$script_dir" /srv/sbmgr)" = /srv/sbmgr ] || \
    fail "显式绝对目录解析错误"
SBMGR_HOME=/var/tmp/sbmgr
export SBMGR_HOME
[ "$(sbmgr_resolve_home "$script_dir")" = /var/tmp/sbmgr ] || \
    fail "SBMGR_HOME 覆盖没有生效"
unset SBMGR_HOME

assert_rejected ""
assert_rejected "relative/path"
assert_rejected "/"
assert_rejected "/root"
assert_rejected "/root/.ssh/sbmgr"
assert_rejected "/home/operator/.ssh/sbmgr"
assert_rejected "/etc/sbmgr"
assert_rejected "/usr/local/sbmgr"
assert_rejected "/proc/1/root/sbmgr"
assert_rejected "/tmp/sbmgr"
assert_rejected "/srv/../etc/sbmgr"
assert_rejected "/srv/sbmgr/"
assert_rejected "/srv/sbmgr unsafe"

test_root=$(mktemp -d "${TMPDIR:-/tmp}/sbmgr-deploy-test.XXXXXX")
transaction_root=$(mktemp -d "/var/tmp/sbmgr-deploy-transaction.XXXXXX")
trap 'rm -rf -- "$test_root" "$transaction_root"' EXIT HUP INT TERM
ln -s /etc "$test_root/etc-link"
assert_rejected "$test_root/etc-link/sbmgr"

mkdir -p "$test_root/app" "$test_root/units"
printf '{}\n' >"$test_root/app/state.json"
[ "$(sbmgr_select_state_file "$test_root/app")" = "$test_root/app/state.json" ] || \
    fail "旧版 JSON 状态选择错误"
: >"$test_root/app/state.db"
[ "$(sbmgr_select_state_file "$test_root/app")" = "$test_root/app/state.db" ] || \
    fail "存在 SQLite 时没有优先选择 state.db"

trusted_true=$(sbmgr_resolve_executable /bin/true)
sh "$script_dir/install-systemd.sh" \
    --home /srv/sbmgr \
    --component all \
    --sing-box-bin /bin/true \
    --output-dir "$test_root/units" >/dev/null

for rendered_unit in \
    "$test_root/units/sbmgr.service" \
    "$test_root/units/sbmgr-ip-cert-renew.service" \
    "$test_root/units/sbmgr-ip-cert-renew.timer" \
    "$test_root/units/sing-box.service.d/sbmgr.conf"; do
    [ -f "$rendered_unit" ] || fail "缺少渲染结果：$rendered_unit"
    if grep -Eq '@[A-Z][A-Z_]*@' "$rendered_unit"; then
        fail "unit 仍包含未解析占位符：$rendered_unit"
    fi
done

grep -Fq 'Environment=SBMGR_HOME=/srv/sbmgr' "$test_root/units/sbmgr.service" || \
    fail "主服务未写入绝对 SBMGR_HOME"
grep -Fq 'ExecStart=/srv/sbmgr/sbmgr daemon' "$test_root/units/sbmgr.service" || \
    fail "主服务 ExecStart 路径错误"
grep -Fq 'ExecStart=/srv/sbmgr/deploy/renew-ip-https.sh --home /srv/sbmgr' \
    "$test_root/units/sbmgr-ip-cert-renew.service" || \
    fail "续期服务 ExecStart 路径错误"
grep -Fq "ExecStart=$trusted_true -D /var/lib/sing-box -c /srv/sbmgr/sing-box.json run" \
    "$test_root/units/sing-box.service.d/sbmgr.conf" || \
    fail "sing-box 可执行文件或配置路径错误"

mock_bin="$transaction_root/mock-bin"
mock_control="$transaction_root/control"
mkdir -p "$mock_bin" "$mock_control"

real_id=$(command -v id)
real_stat=$(command -v stat)
export SBMGR_TEST_REAL_ID="$real_id"
export SBMGR_TEST_REAL_STAT="$real_stat"

cat >"$mock_bin/id" <<'SH'
#!/bin/sh
if [ "${1:-}" = -u ]; then
    printf '0\n'
    exit 0
fi
exec "$SBMGR_TEST_REAL_ID" "$@"
SH
chmod 0700 "$mock_bin/id"

cat >"$mock_bin/stat" <<'SH'
#!/bin/sh
# Transaction fixtures model a root-owned, non-writable production tree even
# when this self-test runs as an unprivileged GitHub Actions user.
if [ "${1:-}" = -c ] && [ "${2:-}" = '%u %a' ]; then
    printf '%s\n' "${SBMGR_TEST_STAT_RESULT:-0 755}"
    exit 0
fi
exec "$SBMGR_TEST_REAL_STAT" "$@"
SH
chmod 0700 "$mock_bin/stat"

cat >"$mock_bin/systemctl" <<'SH'
#!/bin/sh
printf 'systemctl|%s\n' "$*" >>"$SBMGR_TEST_EVENTS"
command_name=${1:-}
shift || true
case "$command_name" in
    show)
        property=
        unit=
        for argument in "$@"; do
            case "$argument" in
                --property=*) property=${argument#--property=} ;;
                --value) ;;
                *) unit=$argument ;;
            esac
        done
        case "$unit:$property" in
            sbmgr.service:WorkingDirectory) printf '%s\n' "${SBMGR_TEST_SYSTEMD_HOME:-$SBMGR_TEST_APP}" ;;
            sbmgr.service:Environment) printf 'SBMGR_HOME=%s\n' "$SBMGR_TEST_APP" ;;
            sbmgr.service:ExecStart)
                printf '{ path=%s/sbmgr ; argv[]=%s/sbmgr daemon ; ignore_errors=no ; }\n' "$SBMGR_TEST_APP" "$SBMGR_TEST_APP"
                ;;
            sing-box.service:ExecStart)
                printf '{ path=%s ; argv[]=%s -D /var/lib/sing-box -c %s/sing-box.json run ; ignore_errors=no ; }\n' \
                    "$SBMGR_TEST_SING_BOX" "$SBMGR_TEST_SING_BOX" "$SBMGR_TEST_APP"
                ;;
            sbmgr-ip-cert-renew.service:Environment) printf 'SBMGR_HOME=%s\n' "$SBMGR_TEST_APP" ;;
            sbmgr-ip-cert-renew.service:ExecStart)
                printf '{ path=%s/deploy/renew-ip-https.sh ; argv[]=%s/deploy/renew-ip-https.sh --home %s ; ignore_errors=no ; }\n' \
                    "$SBMGR_TEST_APP" "$SBMGR_TEST_APP" "$SBMGR_TEST_APP"
                ;;
            *) exit 1 ;;
        esac
        ;;
    is-active)
        unit=
        for argument in "$@"; do
            case "$argument" in --*) ;; *) unit=$argument ;; esac
        done
        state_file="$SBMGR_TEST_CONTROL/$(printf '%s' "$unit" | tr '/.' '__')"
        [ "$(cat "$state_file" 2>/dev/null || true)" = active ]
        ;;
    stop)
        for unit in "$@"; do
            case "$unit" in --*) continue ;; esac
            printf 'inactive\n' >"$SBMGR_TEST_CONTROL/$(printf '%s' "$unit" | tr '/.' '__')"
        done
        ;;
    start)
        for unit in "$@"; do
            case "$unit" in --*) continue ;; esac
            printf 'active\n' >"$SBMGR_TEST_CONTROL/$(printf '%s' "$unit" | tr '/.' '__')"
        done
        ;;
    disable)
        for unit in "$@"; do
            case "$unit" in --*) continue ;; esac
            printf 'inactive\n' >"$SBMGR_TEST_CONTROL/$(printf '%s' "$unit" | tr '/.' '__')"
        done
        ;;
    is-enabled) exit 1 ;;
    daemon-reload) ;;
    *) ;;
esac
SH
chmod 0700 "$mock_bin/systemctl"

cat >"$mock_bin/sing-box" <<'SH'
#!/bin/sh
exit 0
SH
chmod 0700 "$mock_bin/sing-box"

cat >"$mock_bin/pgrep" <<'SH'
#!/bin/sh
# The transactional fixtures intentionally have no independently running CUI.
exit 1
SH
chmod 0700 "$mock_bin/pgrep"

make_old_binary() {
    destination=$1
    cat >"$destination" <<'SH'
#!/bin/sh
if [ "${1:-}" = version ]; then
    echo 'sbmgr 0.22.0'
    exit 0
fi
exit 0
SH
    chmod 0700 "$destination"
}

make_candidate() {
    destination=$1
    cat >"$destination" <<'SH'
#!/bin/sh
if [ "${1:-}" = version ]; then
    echo 'sbmgr 0.23.0'
    exit 0
fi
if [ "${1:-}" != --state ] || [ "$#" -lt 3 ]; then
    exit 2
fi
state_path=$2
shift 2
printf 'candidate|%s|%s\n' "$state_path" "$*" >>"$SBMGR_TEST_EVENTS"
if [ ! -f "$state_path" ]; then
    state_directory=$(dirname -- "$state_path")
    if [ -f "$state_directory/state.json" ]; then
        cp "$state_directory/state.json" "$state_path"
        printf '{"migrated":true}\n' >"$state_directory/state.json.migrated"
    else
        exit 3
    fi
fi
if [ "$state_path" = "$SBMGR_TEST_APP/state.db" ] && [ "$*" = 'admin check' ]; then
    live_count_file="$SBMGR_TEST_CONTROL/live-check-count"
    live_count=$(cat "$live_count_file" 2>/dev/null || echo 0)
    live_count=$((live_count + 1))
    printf '%s\n' "$live_count" >"$live_count_file"
    if [ "${SBMGR_TEST_FAIL_LIVE_CHECK:-0}" -eq 1 ] && [ "$live_count" -ge 2 ]; then
        if [ "${SBMGR_TEST_CORRUPT_SNAPSHOT:-0}" -eq 1 ]; then
            snapshot=$(find "$SBMGR_TEST_APP/backups/state-config" -mindepth 1 -maxdepth 1 -type d -name 'pre-*' | head -n 1)
            printf 'tampered\n' >>"$snapshot/state.json"
        fi
        exit 42
    fi
fi
exit 0
SH
    chmod 0700 "$destination"
}

reset_transaction_fixture() {
    case_name=$1
    app="$transaction_root/$case_name/app"
    rm -rf -- "$transaction_root/$case_name"
    mkdir -p "$app/deploy"
    make_old_binary "$app/sbmgr"
    make_candidate "$app/.sbmgr-release.candidate"
    printf 'original-json-state\n' >"$app/state.json"
    printf 'ephemeral-shm\n' >"$app/state.db-shm"
    printf '{"base":true}\n' >"$app/config.base.json"
    printf '{"runtime":true}\n' >"$app/sing-box.json"
    events="$transaction_root/$case_name/events"
    : >"$events"
    rm -rf -- "$mock_control"
    mkdir -p "$mock_control"
    printf 'active\n' >"$mock_control/sbmgr_service"
    printf 'active\n' >"$mock_control/sing-box_service"
    export SBMGR_TEST_APP="$app"
    export SBMGR_TEST_EVENTS="$events"
    export SBMGR_TEST_CONTROL="$mock_control"
    export SBMGR_TEST_SING_BOX="$mock_bin/sing-box"
    checksum=$(sha256sum "$app/.sbmgr-release.candidate" | awk '{print $1}')
}

assert_transaction_order() {
    events_file=$1
    app_path=$2
    stop_line=$(grep -n '^systemctl|stop sbmgr.service$' "$events_file" | head -n 1 | cut -d: -f1)
    first_candidate_line=$(grep -n '^candidate|' "$events_file" | head -n 1 | cut -d: -f1)
    live_line=$(grep -n "^candidate|$app_path/state.db|admin check$" "$events_file" | head -n 1 | cut -d: -f1)
    start_line=$(grep -n '^systemctl|start sbmgr.service$' "$events_file" | tail -n 1 | cut -d: -f1)
    [ -n "$stop_line" ] && [ -n "$first_candidate_line" ] && [ -n "$live_line" ] && [ -n "$start_line" ] || \
        fail "部署事件不完整"
    [ "$stop_line" -lt "$first_candidate_line" ] || fail "候选程序在停止 daemon 前接触了状态"
    [ "$first_candidate_line" -lt "$live_line" ] || fail "候选程序没有先在影子库预检"
    [ "$live_line" -lt "$start_line" ] || fail "live 迁移未在启动服务前完成"
}

old_path=$PATH
PATH="$mock_bin:$PATH"
export PATH

SBMGR_TEST_STAT_RESULT='1000 755'
export SBMGR_TEST_STAT_RESULT
if sbmgr_assert_root_trusted_path "$transaction_root" >/dev/null 2>&1; then
    fail "root 服务目录不应允许非 root 所有者"
fi
SBMGR_TEST_STAT_RESULT='0 777'
export SBMGR_TEST_STAT_RESULT
if sbmgr_assert_root_trusted_path "$transaction_root" >/dev/null 2>&1; then
    fail "root 服务目录不应允许组/其他用户写入"
fi
SBMGR_TEST_STAT_RESULT='0 755'
export SBMGR_TEST_STAT_RESULT
sbmgr_assert_root_trusted_path "$transaction_root" || fail "受信 root 目录被误拒绝"

SBMGR_TEST_STAT_RESULT='1000 755'
export SBMGR_TEST_STAT_RESULT
if sbmgr_resolve_executable "$mock_bin/sing-box" >/dev/null 2>&1; then
    fail "显式用户目录中的 sing-box 不应被 root 服务接受"
fi
if sbmgr_resolve_executable sing-box >/dev/null 2>&1; then
    fail "PATH 命中的不可信 sing-box 不应被 root 服务接受"
fi
SBMGR_TEST_STAT_RESULT='0 755'
export SBMGR_TEST_STAT_RESULT
trusted_sing_box=$(sbmgr_resolve_executable "$mock_bin/sing-box")
[ -n "$trusted_sing_box" ] && [ -x "$trusted_sing_box" ] || \
    fail "受信 sing-box 可执行文件被误拒绝"

reset_transaction_fixture success
SBMGR_TEST_FAIL_LIVE_CHECK=0 SBMGR_TEST_CORRUPT_SNAPSHOT=0 \
    sh "$script_dir/deploy-release.sh" --home "$app" --sing-box-bin "$mock_bin/sing-box" "$checksum" >/dev/null
[ "$("$app/sbmgr" version)" = 'sbmgr 0.23.0' ] || fail "成功部署后程序版本不正确"
[ -s "$app/state.db" ] || fail "成功部署未完成 JSON → SQLite 迁移"
[ -s "$app/state.json.migrated" ] || fail "成功迁移未写入防陈旧回灌标记"
[ -f "$app/state.lock" ] || fail "部署未使用目录级 state.lock"
[ ! -e "$app/state.db.lock" ] || fail "部署不应使用已废弃的 state.db.lock"
snapshot=$(find "$app/backups/state-config" -mindepth 1 -maxdepth 1 -type d -name 'pre-*' | head -n 1)
[ -n "$snapshot" ] || fail "成功部署没有状态快照"
[ ! -e "$snapshot/state.db-shm" ] || fail "快照不应保存 state.db-shm"
(cd "$snapshot" && sha256sum -c SHA256SUMS >/dev/null) || fail "成功快照校验失败"
assert_transaction_order "$events" "$app"

reset_transaction_fixture rollback
if SBMGR_TEST_FAIL_LIVE_CHECK=1 SBMGR_TEST_CORRUPT_SNAPSHOT=0 \
    sh "$script_dir/deploy-release.sh" --home "$app" "$checksum" >/dev/null 2>&1; then
    fail "live 校验失败时部署本应失败"
fi
[ "$("$app/sbmgr" version)" = 'sbmgr 0.22.0' ] || fail "完整快照回滚未恢复旧程序"
[ "$(cat "$app/state.json")" = original-json-state ] || fail "完整快照回滚未恢复 JSON"
[ ! -e "$app/state.db" ] || fail "完整快照回滚遗留迁移数据库"
[ ! -e "$app/state.json.migrated" ] || fail "JSON 回滚遗留迁移标记"
[ ! -e "$app/state.db-shm" ] || fail "完整快照回滚不应恢复 shm"
[ "$(cat "$mock_control/sbmgr_service")" = active ] || fail "成功回滚后 sbmgr 未恢复"
[ "$(cat "$mock_control/sing-box_service")" = active ] || fail "成功回滚后 sing-box 未恢复"

reset_transaction_fixture sqlite-rollback
rm -f "$app/state.json"
printf 'original-sqlite-db\n' >"$app/state.db"
printf 'original-sqlite-wal\n' >"$app/state.db-wal"
printf 'original-migration-marker\n' >"$app/state.json.migrated"
checksum=$(sha256sum "$app/.sbmgr-release.candidate" | awk '{print $1}')
if SBMGR_TEST_FAIL_LIVE_CHECK=1 SBMGR_TEST_CORRUPT_SNAPSHOT=0 \
    sh "$script_dir/deploy-release.sh" --home "$app" "$checksum" >/dev/null 2>&1; then
    fail "SQLite live 校验失败时部署本应失败"
fi
[ "$(cat "$app/state.db")" = original-sqlite-db ] || fail "SQLite 回滚未恢复主数据库"
[ "$(cat "$app/state.db-wal")" = original-sqlite-wal ] || fail "SQLite 回滚未恢复 WAL"
[ "$(cat "$app/state.json.migrated")" = original-migration-marker ] || fail "SQLite 回滚未恢复迁移标记"
[ ! -e "$app/state.db-shm" ] || fail "SQLite 回滚不应恢复 shm"

reset_transaction_fixture corrupt-rollback
if SBMGR_TEST_FAIL_LIVE_CHECK=1 SBMGR_TEST_CORRUPT_SNAPSHOT=1 \
    sh "$script_dir/deploy-release.sh" --home "$app" "$checksum" >/dev/null 2>&1; then
    fail "损坏快照场景本应部署失败"
fi
[ -s "$app/state.db" ] || fail "快照校验失败前不应删除 live 数据库"
[ "$(cat "$mock_control/sbmgr_service")" = inactive ] || fail "回滚校验失败后 sbmgr 必须保持停止"
[ "$(cat "$mock_control/sing-box_service")" = inactive ] || fail "回滚校验失败后 sing-box 必须保持停止"
find "$app" -maxdepth 1 -type f -name '.sbmgr-deploy-rollback.*' | grep -q . || \
    fail "回滚校验失败后应保留部署前程序"

reset_transaction_fixture unit-mismatch
if SBMGR_TEST_SYSTEMD_HOME=/wrong/home \
    sh "$script_dir/deploy-release.sh" --home "$app" "$checksum" >/dev/null 2>&1; then
    fail "systemd 有效目录不匹配时部署本应拒绝"
fi
if grep -q '^systemctl|stop sbmgr.service$' "$events"; then
    fail "systemd 校验失败后不应进入停服阶段"
fi
[ ! -e "$app/state.db" ] || fail "systemd 校验失败不应触发迁移"
[ "$("$app/sbmgr" version)" = 'sbmgr 0.22.0' ] || fail "systemd 校验失败不应替换程序"

PATH=$old_path
export PATH

echo "deploy 脚本自检通过"
