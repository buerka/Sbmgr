# 第二版安全审计修复记录

基线：`ba295c1`；范围：2026-09-05 的第二版审计报告和 GitHub #1–#33。报告中的内容作为待复核发现处理，不作为执行命令。以下记录描述源码修复，不代表已经部署或关闭 GitHub issue。

## 逐项对应

| Issue | 报告项 | 修复与验证入口 |
| --- | --- | --- |
| #1 | SBM-01 | 访问日志按完整事件语法解析，身份只取固定位置；伪造目标不能归属其他用户。`TestSecurityJournalEventsCannotForgeAttribution` |
| #2 | SBM-03 | token 格式预检、SQLite 索引 EXISTS 查询后才载入业务状态；未知 token 不读取历史表。`TestSecurityUnknownTokenDoesNotLoadFullSQLiteState` |
| #3 | SBM-02 | 限制目标地址语法和长度；终端输出过滤控制字符、OSC/DCS/非 SGR CSI 与 bidi。`TestSecurityLogTargetsRejectTerminalControlsAndMalformedHosts` |
| #4 | SBM-04 | 只按 RemoteAddr 限流，回环连接也不信任 X-Real-IP/转发头。订阅来源回归测试 |
| #5 | SBM-05 | nft 注释和计数键改为固定长度 mark；旧规则按实际 mark 迁移，拒绝含斜杠/控制字符或超长管理名称。`TestSecurityMarkKeysPreventLongNamesAndLabelCollisions` |
| #6 | SBM-11 | 来源日志必须完整匹配，来源和访问事件互斥，拒绝目标内的伪造来源。日志归属回归测试 |
| #7 | SBM-12 | 关闭事件锚定完整语法，访问目标中的关闭文本不删除连接。日志归属回归测试 |
| #8 | SBM-13 | 保留所有入站的未托管 name/username，渲染前再检查基础模板身份冲突。`TestSecurityImportRetainsOtherIdentityRoutesAndRejectsCollision` |
| #9 | SBM-14 | 导入只移除已导入身份的简单路由，保留混合规则中的其他身份和复杂规则。导入回归测试 |
| #10 | SBM-15 | HUP/restart 后要求连续服务活跃和配置入站可连接，失败进入原配置及 nft 回滚。`TestSecurityReloadHealthFailureRollsBackAppliedConfig`（Linux） |
| #11 | SBM-16 | 限流表最多 4096 个来源，有全局预算；拒绝请求不回写计数。`TestSecuritySubscriptionLimiterIsBounded` |
| #12 | SBM-17 | daemon 的 Webhook、出站探测、Fleet 在锁外执行；重新加锁合并仍有效的结果，保留并发管理修改。`TestSecurityNetworkMaintenanceReleasesStateLockAndPreservesEdits` |
| #13 | SBM-18 | 最近访问采用瞬时 map 索引；维护周期统一裁剪，显示时排序副本；相同目标更新不随历史长度线性增长。`BenchmarkSecurityRecentAccessHotTarget` |
| #14 | SBM-19 | 活跃连接最多 4096 条，用索引堆淘汰最旧记录，重复更新不扩大堆。`TestSecurityConnectionTrackingBoundAndMigration` |
| #15 | SBM-29 | 持久保存绑定活动时间，应用/重启不重置换绑宽限；auto 绑定闲置 24 小时释放。`TestSecurityIPGraceSurvivesRestartAndAutoSlotsExpire` |
| #16 | SBM-30 | 并发处罚保存 10 分钟恢复时间，后台恢复并应用；手动禁用清除自动恢复标记。`TestSecurityConnectionBlockRecoversWithoutEnablingManualDisable` |
| #17 | SBM-06 | gRPC 1.82.1、x/net 0.56.0、x/text 0.39.0，构建工具链 Go 1.27.1；CI 增加 govulncheck、CodeQL、Dependabot |
| #18 | SBM-07 | atomicWrite 新建父目录为 0700，文件继续 0600。`TestSecurityConfigSymlinkAndPrivateParent`（Linux） |
| #19 | SBM-09 | 主服务补充系统调用、设备、进程可见性、地址族、主机名和 W^X 限制。采用报告的短期加固方案，HTTP 进程拆分仍是后续架构工作 |
| #20 | SBM-20 | 订阅内部错误返回通用 500/503，避免路径和模板诊断进入公网响应。订阅错误响应回归测试 |
| #21 | SBM-21 | QR、普通订阅和 HEAD 共用用户/设备可用性检查。禁用与 token 撤销回归测试 |
| #22 | SBM-22 | 审计参数改用安全字段白名单，未知参数值及 URL 脱敏；Webhook 投递错误不保留 URL。`TestSecurityDefaultViewsAndAuditHideCredentials` |
| #23 | SBM-23 | 默认详情、设备与订阅页面隐藏 UUID、auth_user、token；Webhook 字段使用秘密输入。显式二维码仍属于凭据分发，页面提示不可分享截图。默认视图回归测试 |
| #24 | SBM-24 | realpath 解析后重新进行路径字符白名单校验。`deploy/test-deploy-scripts.sh` 中的恶意符号链接目标测试 |
| #25 | SBM-25 | 续期服务能力收敛和 sandbox；Certbot 5.8.0 及传递依赖固定版本/官方 wheel 哈希，pip 强制哈希且禁止源码构建 |
| #26 | SBM-26 | Actions 固定完整 commit SHA；扫描不再豁免敏感类型的 example 文件或二进制；标签构建生成 provenance。`scripts/test_check_public_tree.py` |
| #27 | SBM-27 | 基础/运行配置拒绝同路径、规范化别名、符号链接和硬链接。路径回归测试及 Linux 符号链接测试 |
| #28 | SBM-31 | 容量解析拒绝 NaN、Inf、溢出和非零小于一字节的输入，避免变成无限配额。数值边界回归测试 |
| #29 | SBM-32 | mark 分配耗尽返回可处理错误；批量预检容量后才分配，不 panic、不部分保存。mark 容量回归测试 |
| #30 | SBM-33 | 非空但损坏的 BlockedUntil 拒绝载入/保存；运行判定按仍封禁处理。损坏状态回归测试 |
| #31 | SBM-08 | HTTP server 明确 TLS 最低版本 1.2。订阅 TLS 回归测试 |
| #32 | SBM-10 | SSH 参数 POSIX 引号、整体超时和 WaitDelay；提供不执行客户端命令的 Fleet 强制只读入口。Fleet 测试和部署脚本回归 |
| #33 | SBM-28 | Service 拒绝选项和控制字符；SingBoxBin 限定绝对路径或安全 basename；StatsAPI 只允许本机回环或绝对 Unix socket。运行设置回归测试 |

## 升级行为

- 业务状态版本升至 10，SQLite schema 升至 2；前向迁移新增订阅索引及策略活动时间。状态与配置备份继续保留，原 JSON 迁移输入不删除。旧程序不能直接读取新版本数据库；软件回退须使用部署前一致性快照。
- 新配置校验可能拒绝旧数据里的非法名称、token、封禁时间或模板别名。部署前用影子数据库预检并修正，不能手改 SQLite 绕过业务哈希。
- 订阅限额：实际来源每分钟 60 次、全局每分钟 600 次、最多同时处理 4 个请求。超过限制返回 429/503。同机反向代理共享来源限额，代理层需另设真实客户端限流。token 不做长期缓存，撤销后下一次查询立即失效。
- 最近访问保留原有 1000 条限制；连接跟踪超过 4096 条时淘汰旧记录。连接数是日志推断值，在达到上限或日志丢失时可能低估，不能作为内核级精确并发防火墙。
- 动态单活默认 60 秒宽限（可设 1–3600 秒）。auto 绑定连续 24 小时无活动释放；手动绑定不释放。后台应用失败继续保留待应用标记并重试。
- 并发禁用默认持续 10 分钟，恢复后仍受到期、配额和其他策略限制；再次超限可再次处罚。旧版设备禁用没有原因字段，迁移不推测其是否应启用，管理员需检查这类设备。
- Webhook 一轮最多 10 次、总时间预算 5 秒。锁外发送避免阻塞管理事务；外部投递采用重试语义，并发 daemon 或投递成功后进程崩溃仍可能重复通知，接收端应按告警身份去重。

## 验证与发布边界

本次本地验证包括：Windows 全量 Go 测试、go vet、Linux amd64 构建及 Linux 全量测试；部署脚本语法/事务自检（包括失败回滚）、仓库隐私扫描和扫描器回归；govulncheck；订阅 HTTP、迁移、并发编辑及 UI 回归。

最近访问热点基准：1 条记录约 85 ns/op，1000 条约 87 ns/op，均为 56 B/op、2 次分配。数值取决于机器，重点是历史长度增加后开销基本不变。

Linux 测试中的 systemctl、nft 和 sing-box 使用桩程序。未操作真实服务器，未执行真实 ACME 签发/续期，也未验证目标发行版的 systemd sandbox 兼容性。发布前必须在授权的目标环境验证 sbmgr、sing-box、订阅 HTTPS 和代理入站；服务 active 和 TCP 可连接不能替代完整代理协议测试。GitHub CodeQL、provenance 也需推送后由 CI 执行。

## 构建与依赖来源

- Go 漏洞扫描使用 [官方 govulncheck](https://go.dev/security/vuln/)。除报告点名版本外，x/net 继续升至扫描建议的 0.56.0。
- Certbot 锁文件中的 wheel SHA256 来自各版本的 PyPI JSON，适用于 Linux/Python 3.10+。更新须重新解析完整依赖集、核验 wheel 哈希并做测试；安装不会自动选择未审查的新版本。
- 标签 CI 生成 [GitHub artifact attestation](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations)。发布端应先用 `gh attestation verify <artifact> -R buerka/Sbmgr` 核对仓库、工作流与预期 tag，再计算并传入部署 SHA256。仅比较同一下载来源的 checksum 不构成独立来源验证。本地构建须从独立确认的 Git tag 构建。
