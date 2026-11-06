# v8.1 前端依赖与来源

实际版本以 internal/chatui/package.json 和唯一 pnpm-lock.yaml 为准，固定 React 19.3.1、Vite 8.3.6、TypeScript 7.0.2。无 React Router、Zustand、第二套缓存或表单框架。

| 依赖 | 用途 | 许可证 | 运行形态 |
| --- | --- | --- | --- |
| React / react-dom | 展示和 useSyncExternalStore | MIT | 首屏 |
| radix-ui | Select/Dialog/Switch/ContextMenu 的可访问交互 | MIT | 按使用点打包 |
| lucide-react | 本项目有限图标 | ISC | tree shaking |
| class-variance-authority | Button variants | Apache-2.0 | 小型组件函数 |
| clsx / tailwind-merge | 合并组件类名 | MIT | 小型组件函数 |
| markdown-it | 禁用 HTML 的 Markdown | MIT | Markdown chunk |
| katex | 有界且不可信的公式排版 | MIT | Markdown chunk，字体 woff2 |
| highlight.js | 固定语言的代码高亮 | BSD-3-Clause | Markdown chunk |
| lucide | 保留的受测设备逻辑使用；未调用旧安装器 | ISC | 未作为另一套 UI 入口 |
| Tailwind 4 / Vite / TS / React 插件 | 构建与类型检查 | MIT / Apache-2.0 | 开发工具，用户不安装 |
| Playwright 1.65.0 | 真实浏览器检查 | Apache-2.0 | 开发工具 |

当前完整生产构建首屏 JS 约 141 KB gzip，设置约 24 KB、Markdown/KaTeX/高亮约 148 KB 懒加载；这是当前产物观察值，最终验收再次记录。不能把每个 npm 包的安装大小当成浏览器实际传输大小。所有资源同源，fonts 不用 data URL。THIRD_PARTY_NOTICES.md 按锁定依赖闭包生成，发行时实际检查。

shadcn Button/Dialog 源码参考提交 `8a3536678a528cc012b9e62484290e79d0be410f`，来源 https://github.com/shadcn-ui/ui ，MIT；原许可证位于 frontend/components/ui/SHADCN-LICENSE.md。本项目调整语义 token、焦点 outline 和接口。后续引入组件使用脚本固定的 shadcn 4.23.0 CLI，逐次审阅来源及差异。

视觉审阅参考 Damir Sokolovsky 的 https://github.com/heydamir/unslop ，CC BY 4.0；不分发为运行依赖。实际画面按间距、颜色、图标、文案、字形、布局和细节检查；F1 方向已经用户确认。

最低浏览器：Chrome/Edge 111、Firefox 128、Safari 16.4。外部 browser-check.js 在模块挂载前独立运行，并在旧版本显示升级提示，符合 script-src self；WebView2 是 Chromium，但仍需原生文件、缩放、退出和标题栏实测。
