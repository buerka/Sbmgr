# 内嵌 Web 管理

`sbmgr` 包含页面、API、后台维护和部署脚本。前端采用 React 18、TypeScript、shadcn/ui / Radix UI / Tailwind CSS 4、Redux Toolkit 和 React Router，使用 Vite / SWC 构建。界面采用 Shadcn Admin 的布局、Inter 字体和明暗主题，保留上游 MIT 声明。构建后的页面、脚本、样式和字体使用 Go `embed`，无运行时 Node.js、CDN 或单独的资源目录。生产运行仍需 Linux、sing-box、systemd、nftables 和 conntrack；这些是代理运行环境，不由管理程序下载或更新。

## 首次启动

按 [运维指南](OPERATIONS.md) 用 `admin init` 导入基础配置。软件及安装目录由 root 持有，其他用户不可写。以下命令从程序目录运行：

```sh
# 预先在 /root/sbmgr-admin.secret 写入至少 12 字节密码，权限 0600。
./sbmgr web configure --password-file /root/sbmgr-admin.secret
./sbmgr service install
./sbmgr service start
```

`service install` 从二进制提取匹配版本的部署工具，创建低权限 HTTP 账号并安装 core systemd unit。无需另行复制脚本。若使用 `--home /srv/sbmgr`，在目标中不存在程序时复制当前二进制；已存在其他程序则拒绝覆盖，版本替换走外部部署事务。`--sing-box-bin /absolute/path/to/sing-box` 可指定代理内核。

默认账号为 `admin`，监听 `127.0.0.1:9090`。本地电脑建立 SSH 转发：

```sh
ssh -N -L 9090:127.0.0.1:9090 root@<SERVER>
```

打开 `http://127.0.0.1:9090`。地址必须与配置的 origin 一致，不能混用 `localhost` 与 `127.0.0.1`。`./sbmgr web status` 查看地址；`./sbmgr service status` 查看运行状态。

不安装 systemd 时，`./sbmgr` 与 `./sbmgr serve` 均前台运行 Web 和维护。Linux 首次运行仍须先安装 HTTP 服务账号。Windows 的 `serve --web-only` 仅用于回环开发预览，不执行生产网络维护。

## HTTPS 与反向代理

直接提供 HTTPS：

```sh
./sbmgr web configure --password-file /root/sbmgr-admin.secret \
  --listen 0.0.0.0:9443 --origin https://admin.example:9443 \
  --tls-cert /root/certs/fullchain.pem --tls-key /root/certs/privkey.pem
./sbmgr service restart
```

使用现有反向代理时监听回环，origin 指定浏览器使用的 HTTPS 地址：

```sh
./sbmgr web configure --password-file /root/sbmgr-admin.secret \
  --listen 127.0.0.1:9090 --origin https://admin.example
./sbmgr service restart
```

代理须保留 `Host` 和 `Origin`，转发所有路径，不缓存响应。直接非回环 HTTP 监听会被拒绝。客户端来源以真实 TCP 对端为准，不信任 `X-Forwarded-For`；反向代理用户共享登录来源预算，代理可另做客户端限流。

公网面板可在 `web configure` 增加 `--base-path /<随机字符串>`，将页面、静态资源和 API 一起放到该前缀下。路径为单层 `/` 加 1–128 位英文字母、数字、下划线或连字符；建议在部署时用密码学随机源生成至少 128 位随机值，保存在管理员的私有访问说明中。浏览器地址为 `https://admin.example/<随机字符串>/`，`origin` 仍只填 `https://admin.example`，不要带路径。反向代理应原样转发前缀，不要剥离。

设置前缀后，根路径、根路径 API 和其他路径返回 404，不提供入口发现或跳转；只有完整已知前缀缺少末尾斜杠时补上斜杠。Cookie 限定在该前缀下，响应不缓存且不发送 Referer。随机路径只减少扫描噪声，不能替代 HTTPS、管理员密码、来源校验和登录限流。修改路径会撤销旧会话，重启服务后新入口生效；未设置时兼容原有根路径。随机值不应提交源码或写入公开日志。

密码从文件或 `--password-stdin` 读取，不支持明文密码命令行参数；无默认密码、匿名初始化接口或公网首次抢占账号流程。重新执行 `web configure` 会立即使旧会话失效，监听/TLS 改变须重启服务。该命令的未指定字段采用默认值；调整配置时应完整传入需要保留的监听、origin、账号和 TLS 设置。

## 管理边界

- `web-admin.json` 保存本机监听、用户名、随机 salt、PBKDF2-SHA256 密码摘要与 TLS 路径，权限 0600；不进入 SQLite，不复制到从机。状态备份不包含 Web 认证设置，恢复业务状态不会回退管理员密码。
- 管理员拥有该实例的全部管理权限。现有代理用户并非面板登录账号。主机统一管理从机用户授权；从机 Web 拒绝本地修改受主机管理的用户、设备和节点。
- Linux HTTP 使用独立 exec 子进程，先切换 UID/GID、清空 capabilities，再接受请求。特权后端重新验证登录、来源和 CSRF，并只执行显式白名单操作；只读订阅使用另一条 IPC 通道。
- Web 首次启动失败会记录错误，后台计量与主从授权维护继续；修复后重启服务。工作进程在运行中异常退出时每 5 秒重试。
- 会话持续 8 小时，cookie 为 HttpOnly、SameSite=Strict，HTTPS 时带 Secure；最多 32 个会话。5 分钟内单来源 5 次、全局 60 次失败登录触发限流。重启清空会话。
- 请求体最多 256 KiB；管理 IPC 响应最多 8 MiB。一次仅允许一个后台修改任务，重复提交返回冲突；进度通过任务 ID 查询，断线不会自动重放写操作。
- 普通响应不包含密码、UUID、订阅 token、SSH 私钥或完整代理 JSON。明确点击下载时才返回设备订阅地址或 YAML，并重新检查有效期、配额和授权。

配置、身份、授权和限速继续通过原有锁、数据库事务、候选校验和失败恢复执行。页面保存后是否还需应用配置，会在表单和任务结果中说明。

## 日常操作

- 左侧导航管理用户、线路、订阅与运维；右上角可切换浅色、深色或跟随系统。`Ctrl/⌘ K` 搜索页面和用户。
- 用户表格支持状态筛选、名称排序、列显隐和分页；点击用户名查看设备、策略与连接记录。
- 用户表格和设备详情均有「分配线路」入口。先选设备，再勾选已连接、已应用的线路；当前授权自动勾选，提交前显示新增与撤销项。一次保存全部选择，保留节点的名称、身份、限速和用量；其他设备不变。并发修改会拒绝旧表单，失败不部分保存。订阅随保存更新，运行入口仍需应用配置。
- 「线路与服务器 → 线路画布」按入口展示已保存线路与该服务器上的落地。从入口圆点拖到落地，或点击圆点再选落地，即可建立线路；保存后应用拓扑，再分配给用户。画布不会为连接增加另一台中转。读取失败时已有线路仍可查看；多跳线路沿用详情中的高级编辑。服务器登记及基础出站在「服务器与落地」页签维护。
- 「管理账号」可修改本机面板用户名及密码，右上角账号菜单也可进入。当前用户名自动填入，当前密码必须验证，新密码留空则保留原密码；成功后立即撤销所有已登录会话，使用新信息重新登录。不改变代理用户、节点、监听地址、TLS 或访问路径，也不自动修改从机的管理账号。五分钟内连续五次验证失败会暂时限制重试。
- 编辑自动带入当前非敏感设置，修改后显示原值。只提交实际修改的字段，后台刷新不会覆盖正在编辑的值。
- 保存期间等待后台任务完成；失败保留非敏感输入。未保存就退出会提示放弃修改。证书路径、密码和私钥不会从服务器读回表单。
- Radix 弹层的滚动锁样式使用服务端已有的逐响应 CSP nonce，不放宽样式策略。主题仅保存在浏览器，不修改业务配置。

## 自动化

原 `admin` 命令继续接受参数，没有 TUI、`ui`、`menu` 或 `simple-menu`：

```sh
sbmgr admin user set alice --quota 100G
sbmgr admin policy user alice --block-ports 25,445 --max-connections 100
sbmgr admin batch --users alice,bob --quota 200G --enabled=true
sbmgr admin client set --server relay.example --port 443
sbmgr admin audit --limit 100
sbmgr admin apply --restart
```

完整批量能力可使用 `admin batch --file /absolute/path/batch.json`。`Kind` 为 0 用户设置、1 节点限速、2 异常流量、3 来源 IP、4 访问策略；字段省略保持原值，显式零值/空列表清除。结构见 `batchOperation` 及各 patch 类型。例如：

```json
{"Kind":4,"Users":["alice","bob"],"Access":{"BlockedPorts":[25,445],"MaxConnections":100,"ConnectionAction":"alert"}}
```

不提供应用内自更新、版本历史或软件回滚。下载产物核验、部署前备份、候选替换与失败恢复仍由 [外部部署流程](OPERATIONS.md) 完成。

## 前端工程与构建

界面参考 [Shadcn Admin](https://github.com/satnaing/shadcn-admin)，采用 shadcn/ui、Radix UI、Tailwind CSS 4、Inter 与 Lucide；沿用 React 18、React Router 和 Redux Toolkit，以保留现有路由、认证与任务流程。精确依赖在 `frontend/package-lock.json`，上游许可见 `frontend/THIRD_PARTY_NOTICES.md`，构建时将许可文本一并内嵌。

- `frontend/src/api.ts`：同源 API、CSRF、会话失效处理；原始异常与请求凭据不写日志。
- `frontend/src/store.ts`：Redux Toolkit 管理认证、快照、后台任务和通知；禁用 Redux DevTools，不存储登录密码或代理 JSON。
- `frontend/src/components`：shadcn/Radix 菜单、动态表单与共享列表；`pages` 保存各管理页面。
- `cmd/sbmgr/web/dist/`：Vite 输出的临时构建目录，被 Git 忽略。先构建前端，再构建 Go；发布脚本自动执行两步。
- 页面将每次响应独立的 CSP nonce 提供给 Radix 的动态滚动锁样式，保留同源脚本、来源校验和后端认证边界。

运行 `npm --prefix frontend run dev` 可启用 Vite 本地开发预览，API 代理到本机回环测试后端；正式验收使用完整 Go 二进制。前端组件测试使用 Vitest + React Testing Library。仅在本机或隔离测试数据上调试，不把生产凭据写入开发配置。
