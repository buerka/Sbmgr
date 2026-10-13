# 运维指南

## 初始化

应用目录由显式参数、`SBMGR_HOME` 或程序/脚本位置确定；生产目录、可执行文件及父目录须由 root 持有，且不可被其他用户写入。下列占位符由部署方填写。

```sh
export SBMGR_HOME=/absolute/path/to/sbmgr
"$SBMGR_HOME/sbmgr" admin init \
  --config '<SOURCE_CONFIG>' --base "$SBMGR_HOME/config.base.json" \
  --inbound '<VLESS_INBOUND_TAG>' --server '<PUBLIC_HOST>' \
  --public-key '<REALITY_PUBLIC_KEY>'
"$SBMGR_HOME/sbmgr" web configure --password-file /root/sbmgr-admin.secret
"$SBMGR_HOME/sbmgr" service install
"$SBMGR_HOME/sbmgr" service start
```

密码文件由部署方预先以 0600 权限创建，至少 12 字节。Web 默认仅监听回环，可通过 `ssh -N -L 9090:127.0.0.1:9090 root@<SERVER>` 访问 `http://127.0.0.1:9090`；公网访问见 [Web 管理](WEB_ADMIN.md)。

已有身份默认保持非托管，显式 `--import-users` 才导入。安装脚本生成实际路径的 systemd unit；unit 不保存业务数据。主从接入见 [MESH.md](MESH.md)，证书配置见[订阅服务](SUBSCRIPTIONS.md)。

## 文件与恢复

| 应用目录内容 | 用途 |
| --- | --- |
| `state.db`、sidecar、`state.lock` | SQLite 业务状态、跨进程互斥，仅管理员读写 |
| `state.analytics.db`、sidecar、`state.analytics.db.lock` | 独立网站历史与采集游标；不参与日常业务写前备份，仅管理员读写 |
| `state.db.migration.lock` | 业务 schema 升级互斥；不是数据文件，不随快照恢复 |
| `web-admin.json` | 本机 Web 监听、管理员密码摘要与 TLS 路径；0600，不复制到从机 |
| `config.base.json` / `sing-box.json` | 基础模板 / 生成的运行配置，必须是不同文件 |
| `mihomo.template.yaml`、`exports/` | 客户端母版、静态交付文件 |
| `audit.jsonl`、`logs/`、`.drafts/` | 脱敏操作审计、运行日志、私有编辑草稿 |
| `backups/` | 状态、基础模板、运行配置和限速快照 |

仅在数据库缺失且旧 `state.json` 未迁移时，锁内执行一次性导入；保留源文件和副本，成功写入 `state.json.migrated`。标记已存在时禁止因数据库丢失而回灌旧统计。

状态备份使用 SQLite 一致性快照，校验完整性与业务哈希；恢复先快照当前状态，保留损坏文件，再验证数据库、配置和限速规则。模板编辑前备份，配置应用遵循 [AGENTS.md](../AGENTS.md) 的事务顺序。

升级到业务 schema 12 时，旧库分析历史先完整留存到 `backups/state-before-analytics-<迁移标识>.db`，导入独立分析库并校验后才删除旧表，再一次性回收业务库空间。迁移中断可继续；源备份保留，需为源快照、分析库和空间回收准备足够磁盘。已经迁出历史却缺少分析库时会报错，不能通过新建空库掩盖历史丢失。

自动写前、每日和主从配置准备阶段的安全备份覆盖业务库。显式 `admin backup create` 同时生成 `state-manual-*.db` 与存在时的 `analytics-manual-*.db`，分别取一致性快照，保持相同账号与设备身份代次；分析快照失败则整次手动备份不宣告成功。页面单项大小仍指业务备份，目录总计包含分析配套文件。

`admin backup restore` 和页面恢复仅恢复业务状态；恢复会创建新的身份代次，旧代分析历史随后清理，不从配套文件回滚历史。若需保留完整历史，应停服后使用外部灾备流程原样恢复同一组业务库、分析库及各自 WAL/回滚日志，不能混搭不同时间或身份代次的文件，不能恢复 `-shm`。部署失败恢复已自动覆盖这组文件。分析库可用 `sbmgr admin analytics check` 独立检查格式、完整性、外键与身份镜像。

### 保留期限与空间

Web「系统运维 → 状态备份」显示每份状态备份的文件大小、到期时间，以及整个 `backups/` 目录的文件大小合计（包含配置、部署恢复副本和隔离资料，不跟随符号链接；不是文件系统块占用）。搜索、分页不影响总计。操作审计在独立页面显示最近 100 项记录，搜索与分页仅覆盖这些记录。

点击「设置保留期限」可启用按时间清理；也可使用参数化命令：

```sh
sbmgr admin backup retention --days 30
sbmgr admin backup retention --days 0  # 关闭按时间清理
```

默认及升级后均为 `0`（关闭），允许 `1–3650` 天。保存后下一轮后台维护生效（默认每分钟检查），无需应用代理配置；停服期间不清理，恢复运行后补查。期限按文件修改时间计算，每天为 24 小时。清理只针对自动命名的手动与每日状态备份，每类至少保留最新一份，不按备份份数删除。`state.previous.*`、迁移留存、配置备份、部署恢复目录及隔离资料不受本策略删除；带 SQLite sidecar 的备份暂缓清理。

过期手动备份的同名分析配套文件一起清理；任一文件存在 sidecar 或不是普通文件时暂缓，孤立的分析文件不单独按名称批量删除。

清理与状态读写、备份、恢复使用同一跨进程锁。删除不可撤销；成功删除会记录 `backup.cleanup` 审计，部分文件失败会报告错误并在下轮重试，不影响业务状态。保留策略属于业务状态，恢复旧备份也会恢复其中的策略，请恢复后核对。

## 发布与部署

1. 按[开发验证](DEVELOPMENT.md)从干净、已确认的 Git tag 构建。下载产物先核验来源：`gh attestation verify <artifact> -R buerka/Sbmgr`，确认仓库、工作流和预期 tag；同源 checksum 只能证明内容一致。
2. 将候选程序以 `.sbmgr-release.candidate` 放入应用目录，运行候选程序的 `service install --output-dir "$SBMGR_HOME/deploy"` 提取匹配版本的内嵌工具。它不安装 unit 或替换运行程序；确认来源后，更新 core unit 并执行外部部署事务：

   ```sh
   "$SBMGR_HOME/deploy/install-systemd.sh" --home "$SBMGR_HOME" --component core
   "$SBMGR_HOME/deploy/deploy-release.sh" --home "$SBMGR_HOME" '<SHA256_OR_CHECKSUM_FILE>'
   ```

   非默认 sing-box 路径可给两个脚本加 `--sing-box-bin /absolute/path/to/sing-box`。
3. 脚本停服，取得业务、兼容旧状态和分析锁，将两个数据库文件组与配置备份到 `backups/state-config/`（不设份数上限）；影子迁移/业务与分析校验、核对 unit 路径后替换并启动。稳定性门禁与业务自检失败则恢复旧程序、配对状态及配置。成功删除临时旧程序，保留业务备份。
4. 验证 `sbmgr version --verbose`、服务稳定性、订阅 HTTPS 和真实代理 TCP/UDP、出口、停用、配额及限速。远端验证须先获得目标与操作范围授权。

新校验可能拒绝旧名称、token、封禁时间或配置路径别名，须先影子预检，不能手改 SQLite 绕过哈希。旧程序未必能读新 schema；软件回退须重建目标 tag，并使用部署前匹配的一致性状态快照。

网站数据看板需使用支持连接关闭事件与实际上传、下载字节的 sing-box API。升级代理内核并验证本机回环事件接口后，可运行 `sbmgr admin analytics configure --enabled true --listen 127.0.0.1:9191` 启用采集。看板仅展示启用后的已采集区间，升级前和采集中断时的历史网站流量无法补录；覆盖状态会提示缺口。禁用采集不会把未知流量显示为零。

## 诊断与巡检

`systemctl status sbmgr sing-box` 查看服务，`journalctl -u sbmgr` 查看维护/应用错误；成功人工操作见 `audit.jsonl`。出口健康检查只证明端口可达，验收仍需实际协议请求。

`sbmgr_perf` 记录超过 1 秒或失败的阶段，`sbmgr_perf_summary` 在维护循环汇总进程启动以来的次数、错误、SQLite busy、取消、并发峰值与耗时分桶。阶段覆盖锁等待/持有、状态读写/哈希/备份/完整性检查、分析采集、后台维护、配置生成/校验/应用和 Web 任务。分桶上界依次为 1、10、50、100、250 毫秒及 1、5、20、30 秒，最后一桶为更长耗时；这是累计分布，不是滚动百分位。记录只含固定阶段名与数值，不含请求参数或凭据。

排查慢操作先区分 `lock.wait` 与 `lock.hold`，再看同时间的 `state.backup`、`state.integrity`、`config.reload` 等阶段。受理回执不代表保存完成；网络超时先查询原任务，服务重启导致记录丢失时核对当前设置，避免重复提交。

日志和 nft 只读采样的子进程最多执行 3 秒，退出后继承管道最多等 250 毫秒；标准输出最多 8 MiB，超限或超时不采用部分统计，也不推进日志游标。超过上限的历史积压会明确报错并等待处理，目前不自动分批排空。sing-box 配置检查、nft 检查/应用/恢复每步有独立 30 秒预算和 1 秒管道等待，失败仍走既有事务恢复；整体应用时间可能包含多步及回滚，不能把单步预算当作整体保证。

Fleet 通过严格 known_hosts、限时限量的非交互 SSH 获取只读快照。使用专用巡检密钥，远端 `authorized_keys` 示例：

```text
restrict,command="/srv/sbmgr/deploy/fleet-readonly-snapshot.sh --home /srv/sbmgr" <PUBLIC_KEY>
```

脚本 0700，与 `path-lib.sh` 同目录；程序及目录权限同初始化要求。入口忽略客户端命令，仅运行 `admin snapshot`。主从下发使用各自的管理授权。

Webhook 每轮最多 10 次、总预算 5 秒；重试可能重复，接收端按告警身份去重。所有运行资料留在受限部署目录。
