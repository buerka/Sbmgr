# 订阅服务

## 交付

设备独立 token，对应实时 Mihomo YAML 和同地址二维码：

```text
https://<PUBLIC_HOST>:<PORT>/sub/<TOKEN>
https://<PUBLIC_HOST>:<PORT>/qr/<TOKEN>.png
```

Web 的“订阅交付”页按设备下载订阅地址或 YAML。普通页面不回显 token、UUID 或完整代理 JSON；地址文件与 YAML 均为凭据，下载后应私下交付。原终端剪贴板和二维码界面已移除；公开 `/qr/` 服务仍保留。

交付前重新读取凭据并检查可用性。token 轮换立即撤销旧地址；默认界面隐藏 token，二维码和交付文件均属凭据。

响应禁止缓存。成功响应含 `Subscription-Userinfo`（按配额口径）、`Profile-Update-Interval`、稳定的 `Profile-Title`；文件名带用户、设备和时间。静态 YAML 没有动态响应头。

## 运行契约

| 项目 | 约束 |
| --- | --- |
| 进程 | root daemon 管状态；同版本 exec 的专用 `sbmgr-subscription` 进程处理 HTTP/TLS/二维码，不继承业务堆或状态文件 |
| 隔离 | 专用账号无登录 shell、home、附加组；所有线程先切换真实/有效/保存 UID/GID、清空附加组及全部 capabilities、设置 NoNewPrivileges，再接收请求；失败关闭 HTTP |
| 构建/安装 | Linux 必须 `CGO_ENABLED=0`；安装 core unit 创建账号并配置启动降权能力，私有目录不向工作进程开放 |
| 通道 | 匿名 Unix socketpair + 继承监听描述符；TLS 材料经 IPC 提供，只允许单设备订阅、用量头、二维码 URL 查询 |
| 请求/响应 | 请求仅操作码与 token，≤129 字节；YAML ≤4 MiB，编码响应 ≤6 MiB；半帧读取和响应写入超时 5 秒 |
| root 查询 | token 格式/索引预检，再即时校验设备；串行处理，独立 600 次/分钟预算跨子进程重启保留；无路径、SQL、写入或管理命令接口 |
| HTTP 预算 | 实际来源 60 次/分钟、全局 600 次/分钟、来源表最多 4096 条、并发最多 4；超限返回 429/503 |
| 故障 | 父进程退出或通道损坏即关闭监听；子进程异常每 5 秒重试，后台维护继续；首次启动失败需修复后重启服务 |
| 设置变化 | 监听、证书、开关变化需重启 `sbmgr.service`，可运行 `sbmgr service restart`；token 撤销、用户禁用无需重启 |

升级先安装 core unit，详见[运维指南](OPERATIONS.md)。主服务的 SETUID/SETGID 与 `AmbientCapabilities=CAP_SETUID` 用于启动降权；处理 HTTP 时 capabilities 必须全为零，不应放宽 sandbox。构建与边界检查见[开发验证](DEVELOPMENT.md)。

## HTTPS 与反向代理

非回环监听要求 TLS 1.2+，公开 URL 必须 HTTPS；回环可用 HTTP 并由同机代理终止 TLS。限流只认实际 TCP 对端，不信任转发头；反向代理客户端共享来源预算，代理需按真实客户端另行限流并避免记录完整 URL。

IP 证书辅助入口：

```sh
./deploy/setup-ip-https.sh --home '<SBMGR_HOME>' '<PUBLIC_IP>' '<PORT>'
```

末尾可附联系邮箱；签发条件见脚本。证书续期后重启主服务，无需放宽私钥权限。
