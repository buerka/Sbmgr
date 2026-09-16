# 主从管理与线路

主机管理用户、设备、配额和订阅，通过 SSH 下发从机执行计划。线路指定有序节点及每跳协议：前面的从机中转，末跳直接落地；同一从机可供多条线路使用。路径仅含主机标识时在主机落地（示例为 `master`）。

```text
客户端 → 主机入口 ── SOCKS5 → 从机 A ── WG → 从机 B → 目标
客户端 → 主机入口 ── HY2 → 从机 A → 目标
```

## 协议

| 值（别名） | 行为 |
| --- | --- |
| `socks`（`socks5`、`s5`） | 自动认证；TCP/UDP，节点间 UDP 使用 UoT；本身不加密 |
| `hysteria2`（`hy2`、`hy`） | 新线路默认；UDP 监听，自动生成并验证独立 TLS 证书，有效期一年 |
| `wireguard`（`wg`） | UDP 监听，逐连接密钥与用户态 endpoint；需 sing-box WireGuard/gVisor 支持 |

所有机器需兼容的 sbmgr、sing-box；HY2 需 QUIC 支持。主机 SSH 可达从机，相邻节点的数据端口互通，管理地址与数据地址可分别设置。目标机的 `sing-box check` 是应用前门禁。

已有 WG 服务可在“线路与服务器 → 新增 → 端点”导入 `wireguard` JSON，使用 `system: false` 并填写远端、地址和 peer 参数，再分配给设备、应用配置。共享端点经回环认证桥接保留独立计量/限速；内部端口从 48000 起避开配置中的监听，冲突按配置事务回滚。

## 接入与编排

主机先按[运维指南](OPERATIONS.md)初始化。“运维中心 → 主从管理”提供下列命令对应菜单；示例路径须替换为实际绝对路径。

主机登记并导出接入文件（0600，目标文件须不存在）：

```sh
sbmgr admin mesh init --id master --cluster default
sbmgr admin mesh add --id relay-a --host '<SSH_HOST>' --port 22 \
  --user root --key '<PRIVATE_SSH_KEY_PATH>' --home /srv/sbmgr
sbmgr admin mesh export --node relay-a --output '<ABSOLUTE_JOIN_FILE>'
```

从机加入并安装服务。全新目录自动生成最小回环基础配置和数据库，拒绝覆盖已有配置或身份：

```sh
export SBMGR_HOME=/srv/sbmgr
"$SBMGR_HOME/sbmgr" admin mesh join --file '<ABSOLUTE_JOIN_FILE>'
"$SBMGR_HOME/deploy/install-systemd.sh" --home "$SBMGR_HOME" --component core
```

管理公钥在从机 `authorized_keys` 中限制为：

```text
restrict,command="/srv/sbmgr/deploy/mesh-agent-rpc.sh --home /srv/sbmgr" <PUBLIC_KEY>
```

脚本权限 0700；程序、脚本、应用目录及父目录由 root 持有且不可被其他用户修改。提前核验主机指纹；客户端严格检查 known_hosts，不接受交互认证。固定入口忽略 `SSH_ORIGINAL_COMMAND`，只接受状态和 prepare/commit/rollback/finalize；消息 ≤2 MiB，校验协议版本、集合、身份和角色，不接收任意命令或完整配置。

主机编排并应用：

```sh
sbmgr admin mesh route --id near --hops relay-a --protocols hy2
sbmgr admin mesh route --id home --hops master
# 先登记 exit-b，再创建混合线路
sbmgr admin mesh route --id chain --hops relay-a,exit-b \
  --protocols socks,wg --endpoints '<RELAY_HOST>:21000,<EXIT_HOST>:22000'
sbmgr admin mesh check
sbmgr admin mesh apply
```

- 同标识修改线路；协议可填一个统一值或逐跳列表。省略保留原值，新连接默认 HY2；地址省略保留，新连接使用 SSH 主机名及从 20000 起的可用端口。
- 协议改变或 `route ... --rotate` 更新认证材料；HY2 证书过期可编辑/轮换，但拒绝应用。最多 128 节点、256 线路、每条 16 跳，禁止循环。
- 应用成功后，设备“分配节点”选择 `主从 · <线路标识>`，设置速率，再“应用配置”；从机执行 `systemctl enable --now sbmgr` 开启维护。
- 删除用 `remove-route --id <ROUTE_ID>` 或 `remove --id <MEMBER_ID>`。先迁移用户、消除引用并成功应用，使旧监听停用；删除登记不清理远端数据或 SSH 授权，闲置管理密钥需撤销。

## 事务与验收

编辑保存待应用修订。协调器持久记录恢复决定，逐机准备、校验并备份，先提交从机再切换主机，全部成功后确认结束。只下发本机所需计划和密钥；多机不保证同时切换，重启可能断开连接。

中断后未决事务阻止新拓扑变更；恢复通信后运行 `sbmgr admin mesh recover`。已有最终提交决定则继续确认，否则恢复旧计划；同一事务可幂等重试，业务备份保留。

`mesh check` 只验管理通道和修订。实际验收须覆盖线路 TCP/UDP、出口、订阅、停用、配额及双向限速；本机协议测试见[开发验证](DEVELOPMENT.md)。只读 Fleet 巡检设置见[运维指南](OPERATIONS.md)。
