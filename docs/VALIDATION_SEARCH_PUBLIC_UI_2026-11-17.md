# 公网搜索设置修复与验证

日期：2026-11-17。目标版本：v8.9.0-dev.3。

## 原因与修正

v8.9.0-dev.2 的搜索设置页已随正式前端交付，本机 API 正常；公网 `publicRoute` 的精确白名单漏了 `GET/PATCH /api/settings/web-search` 和 `POST /api/settings/web-search/test`。登录后的真实公网请求因此返回纯文本 404，前端仅显示笼统错误，无法显示管理表单。

此前核对线上静态文件与本机接口不能证明已登录公网管理可用。本次补齐精确方法白名单，保持公共认证、CSRF 和既有控制面边界；未开放通配路由或 attach 凭据。

前端错误状态补重新加载按钮；无法解析的成功响应也明确失败，不生成虚假空配置。关闭网络工具时提供设置直达；普通 HTTP 来源提示说明搜索可留空。共享下拉框在空值时显示既有选项文字。

## 已执行检查

- 新增公网回归在补丁前明确失败：`GET /api/settings/web-search` 返回 404；修复后通过。覆盖匿名拒绝、真实登录、CSRF、保存回读、旧修订冲突、固定查询恰好一次及错误方法/未知子路由拒绝。
- Windows 独立 PostgreSQL 下的 Public/Search/设置覆盖 `-race` 回归通过；没有读取用户生产 Key。
- 独立 TLS 服务使用实际 PublicServer、真实 Cookie 和 CSRF，真实 Chrome 登录后完成网络工具设置、创建合成 Key、添加搜索连接、保存、刷新回读和本机 SearXNG 固定查询。服务端检查没有绕过公网路由。
- 模拟上游 502 HTML 与 HTTP 200 非 JSON，页面显示明确错误，隐藏保存表单，重新加载后恢复；重试设置读取不会重新发起搜索查询。
- 检查 1440×960、1024×768、768×1024、390×844、320×568，页面和内容无横向溢出，保存按钮可操作。真实手机仍未重测。
- TypeScript、token/HTML lint、116 项前端测试和生产构建通过，首屏 JS gzip 171,101 字节。

真实浏览器用独立测试服务自己的临时证书；没有跳过生产站点的安全提示。所有搜索调用只访问本机合成服务，不调用付费服务或发送真人 TG 消息。

## 可重复验证

在仓库根目录，先完成前端生产构建并准备测试用 PostgreSQL 和 Playwright/系统 Chrome：

```powershell
$env:FREEAGENT_PUBLIC_SEARCH_BROWSER = '1'
$env:FREEAGENT_PUBLIC_SEARCH_ARTIFACTS = Join-Path $PWD 'output/public-search-browser'
$env:FREEAGENT_BROWSER_CHANNEL = 'chrome'
python scripts/test-postgres.py --runtime output/postgresql-runtime/windows/pgsql -- go test -v -count=1 -run '^TestPublicSearchRealBrowser$' -timeout=4m ./internal/chatui
Remove-Item Env:FREEAGENT_PUBLIC_SEARCH_BROWSER
Remove-Item Env:FREEAGENT_PUBLIC_SEARCH_ARTIFACTS
Remove-Item Env:FREEAGENT_BROWSER_CHANNEL
```

此测试使用新建数据、独立 HTTPS 端口和合成密码；不附着生产 Owner。

## 发布与 Hytron

已推送并发布 [v8.9.0-dev.3](https://github.com/endview/FreeAgent/releases/tag/v8.9.0-dev.3)，程序/标签/归档/Hytron 对应提交 `6004486a70da09cf7c52fa4f21b6ba4a533a900f`。Windows/Linux 各两次干净构建的归档和源码清单相同，八个附件的 API 摘要与本地原值一致、匿名下载地址全部返回 200。Browser 0.1.1 与 Computer Use 0.1.0 使用上版完全相同的独立扩展包，来源提交分别保留。

Windows 最终便携归档启动、版本、数据初始化、Key、Web/CLI 和子进程退出检查通过；本次未重测原生桌面窗口启闭。Debian 12 以独立非 root 用户验证最终 Linux 归档，并完成 Public/Search/设置覆盖 race 回归，64.4 秒通过，没有使用生产 Owner 或模型凭据。

Hytron 更新前已验证备份，检查无活跃任务后执行不取消任务的平滑交接；切换与核对耗时 5.86 秒。schema 2 无迁移。8 个会话、115 个 Run、694 条消息、527 个 Attempt 及原配置/凭据逐项保持；两条旧输入仍暂停，AffectWeave 1.6.0-dev.3 程序、业务记录和明确能力选择均保持。

Core/渠道 active、NRestarts=0，doctor 通过、TG connected。公网 HTTPS/CSP/匿名登录态正常，入口资源与 Owner 完全相同；线上设置资源 `SettingsPage-YJH3-sCd.js` 含搜索表单、保存、失败重试与网络设置入口。没有替用户开启网络或搜索、添加真实 Key、发送真人 TG 测试消息。

[全量 CI](https://github.com/endview/FreeAgent/actions/runs/44035461230) 收尾时仍在运行；不将本次已通过的定向回归与归档验收表述为全量 CI 已通过。

本机证据在忽略目录 `output/search-ui-fix-20261117/`；远端独立验证在 `/var/tmp/freeagent-search-ui-release-20261117/`，生产核对在 `/var/tmp/freeagent-search-ui-production-20261117/`。备份位置和恢复说明记录在本机私有维护文档，不公开凭据。
