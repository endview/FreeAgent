# FreeAgent v8.1 F1 交互样例

开发专用，合成数据；不读取真实对话、配置或凭据，不调用 Provider，也不属于正式 Go 静态资源。

目标：让对话、工具过程、精确审批和设置在同一个轻量工作台内保持清楚。视觉方向尚待用户确认，token 未冻结。

设计取舍：浅灰侧栏、白色阅读面、墨色文字、蓝色操作强调；不使用营销首页、大数字面板或全屏卡片网格。系统中文字体，正文 15px/1.7。左侧按现有空间归类，会话不新增分组对象。工具耗时采用等宽数字；普通回答保持正文形式，只有需确认的变更形成卡片。

布局：侧栏 → 当前会话/设置详情；窄屏改为抽屉。点击状态选择可查看正常、运行、待确认、UNKNOWN、失败和空状态。支持可调侧栏、阅读宽度和输入器最大高度、深浅主题、队列转引导、设置草稿保留。所有操作只修改本样例内存；UNKNOWN 的核查按钮不会假装已确认实际结果。

```powershell
pnpm --dir design/v8.1 install --frozen-lockfile
pnpm --dir design/v8.1 typecheck
pnpm --dir design/v8.1 build
pnpm --dir design/v8.1 dev --port 4181
```

F1 使用已验证的 Vite 7；F2 仍按主计划先实测 Vite 8。shadcn Button/Dialog 取自此前固定来源的框架小样，来源记录见 `SHADCN-LICENSE.md`；本项目改动由 Git 记录。不预设这些样例已通过真实 Core、生产 CSP 或完整性能验收。
