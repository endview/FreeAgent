# Telegram 私聊真实验收（2026-11-03）

范围：用户授权的独立 Bot、Debian 12 amd64、FreeAgent `v7.11.0-dev.1`
发行源码 `e64d999eaf919a157b2d61c3b9680f5799331902`、DeepSeek 官方
`deepseek-flash`。主机、Bot/用户身份和实际凭据仅在私有部署资料中保存。

## PASS

- 干净发行程序及包清单 SHA-256 核对；非 root systemd 实例安装、启动与开机启用。
- 真实 DeepSeek `/models` 和模型请求成功，广告模型为 `deepseek-flash`、
  `deepseek-v4-pro`，本次选择 Flash。
- Telegram 官方 `getMe` 成功，确认没有现存 webhook 后启用产品长轮询，命令菜单注册成功。
- 服务重启后，模型与 Bot 连接配置保留并重新就绪。
- 用户消费一次性配对码，创建一个私聊空间及一个默认对话，多对话关闭、权限 None。
- 用户确认收到模型回复。服务器于 `2026-11-03T13:27:04Z` 核对的四轮请求均
  completed，每轮一次 finished 模型调用，回传均为 sent；确认字节数为 139、963、506、489。
- 凭据经标准输入保存，凭据文件权限 0600；配置、SQLite 和 journal 的实际凭据扫描零匹配。
  公网入口未开启。

这证明当前私聊文字链路 Telegram → Owner/TaskService → 真实模型 → Telegram 可用。
首个 Telegram 只读探测曾因短暂网络异常失败，后续 API、轮询与真实收发通过。
初始部署辅助脚本曾漏传 CLI 确认输入，修正后完成配置与复验；原始失败记录保留。

## NOT_RUN

群聊与 Privacy Mode 的实际可见范围、第二账号权限隔离、真实 TG 文件/图片、
审批按钮、长时间断线恢复及该部署的八小时长测。本记录不覆盖 Lark/QQ 或视觉
语义，也不修改发行包中更早时间生成的验收摘要。实际收发证据和备份私有保存。
