# 订阅服务

## 交付模型

每台设备拥有独立的高熵订阅 token。服务提供两类资源：

```text
https://<PUBLIC_HOST>:<SUBSCRIPTION_PORT>/sub/<TOKEN>
https://<PUBLIC_HOST>:<SUBSCRIPTION_PORT>/qr/<TOKEN>.png
```

订阅返回该设备的 Mihomo YAML；二维码编码同一订阅地址。轮换 token 后，旧地址立即失效。

## 进程与权限

`sbmgr.service` 中的 root 守护进程负责业务状态、nftables 和服务管理；HTTP/TLS 与二维码处理在独立的 `sbmgr-subscription` 账号进程中运行。安装 core unit 时创建无登录 shell、无 home、无附加组的专用账号。数据库、WAL、模板、日志和备份仍仅管理员可读写，不向该账号开放应用目录。

守护进程 exec 同一版本的程序，通过匿名 Unix socketpair 及继承的监听描述符启动工作进程。子进程从内部通道接收本次监听所需的 TLS 材料，在接受连接之前，对所有 Go 线程清除附加组、切换真实/有效/保存 UID 与 GID、清空 capabilities 并设置 `NoNewPrivileges`。未完成降权就退出，不回退为 root HTTP。Linux 正式构建必须使用 `CGO_ENABLED=0`，以保证跨线程降权；标准发布脚本已设置。

内部请求只允许“设备订阅、用量头、二维码 URL”三种只读操作和一个 token，最大 129 字节。没有路径、SQL、状态写入或管理命令接口。root 端独立执行 token 查询、即时可用性检查及每分钟 600 次的总预算；该预算跨工作进程重启保留。单次串行处理，响应 YAML 最大 4 MiB，编码后的内部响应最大 6 MiB，部分请求帧与响应写入限时 5 秒。HTTP 进程最多并发 4 个请求，只获得当前设备的输出，不获得其他设备或完整状态。

父进程退出或内部通道损坏时关闭 HTTP 监听。工作进程异常退出后，守护进程每 5 秒尝试重新启动，后台统计和策略维护继续运行。首次启动因账号、证书或降权条件不满足而失败时，修复后重启 `sbmgr.service`。更改监听、证书或启用设置也需要重启；CUI 保存设置会自动重启，无需“应用配置”。token 撤销和用户禁用无需重启。

升级时先运行 `deploy/install-systemd.sh --home <SBMGR_HOME> --component core`，创建专用账号并更新 capability 边界，再按部署流程更新程序。主服务增加的 `CAP_SETUID` / `CAP_SETGID` 仅供工作进程启动降权使用；HTTP 处理阶段不保留这些能力。证书续期后仍通过重启主服务更换工作进程内的证书，私钥文件不需要改变权限。

## 用量信息

成功响应包含兼容 Mihomo 面板的用量元数据：

- `Subscription-Userinfo`
- `Profile-Update-Interval`
- `Profile-Title`

`Subscription-Userinfo` 根据用户选择的配额计量口径生成，使客户端进度与服务端配额策略保持一致。静态 YAML 导出没有动态 HTTP 响应头。

下载文件名包含用户、设备和生成时间；`Profile-Title` 保持稳定，避免客户端把每次更新识别为不同订阅。

## TLS

回环监听可使用 HTTP，由同机反向代理终止 TLS。非回环监听必须配置证书和私钥，公开基础 URL 必须使用 HTTPS。订阅端口可以使用独立高位端口，不要求与代理入站共用端口。

项目提供 IP 地址证书辅助脚本：

```bash
./deploy/setup-ip-https.sh \
  --home "<SBMGR_HOME>" \
  "<PUBLIC_IP>" \
  "<SUBSCRIPTION_PORT>"
```

联系邮箱可以作为最后一个可选参数传入。证书签发和续期需要满足 ACME 服务的当前要求。部署前应确认地址所有权、验证端口、防火墙和证书客户端兼容性。

## 反向代理

sbmgr 只按实际 TCP 对端限流，不信任 `X-Real-IP` 或 `X-Forwarded-For`，包括回环连接。同机反向代理后的客户端共享回环来源预算，代理层应另行按真实客户端限流，且不得记录完整订阅 URL。

订阅端点包含访问凭据，应启用 TLS、限制日志可见范围，并避免在公开页面、截图或 issue 中出现完整 URL。

## 缓存与速率限制

订阅响应禁止缓存；实际来源每分钟 60 次、全局每分钟 600 次、来源表最多 4096 条。频率限制用于降低 token 探测和误用风险，不替代防火墙、TLS 或 token 轮换。
