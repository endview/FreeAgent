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

程序发布、便携包及 Hytron 升级在本地验证完成后执行；完成后的版本、数据核对与公网回归结果另在此节补记。
