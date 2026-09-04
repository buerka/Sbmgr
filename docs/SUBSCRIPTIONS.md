# 订阅服务

## 交付模型

每台设备拥有独立的高熵订阅 token。服务提供两类资源：

```text
https://<PUBLIC_HOST>:<SUBSCRIPTION_PORT>/sub/<TOKEN>
https://<PUBLIC_HOST>:<SUBSCRIPTION_PORT>/qr/<TOKEN>.png
```

订阅返回该设备的 Mihomo YAML；二维码编码同一订阅地址。轮换 token 后，旧地址立即失效。

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

反向代理应覆盖写入客户端来源地址，并移除客户端自带的同名头。sbmgr 只在直接连接来自回环地址时信任受控的 `X-Real-IP`，不使用客户端可拼接的 `X-Forwarded-For` 作为限流身份。

订阅端点包含访问凭据，应启用 TLS、限制日志可见范围，并避免在公开页面、截图或 issue 中出现完整 URL。

## 缓存与速率限制

订阅响应禁止缓存，并按来源地址执行基础请求频率限制。频率限制用于降低 token 探测和误用风险，不替代防火墙、TLS 或 token 轮换。
