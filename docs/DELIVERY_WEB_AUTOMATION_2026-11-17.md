# 搜索、Browser 与 Computer Use 交付

日期：2026-11-17。先独立交付搜索 `v8.9.0-dev.1`，再交付含通用扩展接口的宿主 `v8.9.0-dev.2`。

## 已发布内容

- [v8.9.0-dev.1](https://github.com/endview/FreeAgent/releases/tag/v8.9.0-dev.1)：搜索版，源码 `5eb018801c5abf64d68df0726dc511c2f88a08d5`；Windows/Linux 普通与 race 四组 CI 全通过。
- [v8.9.0-dev.2](https://github.com/endview/FreeAgent/releases/tag/v8.9.0-dev.2)：宿主源码、标签、归档及 Hytron 均为 `5eb180020d6a2a187ba46047e0d12f7e91b0cd85`。
- dev.2 包含两个 Core 便携包、Browser 0.1.1 Windows/Linux 两个扩展包、Computer Use 0.1.0 Windows 扩展包，以及源码清单、构建记录和校验和，共八个附件。Browser 0.1.1 为后续独立扩展修正，源提交记录在扩展 BUILD_INFO 中，宿主和 Computer Use 不受影响。附件 API 摘要与本地原值一致。
- Core 两个平台双次构建一致；扩展包记录来源提交和校验和，不宣称它们可重复构建。发行包不包含凭据、用户数据、浏览器 profile 或截图。

## 实测

搜索结论见[联网验收](VALIDATION_WEB_SEARCH_2026-11-17.md)。自动化的真实浏览器、Core/PG/RPC、实际桌面、配置交互和未运行项目见[自动化验收](VALIDATION_AUTOMATION_2026-11-17.md)。

最终 dev.2 Linux 归档通过独立非 root 验收：便携启动、搜索与 Provider 回归、Browser 源代码 race / 强制退出清理、实际 Linux 扩展包通过 Core 打开网页和回传截图全部通过。Windows 最终便携启动通过，最终桌面扩展包实际完成中文输入与保存结果核对。相关上下文/附件/扩展 race 回归、116 项前端测试、四种宽度保存/刷新回读和静态检查通过。

[最终扩展构建 CI](https://github.com/endview/FreeAgent/actions/runs/43960255349) Windows/Linux 通过。[最终 Core 全量 CI](https://github.com/endview/FreeAgent/actions/runs/43960255533) 发布时仍在运行；不把本地定向回归写成该 CI 已完成。

## Hytron

宿主已更新至 `v8.9.0-dev.2`，TG connected、Core/渠道 active、无重启循环，doctor、HTTPS 200、CSP 与公开静态资源核对正常。schema 2 无迁移。

升级前备份并验证；会话、Run、消息、Attempt、凭据、模型/搜索设置、两个暂停输入和 AffectWeave 程序/业务记录及明确选择均保留。没有发送真人 TG 测试消息。备份路径、回滚程序和脱敏详细结果保存在本机私有维护记录。

程序升级没有替生产会话安装或开启 Browser / Computer Use。两者按[安装说明](AUTOMATION_EXTENSIONS.md)分别导入与启用；桌面工具仅允许本机入口。AffectWeave 版本不捆绑改变。
