# v8.9.0-dev.2：可选浏览器与 Windows Computer Use 预览

日期：2026-11-17。沿用 schema 2，无数据库迁移。

- 分别交付 Browser 0.1.0（Windows/Linux）和 Computer Use 0.1.0（Windows），以独立扩展包安装、启用和选择；Core 不增加浏览器/桌面运行时依赖。
- Browser 提供独立 profile、DOM/截图、点击输入、标签管理、下载及人工接管，公共网络出口校验；本机 Chrome 与 Debian 12 Chromium 已实际验证。首版元素操作限主 frame。
- Computer Use 提供本机允许窗口的 UIA/截图/输入、持续控制提示和停止；本机用户显式启用，公网与渠道不能调用。真实 150% 缩放操作通过，真实多屏与其他缩放仍未验收。
- 通用扩展契约增加可信调用范围、仅本机工具和有界图片/文件产物。保持既有附件授权、模型图像预算、UNKNOWN 与工具组结构；旧观察不反复堆积图片。
- 扩展设置页支持 schema 中字符串、布尔和字符串列表的普通控件，沿用保存/回读/草稿/冲突流程。

安装、限制与停用见[自动化扩展](AUTOMATION_EXTENSIONS.md)，结果见[验收记录](VALIDATION_AUTOMATION_2026-11-17.md)。更新宿主不会自动安装、开启扩展或更改 AffectWeave。
