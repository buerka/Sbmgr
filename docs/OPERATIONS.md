# 运维指南

## 初始化

应用目录由显式参数、`SBMGR_HOME` 或程序/脚本位置确定；生产目录、可执行文件及父目录须由 root 持有，且不可被其他用户写入。下列占位符由部署方填写。

```sh
export SBMGR_HOME=/absolute/path/to/sbmgr
"$SBMGR_HOME/sbmgr" admin init \
  --config '<SOURCE_CONFIG>' --base "$SBMGR_HOME/config.base.json" \
  --inbound '<VLESS_INBOUND_TAG>' --server '<PUBLIC_HOST>' \
  --public-key '<REALITY_PUBLIC_KEY>'
"$SBMGR_HOME/deploy/install-systemd.sh" --home "$SBMGR_HOME" --component all
systemctl enable --now sbmgr
```

已有身份默认保持非托管，显式 `--import-users` 才导入。安装脚本生成实际路径的 systemd unit；unit 不保存业务数据。主从接入见 [MESH.md](MESH.md)，证书配置见[订阅服务](SUBSCRIPTIONS.md)。

## 文件与恢复

| 应用目录内容 | 用途 |
| --- | --- |
| `state.db`、sidecar、`state.lock` | SQLite 业务状态、跨进程互斥，仅管理员读写 |
| `config.base.json` / `sing-box.json` | 基础模板 / 生成的运行配置，必须是不同文件 |
| `mihomo.template.yaml`、`exports/` | 客户端母版、静态交付文件 |
| `audit.jsonl`、`logs/`、`.drafts/` | 脱敏操作审计、运行日志、私有编辑草稿 |
| `backups/` | 状态、基础模板、运行配置和限速快照 |

仅在数据库缺失且旧 `state.json` 未迁移时，锁内执行一次性导入；保留源文件和副本，成功写入 `state.json.migrated`。标记已存在时禁止因数据库丢失而回灌旧统计。

状态备份使用 SQLite 一致性快照，校验完整性与业务哈希；恢复先快照当前状态，保留损坏文件，再验证数据库、配置和限速规则。模板编辑前备份，配置应用遵循 [AGENTS.md](../AGENTS.md) 的事务顺序。

## 发布与部署

1. 按[开发验证](DEVELOPMENT.md)从干净、已确认的 Git tag 构建。下载产物先核验来源：`gh attestation verify <artifact> -R buerka/Sbmgr`，确认仓库、工作流和预期 tag；同源 checksum 只能证明内容一致。
2. 将匹配版本的程序与部署脚本放到应用目录；候选程序名为 `.sbmgr-release.candidate`。更新 core unit 后部署：

   ```sh
   "$SBMGR_HOME/deploy/install-systemd.sh" --home "$SBMGR_HOME" --component core
   "$SBMGR_HOME/deploy/deploy-release.sh" --home "$SBMGR_HOME" '<SHA256_OR_CHECKSUM_FILE>'
   ```

   非默认 sing-box 路径可给两个脚本加 `--sing-box-bin /absolute/path/to/sing-box`。
3. 脚本停服加锁，备份到 `backups/state-config/`（保留 20 组），影子迁移/校验、核对 unit 路径后替换并启动；稳定性门禁与业务自检失败则恢复旧程序、状态、配置。成功删除临时旧程序，保留业务备份。
4. 验证 `sbmgr version --verbose`、服务稳定性、订阅 HTTPS 和真实代理 TCP/UDP、出口、停用、配额及限速。远端验证须先获得目标与操作范围授权。

新校验可能拒绝旧名称、token、封禁时间或配置路径别名，须先影子预检，不能手改 SQLite 绕过哈希。旧程序未必能读新 schema；软件回退须重建目标 tag，并使用部署前匹配的一致性状态快照。

## 诊断与巡检

`systemctl status sbmgr sing-box` 查看服务，`journalctl -u sbmgr` 查看维护/应用错误；成功人工操作见 `audit.jsonl`。出口健康检查只证明端口可达，验收仍需实际协议请求。

Fleet 通过严格 known_hosts、限时限量的非交互 SSH 获取只读快照。使用专用巡检密钥，远端 `authorized_keys` 示例：

```text
restrict,command="/srv/sbmgr/deploy/fleet-readonly-snapshot.sh --home /srv/sbmgr" <PUBLIC_KEY>
```

脚本 0700，与 `path-lib.sh` 同目录；程序及目录权限同初始化要求。入口忽略客户端命令，仅运行 `admin snapshot`。主从下发使用各自的管理授权。

Webhook 每轮最多 10 次、总预算 5 秒；重试可能重复，接收端按告警身份去重。所有运行资料留在受限部署目录。
