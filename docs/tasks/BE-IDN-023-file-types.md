# BE-IDN-023 · 文件上传类型白名单扩展

- ID: BE-IDN-023
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
- 预计: 0.2 人日
- 分支: `feat/be-idn-023-file-types`
- 依赖: BE-IDN-021（文件库）

---

## 1. 现象

上传 `text/plain` 文件被拒：

```json
{ "code": 10001, "message": "文件参数无效" }
```

当前白名单只有：`image/*`、PDF、CSV、XLSX、ZIP。
**普通文本、Markdown、JSON、日志**这些最常见的附件类型都被挡住了。

## 2. 改动

### 2.1 允许的类型

在现有白名单基础上增加：

| MIME | 典型文件 |
|---|---|
| `text/plain` | `.txt` `.log` `.md` `.json` `.yaml`（多数被嗅探为 text/plain） |
| `text/markdown` | 显式声明时 |
| `text/csv` | 已有 |
| `application/json` | 显式声明时 |
| `application/x-ndjson` | 流式日志 |
| `text/xml` / `application/xml` | 配置与数据交换 |
| `application/yaml` / `text/yaml` | 配置文件 |

保持允许：`image/*`（**含 SVG**，现状如此）、PDF、CSV、XLSX、ZIP。

### 2.2 安全边界（不变）

- 仍以**嗅探结果**为准，不信声明类型
- 伪装成 `.txt` 的 HTML/JS → 嗅探为 `text/html`、`text/javascript` → **拒绝**
- SVG 为了预览功能继续允许，但**预签名 URL 必须带 `response-content-disposition=attachment`**，
  避免直接在浏览器打开时执行脚本
- 下载始终 `Content-Disposition: attachment` + `X-Content-Type-Options: nosniff`（已有）
- 大小上限不变（20 MiB）

### 2.3 错误信息

当前被拒时只有「文件参数无效」，无法定位原因。改为：

```json
{ "code": 10001, "message": "不支持的文件类型：text/html",
  "details": { "mime": "text/html", "allowed": ["image/*","application/pdf", …] } }
```

超限与类型错误分开：

| 情况 | message |
|---|---|
| 类型不允许 | 不支持的文件类型：<mime> |
| 超过大小 | 文件超过 20 MB 上限 |
| 文件名为空 | 文件名不能为空 |

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | txt | 上传成功，下载一致 |
| 2 | md | 上传成功 |
| 3 | json | 上传成功 |
| 4 | log | 上传成功 |
| 5 | csv / pdf / png / zip / xlsx | 仍成功 |
| 6 | 伪装 html | `.txt` 内含 HTML → 400，message 含嗅探类型 |
| 7 | SVG | 仍可上传；预签名 URL 打开时是下载而非渲染 |
| 8 | 超限 | 20 MiB + 1 字节 → 413，message 为大小提示 |
| 9 | 错误细节 | 类型错误响应含 details.allowed |
| 10 | 全量 | `verify` 通过 |

## 4. Codex prompt

```
实现 docs/tasks/BE-IDN-023-file-types.md。

分支：feat/be-idn-023-file-types（从最新 main de20a59 开）

必读：本卡、internal/modules/file/validation.go、
internal/modules/file/file.go（上传流程）、internal/modules/file/file_http.go

改动：
1. 白名单增加 text/plain、text/markdown、application/json、
   application/x-ndjson、text/xml、application/xml、application/yaml、text/yaml
2. 保持：嗅探结果为准、拒绝 SVG、下载 attachment + nosniff、20 MiB 上限
3. 类型/大小/文件名的错误信息分开，类型错误带 details.allowed
4. OpenAPI 补充错误响应说明

约束：
- 不放宽为「任意类型」
- 不改变对象键格式与下载头

验收按卡片 §3 十条逐条贴证据：
- 第 6 条贴伪装 HTML 的请求与响应原文
完成后停下报告，不要做前端。
```
