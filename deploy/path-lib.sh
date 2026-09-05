#!/bin/sh

# Shared path validation for deployment helpers. This file is sourced by the
# scripts in this directory; it deliberately has no executable side effects.

sbmgr_path_error() {
    echo "$*" >&2
    return 1
}

sbmgr_resolve_home() {
    if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
        sbmgr_path_error "sbmgr_resolve_home: 参数数量不正确"
        return 2
    fi

    sbmgr_script_dir=$1
    if [ "$#" -eq 2 ]; then
        sbmgr_home_input=$2
        [ -n "$sbmgr_home_input" ] || {
            sbmgr_path_error "SBMGR_HOME / --home 不能为空"
            return 2
        }
    elif [ "${SBMGR_HOME+x}" = x ]; then
        sbmgr_home_input=$SBMGR_HOME
        [ -n "$sbmgr_home_input" ] || {
            sbmgr_path_error "SBMGR_HOME 不能为空"
            return 2
        }
    else
        sbmgr_home_input=$(CDPATH= cd -- "$sbmgr_script_dir/.." && pwd -P)
    fi

    case "$sbmgr_home_input" in
        /*) ;;
        *)
            sbmgr_path_error "安装目录必须是绝对路径：$sbmgr_home_input"
            return 2
            ;;
    esac
    case "$sbmgr_home_input" in
        *[!A-Za-z0-9_./-]*)
            sbmgr_path_error "安装目录含不安全字符（仅允许字母、数字、_、-、. 和 /）：$sbmgr_home_input"
            return 2
            ;;
        */)
            sbmgr_path_error "安装目录末尾不要带 /：$sbmgr_home_input"
            return 2
            ;;
    esac
    sbmgr_home_inner=${sbmgr_home_input#/}
    case "/$sbmgr_home_inner/" in
        *//*|*/./*|*/../*)
            sbmgr_path_error "安装目录不能包含空、. 或 .. 路径段：$sbmgr_home_input"
            return 2
            ;;
    esac

    sbmgr_realpath_probe=
    if command -v realpath >/dev/null 2>&1; then
        sbmgr_realpath_probe=$(realpath -m /__sbmgr_missing__/.. 2>/dev/null || true)
    fi
    if [ "$sbmgr_realpath_probe" = / ]; then
        sbmgr_home_resolved=$(realpath -m "$sbmgr_home_input") || return 2
    elif [ -d "$sbmgr_home_input" ]; then
        sbmgr_home_resolved=$(CDPATH= cd -- "$sbmgr_home_input" && pwd -P)
    else
        sbmgr_home_parent=$(dirname -- "$sbmgr_home_input")
        sbmgr_home_leaf=$(basename -- "$sbmgr_home_input")
        [ -d "$sbmgr_home_parent" ] || {
            sbmgr_path_error "系统缺少 realpath，且安装目录的父目录不存在：$sbmgr_home_parent"
            return 2
        }
        sbmgr_parent_resolved=$(CDPATH= cd -- "$sbmgr_home_parent" && pwd -P)
        case "$sbmgr_parent_resolved" in
            /) sbmgr_home_resolved=/$sbmgr_home_leaf ;;
            *) sbmgr_home_resolved=$sbmgr_parent_resolved/$sbmgr_home_leaf ;;
        esac
    fi

    case "$sbmgr_home_resolved" in
        *[!A-Za-z0-9_./-]*)
            sbmgr_path_error "解析后的安装目录含不安全字符"
            return 2
            ;;
        /|/bin|/bin/*|/boot|/boot/*|/dev|/dev/*|/etc|/etc/*|/home|/lib|/lib/*|/lib64|/lib64/*|/opt|/proc|/proc/*|/root|/root/.ssh|/root/.ssh/*|/root/.gnupg|/root/.gnupg/*|/run|/run/*|/sbin|/sbin/*|/srv|/sys|/sys/*|/tmp|/tmp/*|/usr|/usr/*|/var|/home/*/.ssh|/home/*/.ssh/*|/home/*/.gnupg|/home/*/.gnupg/*)
            sbmgr_path_error "拒绝把系统根目录、顶层目录或敏感系统目录作为安装目录：$sbmgr_home_resolved"
            return 2
            ;;
    esac
    sbmgr_resolved_inner=${sbmgr_home_resolved#/}
    case "$sbmgr_resolved_inner" in
        */*) ;;
        *)
            sbmgr_path_error "拒绝把根目录下的一级目录作为安装目录：$sbmgr_home_resolved"
            return 2
            ;;
    esac

    printf '%s\n' "$sbmgr_home_resolved"
}

sbmgr_resolve_executable() {
    if [ "$#" -ne 1 ] || [ -z "$1" ]; then
        sbmgr_path_error "可执行文件不能为空"
        return 2
    fi
    sbmgr_executable_input=$1
    case "$sbmgr_executable_input" in
        */*) sbmgr_executable=$sbmgr_executable_input ;;
        *) sbmgr_executable=$(command -v "$sbmgr_executable_input" 2>/dev/null || true) ;;
    esac
    case "$sbmgr_executable" in
        /*) ;;
        *)
            sbmgr_path_error "找不到绝对可执行路径：$sbmgr_executable_input"
            return 1
            ;;
    esac
    case "$sbmgr_executable" in
        *[!A-Za-z0-9_./+-]*)
            sbmgr_path_error "可执行路径含 systemd 模板不支持的字符：$sbmgr_executable"
            return 2
            ;;
    esac
    if [ ! -f "$sbmgr_executable" ] || [ ! -x "$sbmgr_executable" ]; then
        sbmgr_path_error "不可执行：$sbmgr_executable"
        return 1
    fi

    # A root systemd unit must not retain a user-controlled symlink or PATH
    # spelling. Resolve the complete existing chain first; if the platform's
    # realpath cannot do that, accept only a non-symlink leaf below a
    # physically resolved directory.
    sbmgr_executable_resolved=
    if command -v realpath >/dev/null 2>&1; then
        sbmgr_executable_resolved=$(realpath -e "$sbmgr_executable" 2>/dev/null || true)
    fi
    if [ -z "$sbmgr_executable_resolved" ]; then
        [ ! -L "$sbmgr_executable" ] || {
            sbmgr_path_error "系统无法安全解析可执行文件符号链接：$sbmgr_executable"
            return 1
        }
        sbmgr_executable_dir=$(dirname -- "$sbmgr_executable")
        sbmgr_executable_leaf=$(basename -- "$sbmgr_executable")
        sbmgr_executable_dir=$(CDPATH= cd -- "$sbmgr_executable_dir" && pwd -P) || return 1
        case "$sbmgr_executable_dir" in
            /) sbmgr_executable_resolved=/$sbmgr_executable_leaf ;;
            *) sbmgr_executable_resolved=$sbmgr_executable_dir/$sbmgr_executable_leaf ;;
        esac
    fi
    case "$sbmgr_executable_resolved" in
        /*) ;;
        *)
            sbmgr_path_error "无法把可执行文件解析为绝对路径：$sbmgr_executable"
            return 1
            ;;
    esac
    case "$sbmgr_executable_resolved" in
        *[!A-Za-z0-9_./+-]*)
            sbmgr_path_error "解析后的可执行路径含不安全字符：$sbmgr_executable_resolved"
            return 2
            ;;
    esac
    if [ ! -f "$sbmgr_executable_resolved" ] || [ ! -x "$sbmgr_executable_resolved" ]; then
        sbmgr_path_error "解析后的路径不可执行：$sbmgr_executable_resolved"
        return 1
    fi
    sbmgr_assert_root_trusted_path "$sbmgr_executable_resolved" || return 1
    printf '%s\n' "$sbmgr_executable_resolved"
}

# Root-run systemd units execute code from SBMGR_HOME. Every existing path
# component therefore has to be root-owned and non-writable by group/others;
# otherwise an ordinary account could replace the directory and gain code
# execution when the service restarts.
sbmgr_assert_root_trusted_path() {
    if [ "$#" -ne 1 ] || [ -z "$1" ]; then
        sbmgr_path_error "sbmgr_assert_root_trusted_path: 参数不正确"
        return 2
    fi
    sbmgr_trusted_path=$1
    case "$sbmgr_trusted_path" in
        /*) ;;
        *) sbmgr_path_error "受信路径必须是绝对路径：$sbmgr_trusted_path"; return 2 ;;
    esac

    while [ ! -e "$sbmgr_trusted_path" ]; do
        sbmgr_trusted_parent=$(dirname -- "$sbmgr_trusted_path")
        [ "$sbmgr_trusted_parent" != "$sbmgr_trusted_path" ] || {
            sbmgr_path_error "无法找到安装目录的已存在父目录"
            return 1
        }
        sbmgr_trusted_path=$sbmgr_trusted_parent
    done

    while :; do
        [ ! -L "$sbmgr_trusted_path" ] || {
            sbmgr_path_error "拒绝使用符号链接路径承载 root 服务：$sbmgr_trusted_path"
            return 1
        }
        sbmgr_trusted_stat=$(stat -c '%u %a' "$sbmgr_trusted_path" 2>/dev/null) || {
            sbmgr_path_error "无法检查路径所有权：$sbmgr_trusted_path"
            return 1
        }
        sbmgr_trusted_owner=${sbmgr_trusted_stat%% *}
        sbmgr_trusted_mode=${sbmgr_trusted_stat#* }
        case "$sbmgr_trusted_owner:$sbmgr_trusted_mode" in
            *[!0-9:]*|:|*:)
                sbmgr_path_error "路径所有权数据无效：$sbmgr_trusted_path"
                return 1
                ;;
        esac
        sbmgr_group_mode=$(( (sbmgr_trusted_mode / 10) % 10 ))
        sbmgr_other_mode=$(( sbmgr_trusted_mode % 10 ))
        if [ "$sbmgr_trusted_owner" -ne 0 ] \
            || [ $((sbmgr_group_mode & 2)) -ne 0 ] \
            || [ $((sbmgr_other_mode & 2)) -ne 0 ]; then
            sbmgr_path_error "root 服务路径必须由 root 持有且不允许组/其他用户写入：$sbmgr_trusted_path"
            return 1
        fi
        [ "$sbmgr_trusted_path" = / ] && break
        sbmgr_trusted_path=$(dirname -- "$sbmgr_trusted_path")
    done
}

sbmgr_systemd_value() {
    if [ "$#" -ne 2 ]; then
        sbmgr_path_error "sbmgr_systemd_value: 参数数量不正确"
        return 2
    fi
    systemctl show --value --property="$2" "$1"
}

sbmgr_assert_core_systemd_layout() {
    if [ "$#" -ne 2 ]; then
        sbmgr_path_error "sbmgr_assert_core_systemd_layout: 参数数量不正确"
        return 2
    fi
    sbmgr_systemd_home=$1
    sbmgr_systemd_sing_box=$2

    sbmgr_working_directory=$(sbmgr_systemd_value sbmgr.service WorkingDirectory) || return 1
    [ "$sbmgr_working_directory" = "$sbmgr_systemd_home" ] || {
        sbmgr_path_error "sbmgr.service WorkingDirectory 不匹配：$sbmgr_working_directory"
        return 1
    }
    sbmgr_environment=$(sbmgr_systemd_value sbmgr.service Environment) || return 1
    case " $sbmgr_environment " in
        *" SBMGR_HOME=$sbmgr_systemd_home "*) ;;
        *)
            sbmgr_path_error "sbmgr.service 未设置正确的 SBMGR_HOME=$sbmgr_systemd_home"
            return 1
            ;;
    esac
    sbmgr_exec_start=$(sbmgr_systemd_value sbmgr.service ExecStart) || return 1
    case "$sbmgr_exec_start" in
        *"path=$sbmgr_systemd_home/sbmgr"*"argv[]=$sbmgr_systemd_home/sbmgr daemon"*) ;;
        *)
            sbmgr_path_error "sbmgr.service 的有效 ExecStart 未指向 $sbmgr_systemd_home/sbmgr daemon"
            return 1
            ;;
    esac

    sbmgr_sing_box_exec=$(sbmgr_systemd_value sing-box.service ExecStart) || return 1
    case "$sbmgr_sing_box_exec" in
        *"path=$sbmgr_systemd_sing_box"*" -c $sbmgr_systemd_home/sing-box.json run"*) ;;
        *)
            sbmgr_path_error "sing-box.service 的有效 ExecStart 未使用 $sbmgr_systemd_home/sing-box.json"
            return 1
            ;;
    esac
}

sbmgr_assert_https_systemd_layout() {
    if [ "$#" -ne 1 ]; then
        sbmgr_path_error "sbmgr_assert_https_systemd_layout: 参数数量不正确"
        return 2
    fi
    sbmgr_https_home=$1
    sbmgr_https_environment=$(sbmgr_systemd_value sbmgr-ip-cert-renew.service Environment) || return 1
    case " $sbmgr_https_environment " in
        *" SBMGR_HOME=$sbmgr_https_home "*) ;;
        *)
            sbmgr_path_error "证书续期服务未设置正确的 SBMGR_HOME=$sbmgr_https_home"
            return 1
            ;;
    esac
    sbmgr_https_exec=$(sbmgr_systemd_value sbmgr-ip-cert-renew.service ExecStart) || return 1
    case "$sbmgr_https_exec" in
        *"path=$sbmgr_https_home/deploy/renew-ip-https.sh"*"argv[]=$sbmgr_https_home/deploy/renew-ip-https.sh --home $sbmgr_https_home"*) ;;
        *)
            sbmgr_path_error "证书续期服务的有效 ExecStart 未指向当前安装目录"
            return 1
            ;;
    esac
}

sbmgr_select_state_file() {
    if [ "$#" -ne 1 ]; then
        sbmgr_path_error "sbmgr_select_state_file: 参数数量不正确"
        return 2
    fi
    sbmgr_state_home=$1
    if [ -f "$sbmgr_state_home/state.db" ]; then
        printf '%s\n' "$sbmgr_state_home/state.db"
    elif [ -f "$sbmgr_state_home/state.json" ]; then
        printf '%s\n' "$sbmgr_state_home/state.json"
    else
        sbmgr_path_error "找不到 $sbmgr_state_home/state.db 或旧版 state.json"
        return 1
    fi
}
