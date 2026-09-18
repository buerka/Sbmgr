# 独立 DataImpulse SOCKS5 封装

`scripts/dataimpulse_gateway.py` 是独立 Python 3.11+ 服务，不依赖 sbmgr 的状态、用户或主从协议。需要 curl。示例 unit 为 `deploy/dataimpulse-gateway.service`，默认安装在 `/srv/dataimpulse-gateway`。

私有 `config.json` 由部署方提供，权限 0600：`login`、`password` 为现有套餐凭据，`local_password` 为随机生成的本机接入密码，`listen_ports` 为三个不重复端口（默认 31001–31003）。程序仅监听 `127.0.0.1`，SOCKS5 用户名为 `sbmgr`。sbmgr 只导入这三个普通 SOCKS5 出站，并在对应末跳引用它们；套餐凭据不进入 sbmgr。

上游始终使用 `__cr.gh;sessttl.120`、SOCKS5 和端口型 Sticky 会话。启动与运行时通过 HTTPS 公网 IP 接口检查三个会话，必须全部成功、均为 GH 且同批 IP 不重复，才发布可用状态。失败或重复时停止提供代理、关闭旧连接，并有界尝试替代端口；选择后再次并发确认。每分钟复查一次，状态文件权限 0600，日志只输出可用数量和原因，不输出账号、密码或出口 IP。

120 分钟是传给供应商的参数，不是本程序作出的保持时间保证；供应商可能在两次采样之间更换 IP。因此能证明的是成功采样时三路不同，无法保证未来任意时刻永不重复。此封装只支持 TCP CONNECT，明确拒绝 UDP ASSOCIATE 和 BIND，不会降级为直连。

用户只需为本机三个 SOCKS5 出站建立 sbmgr 线路并授权给设备。安装位置、真实主机、凭据、运行状态和测试结果属于部署资料，不进入源码仓库。

供应商套餐、真实主机、出口采样和运行状态均属于部署方的私有验收资料；公开仓库只保留不含凭据的封装源码和安装示例。
