# v0.2.20 桌面安全区、工具栏与下拉框修复

## 问题结论

截图中的两个现象都是真实缺陷，而不是截图裁切：

1. Tauri 自绘标题栏占据窗口顶部 36px，但 Sub2API 页面中的固定侧栏、导航进度条和 Teleport 弹层仍按浏览器视口 `top: 0` 定位，因此 Logo、页面标题和右上操作区会被标题栏或通知遮挡。
2. 成本监控工具栏同时受旧响应式规则和 Apple UI 通用 `sidebar` 规则影响，主导航宽度不足后文字换成两行，四个工作区与 Sub2API 设置也没有形成统一分段控件。
3. 项目仍有 53 个单选原生 `<select>` 分布在 16 个 Vue 文件。闭合状态可以用普通 CSS 装饰，但旧式系统 popup 不受应用主题控制，导致展开层像未设计的系统控件。

## 修复设计

### 桌面标题栏安全区

- 桌面壳定义唯一的 `--desktop-titlebar-height: 36px`。
- App 内容继续使用 flex 布局，不给普通页面重复增加内边距。
- 只修正绕过内容流的固定层：侧栏、导航进度条、全屏弹层和视口高度工具类。
- Teleport 到 `body` 的全屏弹层通过 `desktop-runtime` 根类继承相同安全区。
- Toast 在桌面端移动到右下角更新入口上方，避免覆盖工具栏；Web 端仍保持原有右上位置。

### 成本监控主工具栏

- 工具栏桌面高度由 82px 收紧到 74px。
- 工作区导航改用 Apple UI `segmented` 语义，不再误标为 `sidebar`。
- 资产总览、上游排行、渠道号池、API 接入、Sub2API 设置统一为 142px 等宽、40px 高、单行文字。
- 数据治理不再作为文字按钮占用主操作带，保留为带 `aria-label` 和 tooltip 的右侧图标工具。

### 全局现代下拉框

- 对所有非 `multiple`、非显式 `size` 的 HTML select 使用 `@supports (appearance: base-select)` 渐进增强。
- 同时给 select 与 `::picker(select)` 设置 `appearance: base-select`。
- 统一顶层 picker 的圆角、阴影、间距、最大高度、选项 hover/focus、当前项 checkmark、深色和高对比度颜色。
- 保留浏览器原有 value、change、表单提交、键盘导航、Escape/Tab、触摸和辅助技术语义；不支持该能力的运行时继续使用功能完整的原生回退。
- 搜索型或富内容选择仍使用项目已有 `Select.vue`，本次不重复实现第二套状态机。

## 算法和数据边界

本修复只调整桌面壳、工具栏和选择控件表现，不修改 Sub2API Token 输入/输出/缓存计费公式、模型价格、采购摊销或经济预测算法。成本算法版本保持 `v1.6.0`，桌面版本升级为 `v0.2.20`。

## 回归覆盖

- `desktopChromeLayout.spec.ts`：标题栏高度共享、固定侧栏安全区、全屏弹层安全区、Toast 非遮挡位置。
- `uiShellContract.spec.ts`：紧凑工具栏、等宽单行导航、分段语义、数据治理工具入口。
- `nativeSelectVisualContract.spec.ts`：base-select、picker、checkmark、焦点、选中态和深色外观。
- 生产构建后检查最终 CSS，确认新选择器和桌面安全区规则未被 PostCSS/Tailwind 移除。

## 升级方式

v0.2.19 客户端可通过现有签名自动更新通道原位升级到 v0.2.20，不需要先卸载或重新配置。也可手动运行 v0.2.20 NSIS 安装包覆盖升级。
