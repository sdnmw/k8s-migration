# 中文离线文档站

```bash
npm ci
npm run typecheck
npm run build:offline
npm run verify:assets
```

`build:offline` 使用 `/docs/`，平台和文档一起进入 Web 镜像。根目录 `netlify.toml` 使用普通 `npm run build`，基路径为 `/`，仅提供构建配置，不自动发布。

浏览器测试：`sh scripts/check-browser.sh http://平台地址:端口`。需要 Playwright CLI，可用 `PLAYWRIGHT_CLI` 指定安装路径。测试阻断全部非同源请求，检查本地搜索、深链接刷新、图片放大及移动端宽度；不依赖平台 API。

截图清单位于 `src/data/guide-screenshots.json`。新增截图必须包含操作、预期结果和失败处理，分辨率为 1440×900，不得含密码、Token 或 Secret 值。原始截图不改写业务结果，数字标注由 `GuideShot` 覆盖层提供。

品牌样式依据 SmartX Product UI 色彩与系统字体；沿用现有项目头像，不将其宣称为 SmartX 官方 Logo。决策、资产与内容覆盖记录在 `qa/`。
