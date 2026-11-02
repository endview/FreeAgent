# 可选公网 Web（v7.10，Linux）

公网入口默认关闭。它由 **Caddy HTTPS → 受限 Unix socket → 当前 Owner**
组成，使用相同的 TaskService、对话、权限、命令、附件和 Store。Windows
便携包提供本地桌面/CLI，不提供公网监听。不要把本地 Owner 端口直接放行。

## 安装和开启

1. 使用干净 Linux 便携包，按 `SERVER_DEPLOYMENT.md` 安装非 root 服务。
   代理必须使用另一个非 root 账号。安装器的可选参数只准备代理组和运行目录，
   不会开启公网或探测 Provider：

   ```sh
   sudo python3 deploy/install-server.py --package /path/to/clean-package \
     --public-proxy-user caddy --public-group freeagent-public --start
   ```

   已运行的代理需要在维护窗口重新启动，才能取得新增组。更新时保留相同参数；
   安装器拒绝覆盖未知 unit、管理命令或未经核对的安装目录。

2. 用安装器输出的管理命令设置管理员密码。默认隐藏输入，并要求重复；秘密不放在参数里：

   ```sh
   sudo /usr/local/bin/freeagent public password
   sudo /usr/local/bin/freeagent public status
   ```

   自动测试可显式使用 `--password-stdin`，从当前进程的输入管道读取一行。
   密码为 12–1024 字节，使用随机盐的 Argon2id（64 MiB，3 次，2 路）。
   `public-admin.json` 为服务用户的 0600 文件，不属于 Provider Key，
   不进入模型请求、日志、普通配置或备份。

3. GUI 的设置页打开“公网安全设置”，逐项选择开关并填写准确的 HTTPS Origin。
   也可以通过现有 CLI 控制目录或 AI 提案保存同一份配置：

   ```sh
   freeagent public configure --policy-json '<完整 public_web JSON>'
   # 或：control plan --kind public_web.set_policy --arguments '<同一 JSON>'
   ```

   CLI 会显示实际变化并提示确认。AI 提案必须由用户通过已有控制卡片/CLI
   确认。密码只在直接安全表单或隐藏输入中消费，模型只能说明如何设置。
   从 `public status` 的 `config` 取得完整初始值；不要把整个状态包装对象作为 policy。
   Origin 只包含 `https://主机[:端口]`，不含路径、尾斜杠、查询或凭据。

4. 审阅 `deploy/freeagent.Caddyfile`，把 Origin 和 socket 与配置保持一致。
   `FREEAGENT_PUBLIC_ORIGIN` 和 `FREEAGENT_PUBLIC_SOCKET` 为代理的环境变量。
   使用部署的 Caddy 版本运行 `caddy validate`。证书签发/续期由代理负责；
   公网 IP 的证书必须覆盖该 IP，不能假设任意 IP 都能自动签发。
   使用私有 CA 的测试只能证明该 CA 下的 TLS 链路，不能代替公网可信证书验收。

5. 启用配置后显式重启 Owner，再启动/重载经过验证的专用代理。
   只开放代理端口。缺少密码、组、目录权限或证书时拒绝工作，不回退匿名 HTTP。
   代理账号只应访问公共 socket，不应能读发现文件、数据库、Key 或密码哈希。

安装器默认 socket 位于 `/run/<unit>-public/web.sock`；运行目录由 systemd
创建，Owner 校验归属和非链接路径后改为专用代理组 0750，socket 为 0660。
手工 unit 可额外设置 `RuntimeDirectory=freeagent freeagent-public`、
`SupplementaryGroups=freeagent-public`，并把 `/run/freeagent-public`
加入 `ReadWritePaths`。保留 `/var/lib/freeagent` 和私有运行目录的 0700。

## 登录、权限与接续

一个管理员，最多默认 8 个内存会话；新登录淘汰最久未交互会话。默认 30 分钟
无交互或 12 小时绝对到期。SSE 心跳不续期。Cookie 为 Secure、HttpOnly、
SameSite=Strict 的 `__Host-FreeAgent`，只保存随机会话值，浏览器不获得
本地 launch key 或 attach secret。变更请求同时要求严格 HTTPS Origin 和独立 CSRF。

远程权限上限默认 Full，实际与空间和原发起人权限取低，新空间仍为 None。
降低上限立即约束后续派发和继续；已保存的 Run 不会因之后提高上限而扩权。
远程提高上限只能提案，必须在本机确认。Telegram 等已有 Run 保留原发起人。
审批绑定会话、Owner、Run、工具调用和摘要，不能复制旧会话卡片给另一个会话使用。

退出、到期、更换密码、关闭公网或重启撤销会话并关闭 SSE/文件传输；已经开始的
任务不因浏览器退出而撤销，不会自动重放。原会话的暂存上传只供该会话提交；
已经提交的附件遵守既有对话/空间授权。相同对话可从公网 → 本机 CLI → 公网接续。

Full/host 是同一 OS 用户下的明确宿主执行入口，命令可访问服务用户有权访问的
管理文件。需要隔离时选择沙箱；沙箱不可用会拒绝沙箱命令，不自动改为 host。

Cookie 不按端口隔离。推荐专用主机名，或确认该主机没有其他不可信的 HTTPS
服务。应用对域名发送 HSTS；浏览器对 IP 地址忽略 HSTS。

## 公网安全设置与恢复

用户可分别切换失败锁定、自动黑名单、全局保护、IP 白/黑名单、登录 Cookie
检测、会话绑定。默认开启失败锁定（15 分钟 5 次，锁定 30 分钟）和登录 Cookie
检测，其余默认关闭。Cookie 检测只是一用一次的登录检测，不隐式开启会话绑定。

黑名单优先；IPv4 按地址、IPv6 按 /64 计失败。失败锁定不影响已有有效会话，
名单变化可撤销不再符合策略的会话。自动黑名单、全局失败阈值及时间均有上限。
会挡住当前远程来源的配置被拒绝，应先加入其白名单。

密码校验最多同时 2 个；来源状态最多 512 个、登录检测最多 128 个、
会话最多 8 个，安全事件保留最近 256 条。过载拒绝新请求，不扩大内存状态。
计数、锁定和会话不跨重启恢复；无凭据的事件环复用现有 SQLite meta，
保存错误在运行状态中显示。上传有独立大小、并发和
60 秒读取限制；下载逐块校验会话，并限制阻塞写入时间。

客户端地址只接受受信任 Unix listener 上的 `X-FreeAgent-Client-IP`，Caddy
模板用连接来源覆盖它，并覆盖协议头、删除本地凭据头。互联网提供的同名头
不可信。使用 CDN/其他前置代理时先在 Caddy 配置正确的可信代理，否则来源
可能都变成代理地址。不要随意信任任意 `X-Forwarded-For`。

通过 SSH 的本机入口始终可以恢复，不受公网名单/锁定限制：

```sh
sudo /usr/local/bin/freeagent public clear-locks
sudo /usr/local/bin/freeagent public password
sudo /usr/local/bin/freeagent public disable
```

`public disable` 仍显示配置变化并提示确认。恢复密码不会删除对话。
备份故意不包含管理员口令、会话和代理私钥；恢复到新实例后显式重设。

## 验收状态的含义

单元/协议模拟、真实 Debian Unix socket、真实 TLS 代理/浏览器和公网可信证书
分别记录。没有当前凭据的视觉 Provider、没有官方应用的渠道文件收发为
NOT_RUN；QQ 未开放的任意文件上传为 NOT_SUPPORTED。UNKNOWN 只用于已经
派发但结果不确定的执行，不能用来代替“尚未测试”。实际结果见实施记录。
