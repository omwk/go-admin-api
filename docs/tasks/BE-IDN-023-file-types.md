# BE-IDN-023 · 文件上传类型白名单扩展

- ID: BE-IDN-023
- 类型: 修复
- 仓库: 后端
- 状态: 已完成（本地验收）
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

分支：feat/be-idn-023-file-types（从最新 main 71bc935 开）

必读：本卡、internal/modules/file/validation.go、
internal/modules/file/file.go（上传流程）、internal/modules/file/file_http.go

改动：
1. 白名单增加 text/plain、text/markdown、application/json、
   application/x-ndjson、text/xml、application/xml、application/yaml、text/yaml
2. 保持：嗅探结果为准、下载 attachment + nosniff、20 MiB 上限
3. SVG 保留，预签名 URL 加 response-content-disposition=attachment
4. 类型/大小/文件名的错误信息分开，类型错误带 details.allowed
5. OpenAPI 补充错误响应说明

约束：
- 不放宽为「任意类型」
- 不改变对象键格式与下载头

验收按卡片 §3 十条逐条贴证据：
- 第 6 条贴伪装 HTML 的请求与响应原文
- 第 7 条贴预签名 URL 的 Content-Disposition 头
完成后停下报告，不要做前端。
```

## 5. 实现与接口同步

- [x] 先补回归测试并确认旧实现失败：新增文本上传、错误消息与附件预签名参数。
- [x] 扩展明确的 MIME / 扩展名白名单；文本声明别名只兼容嗅探到的文本，记录的 MIME 仍使用嗅探结果。
- [x] 区分类型、超限、空文件名错误；类型错误的 `details.allowed` 从实际白名单生成并排序。
- [x] 在文件对象适配器中将附件响应参数交给 SDK 后签名；有效期仍为 300 秒。
- [x] 全量 verify、验收证据归档与隔离资源清理。

无新增接口；OpenAPI `api/openapi/file.yaml` 已同步：

| 接口 | 同步内容 |
|---|---|
| `POST /api/v1/admin/files` | 新增允许的 MIME；400 类型 / 空文件名错误示例及 details；413 大小提示 |
| `GET /api/v1/admin/files/{id}` | 记录现有 attachment / nosniff 响应头 |
| `GET /api/v1/admin/files/{id}/url` | 记录签名中的 attachment 参数及 300 秒有效期 |

## 6. 验收记录（2026-11-20）

环境：从 `main 71bc935` 创建本卡分支；真实 PostgreSQL 的隔离测试库、miniredis、临时私有 MinIO。HTTP 请求经过现有 JWT / 权限 / handler / service / repo 链路；测试请求的 Authorization 不进入证据。SVG 使用真实预签名 URL 请求对象存储。

执行命令：

```powershell
go test -count=1 -v ./internal/bootstrap -run '^TestFileTypesIntegration'
go test -count=1 -v ./internal/kernel/db -run '^TestMigrateIntegration'
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1
```

| # | 证据 | 结果 |
|---|---|---|
| 1 | `TestFileTypesIntegrationUploadsAndDownloads/notes.txt` | 201 / 下载 200，逐字节及 SHA256 一致；attachment + nosniff |
| 2 | 同测试 `readme.md` | 201 / 下载一致 |
| 3 | 同测试 `data.json` | 201 / 下载一致 |
| 4 | 同测试 `events.log` | 201 / 下载一致 |
| 5 | 同测试 table.csv / document.pdf / pixel.png / archive.zip / sheet.xlsx | 均 201 / 下载一致 |
| 6 | `TestFileTypesIntegrationHTMLAndErrors`；原始请求 / 响应见下 | .txt 声明 text/plain、内容为 HTML → 400，message 含 text/html |
| 7 | `TestFileTypesIntegrationSVGPresignedAttachment`；真实存储响应头见下 | SVG 上传 201；签名 query 为 attachment；GET 200、附件响应头及字节一致 |
| 8 | `TestFileTypesIntegrationHTMLAndErrors` | 20971520 字节 → 201；20971521 字节 → 413，message 为「文件超过 20 MB 上限」 |
| 9 | 同测试，解析实际 400 响应 | details.mime=text/html；details.allowed 含全部新增类型及 SVG，不含 HTML / JavaScript / 任意二进制 |
| 10 | 全量 `scripts/verify.ps1` | 退出码 0；47 个包通过、0 失败；最后一行 `all checks passed` |

补充：单元测试覆盖显式 text/markdown / application/json / application/x-ndjson / application/xml / application/yaml / text/yaml 声明与嗅探类型的兼容，以及未声明类型、二进制伪装、禁止扩展名、空文件、空文件名与精确大小上限。真实存储测试另覆盖 YAML / NDJSON / XML；原对象键正则由单元回归测试验证。

原始输出：[本卡集成验收](evidence/BE-IDN-023/integration.txt)、[全量 verify](evidence/BE-IDN-023/verify.txt)、[迁移链](evidence/BE-IDN-023/migration.txt)、[迁移结构不变量](evidence/BE-IDN-023/migration-invariants.txt)、[尺寸 / 分片 / 清理检查](evidence/BE-IDN-023/checks.txt)。

### 第 6 条：伪装 HTML 请求与响应原文

Authorization 为测试生成的真实 Bearer，仅在发往路由前加入，以下原文不记录该敏感头。

```http
POST /api/v1/admin/files HTTP/1.1
Host: example.com
Content-Type: multipart/form-data; boundary=BE-IDN-023

--BE-IDN-023
Content-Disposition: form-data; filename=spoof.txt; name=file
Content-Type: text/plain

<!DOCTYPE html><html><script>alert(1)</script></html>
--BE-IDN-023--
```

```http
HTTP/1.1 400 Bad Request
Connection: close
Access-Control-Allow-Headers: Content-Type, Authorization, X-Request-Id, Idempotency-Key
Access-Control-Allow-Methods: GET, POST, PUT, PATCH, DELETE, OPTIONS
Access-Control-Allow-Origin: *
Access-Control-Expose-Headers: X-Request-Id
Content-Type: application/json; charset=utf-8
X-Request-Id: 01M7WRKDHTERH8KTQ7G34BRTWG

{"code":10001,"message":"不支持的文件类型：text/html","details":{"allowed":["application/json","application/pdf","application/vnd.openxmlformats-officedocument.spreadsheetml.sheet","application/x-ndjson","application/xml","application/yaml","application/zip","image/bmp","image/gif","image/jpeg","image/png","image/svg+xml","image/tiff","image/webp","image/x-icon","text/csv","text/markdown","text/plain","text/xml","text/yaml"],"mime":"text/html"},"trace_id":"01M7WRKDHTERH8KTQ7G34BRTWG"}
```

### 第 7 条：SVG 预签名 URL 的实际响应头

签名参数：`response-content-disposition=attachment`，`X-Amz-Expires=300`。签名 URL 不进入日志。

```http
HTTP/1.1 200 OK
Content-Length: 71
Content-Disposition: attachment
Content-Type: image/svg+xml
Server: MinIO
X-Content-Type-Options: nosniff
```

### 自检与偏差

- `make lint`：退出码 0，最后一行 `golangci-lint run`。
- `make test` 的同一命令 `go test ./...` 由 verify 执行，47 个包通过、0 失败；摘要：

  ```text
  ok  github.com/omwk/go-admin-api/internal/bootstrap 424.086s
  ok  github.com/omwk/go-admin-api/internal/modules/file 15.083s
  all checks passed
  ```

- 隔离迁移测试：`TestMigrateIntegrationReversibleAndRepeatable` 与 `TestMigrateIntegrationConcurrentStartup` 均 PASS；执行全链 up、down 到 0、再次 up、并发启动。
- 迁移结构不变量：PartitionInvariants / TenantAndIdempotencyUniqueness / VersionDefaultsAndCompositeKeys 均 PASS。
- 分片守卫：`Source 632 = shards 631 + excluded 1`；`PASS: every integration test has exactly one shard or an explicit exclusion.` 新增 3 个集成测试全部归属 core。
- `rg -n 'TODO|not implemented' internal/`：无匹配。
- Go parser 检查本卡 7 个 Go 文件及函数体：最大文件 342 行，最大函数 69 行，均低于 400 / 80 限制。
- 日志检查：新增错误只包含嗅探 MIME / 白名单；测试不输出 Bearer、签名 URL、存储密钥；未新增业务日志。
- 无新增依赖、迁移或接口；对象键格式、现有下载处理器中的响应头未修改。
- 偏差说明：无；卡片旧 prompt 中“拒绝 SVG”及旧起点已按本次明确指令与卡片正文同步。

### 清理

已清理：测试 fixture 自动删除本卡隔离数据库、测试桶与对象（测试清理断言通过）；`docker stop be-idn-023-822f7e4a` → 停止并自动删除临时 MinIO，检查剩余容器为 0；临时存储凭据文件已删除。开发库没有本卡验收产生的租户、用户、配置或审计记录，无需对开发库执行 audit-prune；无保留测试数据、无待恢复配置。
