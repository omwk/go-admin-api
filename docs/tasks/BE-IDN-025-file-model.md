# BE-IDN-025 · 文件分类、访问模式与配额

- ID: BE-IDN-025
- 类型: 新增
- 仓库: 后端
- 状态: 已实现，本地验收通过
- 预计: 2 人日
- 分支: `feat/be-idn-025-file-model`
- 依赖: BE-IDN-021/024

---

## 1. 五个问题（均已实测确认）

| # | 问题 | 事实 |
|---|---|---|
| 1 | 「上传用途」无意义 | `purpose` 只用于对象键前缀，对用户零信息量；按数据类型分类更有用 |
| 2 | 头像没有归处 | 无头像上传接口；`avatar` 只能是外部 URL |
| 3 | 预签名无意义 | 桶是**公开读**：去掉签名参数裸路径仍 200（实测） |
| 4 | 无配额 | 任何账号可无限上传，公网桶成本失控 |
| 5 | 图片无预览 | 列表只能下载，不能看图 |

## 2. 改动

### 2.1 purpose → source + category

- **source**（内部，自动）：`attachment` / `avatar` / `shop` / `export`
  - 由调用入口决定，**用户不再选择**
  - 列表响应可返回，但界面不展示
- **category**（展示，由 MIME 推导）：

| category | MIME |
|---|---|
| `image` | image/* |
| `document` | application/pdf |
| `sheet` | xlsx / csv |
| `archive` | zip |
| `text` | text/*, json, xml, yaml |
| `other` | 其余 |

列表筛选由 `purpose` 改为 `category`。

### 2.2 访问模式

`storage_provider` 增加 `access_mode`：`public`（默认）/ `private`。

| 模式 | 返回给前端的 URL |
|---|---|
| public | `public_base_url`（为空则 `https://endpoint/bucket`）+ `/` + object_key，**无签名** |
| private | 预签名 5 分钟 |

- 实测当前桶为公开读，因此默认 `public`
- `GET /admin/files/{id}/url` 按模式返回；响应增加 `expires_in`（public 为 0）
- 前端「复制链接」复制该 URL；「下载」走流式接口（两者语义区分：一个是长期可分享地址，一个是本次下载）

### 2.3 每用户配额

- provider 字段 `quota_per_user_mb`，默认 1024（1 GB），0 表示不限
- 上传前计算该用户 `sum(size_bytes)`；超限返回 413：

```json
{ "code": 10001, "message": "存储空间不足：已用 1020 MB / 上限 1024 MB" }
```

- 新增 `GET /admin/files/usage` → `{ used_bytes, quota_bytes, file_count }`

### 2.4 缩略图

- 上传图片时生成缩略图（最长边 320px，JPEG q80），存 `thumb_key`
- 支持解码：JPEG / PNG / GIF（WebP 不生成缩略图，直接原图）
- `file_object` 增加 `thumb_key text`、`category text`
- 列表返回 `thumb_url`（按访问模式生成）；非图片为 null

### 2.5 头像上传

```
POST /auth/profile/avatar   multipart: file
→ { "avatar": "/api/v1/avatars/12", "file_id": 42 }
```

- 校验：仅图片、≤2 MB、生成 256px 方图（居中裁剪）
- `sys_user.avatar` 存**内部路径** `/api/v1/avatars/{user_id}`
- 新增公开接口 `GET /api/v1/avatars/{user_id}`：流式返回头像，`Cache-Control: public, max-age=3600`
  - 两种访问模式下都可用（不依赖桶是否公开）
- 旧的外部 URL 头像继续兼容（直接返回 302 或保持原值）

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 分类 | 图片/PDF/CSV/文本/压缩包各返回正确 category |
| 2 | 筛选 | 按 category 过滤正确；purpose 参数不再作为筛选 |
| 3 | public 链接 | 返回无签名直链，浏览器可访问；`expires_in=0` |
| 4 | private 链接 | 返回带签名 URL，5 分钟后失效 |
| 5 | 配额 | 超过配额返回 413，message 含已用/上限；删除文件后恢复可上传 |
| 6 | usage | 返回与 sum(size_bytes) 一致 |
| 7 | 缩略图 | 上传 PNG/JPEG/GIF 生成缩略图且 `thumb_url` 可访问；WebP 无缩略图 |
| 8 | 头像上传 | 上传后 `/auth/me` 的 avatar 为内部路径；`GET /api/v1/avatars/{id}` 返回图片 |
| 9 | 头像限制 | 非图片 400；>2MB 413 |
| 10 | 兼容 | 旧文件 category/thumb_key 为空时列表正常返回 |
| 11 | 契约 | OpenAPI 更新，双向一致 |
| 12 | 全量 | `verify` 通过 |

## 4. 不做

- 图片压缩质量选项
- 视频缩略图
- 配额按租户总量（本卡只做按用户）

## 实施计划

- [x] 迁移 000039：新增分类、缩略图、访问模式与用户配额，保留物理 purpose 列承载 source，扩充 avatar 入口值；验证 up/down/up。
- [x] 文件上传与列表：入口固定 source，MIME 推导分类并兼容空分类；在上传前与元数据事务内校验配额，提供 usage；测试分类、筛选、并发限额和删除释放。
- [x] 图片与链接：仅用标准库处理 JPEG/PNG/GIF，限制输入和像素数，缩略图最长边 320、JPEG q80，超时降级；按 provider 生成 public 裸链或 private 五分钟链接。
- [x] 头像：受认证入口裁剪 256 方图，文件登记与用户内部路径原子提交；公开流式读取并缓存一小时，兼容旧外部 URL。
- [x] 契约与验收：同步 file/storage/identity OpenAPI 和双向检查；在隔离数据库及真实对象存储逐项验证 §3，运行 lint/test/verify 与迁移全链并清理测试资源。

### 接口表

| 方法 | 完整路径 | 权限 / 行为 |
|---|---|---|
| GET | `/api/v1/admin/files/usage` | `system:file:manage`；当前用户占用、配额、文件数 |
| POST | `/api/v1/auth/profile/avatar` | JWT；multipart file，≤2 MiB，图片，256px 居中方图 |
| GET | `/api/v1/avatars/{user_id}` | 公开；流式图片，缓存 3600 秒；旧外链 302 |
| GET | `/api/v1/admin/files` | `category` 筛选；返回 source/category/thumb_url/uploaded_by_name |
| GET | `/api/v1/admin/files/{id}/url` | public 无签名、expires_in=0；private 预签名、expires_in=300 |

### 验收记录（2026-11-21）

基线 `6e30e00e14ce82534888360034b81efe8aad49d9`，分支 `feat/be-idn-025-file-model`。使用隔离 PostgreSQL 测试数据库和专用 MinIO 容器，不修改开发库的 provider、用户或系统配置。

| §3 | 证据 |
|---|---|
| 1 | `TestFileModelIntegrationCategoryFilterAndLegacy`：PNG=image、PDF=document、CSV/XLSX=sheet、TXT/JSON=text、ZIP=archive；上传均 201，客户端传 purpose=avatar 仍为 source=attachment。 |
| 2 | 同一测试逐项核对六类筛选数量 `[1,1,2,1,2,0]`；带 `purpose=does-not-filter` 仍返回全部 7 条。 |
| 3 | `TestFileModelIntegrationPublicLinksAndThumbnails`：public URL 无 query/signature，原始裸链 GET=200，expires_in=0。 |
| 4 | `FILE_MODEL_EXPIRY_ACCEPTANCE=1` 实等 302 秒：签名 URL 初次 GET=200，去签名 GET=403；同一原始 URL 到期 GET=403，响应 `Request has expired`；expires_in=300。测试耗时 303.92s，通过。 |
| 5 | `TestFileModelIntegrationQuotaUsageAndDeleteRelease`：1 MiB 配额，第二份 600 KiB 文件返回 413/10001，message=`存储空间不足：已用 0 MB / 上限 1 MB`；原幂等请求可重放，删除 204 后被拒的同键请求可上传 201。单元测试证明新幂等键超额时未写对象。 |
| 6 | 同一测试 usage=`{used_bytes:614400,quota_bytes:1048576,file_count:1}`，SQL `SUM(size_bytes)=614400`。并发上传两份 600 KiB，仅一份提交，另一份 413。 |
| 7 | PNG/JPEG/GIF 原图 640×320 → 三个缩略图 URL GET=200、JPEG 320×160；私有缩略图带 300 秒签名，GET=200、320×160，裸链 403；WebP 的 thumb_url=null。原图字节保持不变。 |
| 8 | `TestFileModelIntegrationAvatarModesAndLimits`：public/private 两种模式下，无文件管理权限的登录成员上传 201；预热的 `/auth/me` 缓存立即返回 `/api/v1/avatars/{id}`；匿名 GET=200、JPEG 256×256，`Cache-Control: public, max-age=3600`。文件登记与个人资料写入失败时一起回滚。 |
| 9 | 同一测试在两种模式分别验证非图片 400、超过 2 MiB 返回 413；幂等重放只保留 1 条头像元数据。 |
| 10 | 置 category/thumb_key 为 NULL 后，列表、元数据和分类筛选正常，分类按 MIME 推导，thumb_url=null；旧外部头像 GET=302，保留 Location。 |
| 11 | file/storage/avatar 路由与 OpenAPI 双向一致；响应逐条校验 schema，新增 source/category/thumb_url/uploaded_by_name/usage/provider 字段；identity 中同步头像路径语义。 |
| 12 | 完整 `scripts/verify.ps1` 通过：vet、lint、全量 go test，最后一行 `all checks passed`；bootstrap 423.716s。独立 `make lint` exit=0、`make test` exit=0，47 个包通过。 |

验收命令（存储凭据通过临时进程环境注入，不记入日志）：

```powershell
go test ./internal/modules/file/... ./internal/bootstrap -run '^TestFile(Model|Quota)' -count=1 -v
# ok internal/modules/file; ok internal/bootstrap 6.845s
$env:FILE_MODEL_EXPIRY_ACCEPTANCE='1'
go test ./internal/bootstrap -run '^TestFileModelIntegrationPrivateExpiry$' -count=1 -v -timeout 10m
# ok internal/bootstrap 304.780s
Remove-Item Env:FILE_MODEL_EXPIRY_ACCEPTANCE
go test ./internal/kernel/db -run '(TestMigrateIntegration|TestMigrationIntegration)' -count=1 -v
# PASS; ok internal/kernel/db 9.365s
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
# Source 646 = shards 645 + excluded 1
# PASS: every integration test has exactly one shard or an explicit exclusion.
```

迁移证据：000039 单步 down=38、up=39、dirty=false；已有 provider 默认 public/1024，两个 file_object 新列允许 NULL。全链 `TestMigrateIntegrationReversibleAndRepeatable` 执行 up、逐步 down 到底、再次 up；并发启动和关键结构不变量均 PASS。

边界：缩略图输入最多 4 MiB、400 万像素，生成及写入使用 1500ms 上下文预算；过大、不可解码或生成超时的图片保留原图。头像仅接收可解码 JPEG/PNG/GIF，≤2 MiB、400 万像素。保留物理 purpose 列及旧 snapshot/product_image 数据兼容，API 暴露 source，product_image 投影为 shop。未新增第三方依赖。

### 自检与清理

- [x] `make lint`：最后一行 `golangci-lint run`，exit=0。
- [x] `make test`：47 个包 `ok`，最后一行 `ok github.com/omwk/go-admin-api/scripts/smoke (cached)`，exit=0。
- [x] `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1`：`all checks passed`。
- [x] 迁移 up/down/up 与全链回滚、并发、结构不变量：见上方命令及 §3.12 记录。
- [x] 新增接口已记录在本卡接口表。
- [x] `rg -n 'TODO|not implemented' internal/ -g '*.go'`：0 命中。
- [x] 日志检查：逐项检查上传/头像/存储错误及审计字段，修改的 Go 文件无新增打印；对全部本卡日志扫描临时存储 access/secret，0 命中；过期证据不打印签名或凭据。
- [x] `go run "$env:TEMP/be-idn-020-size.go" -- <全部变更及新增 Go 文件>`（Go AST 函数/文件行数检查）：`files=44 max_file_lines=276 max_function_lines=71`；`gofmt -l` 和 `git diff --check` 均无输出。

已清理：隔离测试数据库由 fixture 在 t.Cleanup 中销毁（测试全部通过）；独立 MinIO `/data` 仅剩 `.minio.sys`，无测试桶。`docker rm -f -v be-idn-025-minio-e37e388b` → 成功；临时存储凭据脚本已删除。开发库未写入本卡验收记录，未新增开发库审计，无临时用户/租户或待恢复配置；保留的测试业务数据：无。

日志目录：`%TEMP%/be-idn-025-1f61aa6cb66f48e4b46c9dddb8213641/`，最终证据为 `acceptance-final.log`、`expiry-final.log`、`migrations-final.log`、`verify-final.log`、`make-test-final.log`、`make-lint-final.log`、`test-groups-final.log`。本机 CGO_ENABLED=0，未执行 race；合并前仍需 CI 的 race 结果。

偏差说明：无。未进行前端工作；工作区保留实现，未提交或推送。

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-025-file-model.md。

分支：feat/be-idn-025-file-model（从最新 main 6e30e00 开）

必读：本卡、internal/modules/file/*（file.go、library.go、validation.go）、
internal/infra/storage/*（s3.go、file_object.go、resolver.go）、
internal/modules/system/storage*（provider 字段）、
internal/modules/identity/account_profile.go（头像）、
migrations/（file_object、storage_provider、sys_user.avatar）

改动：
1. 迁移 000039：file_object.category / thumb_key；storage_provider.access_mode /
   quota_per_user_mb；sys_user.avatar 语义（内部路径）
2. 上传：source 由入口决定，category 由 MIME 推导，图片生成 320px 缩略图
3. 链接：public 返回无签名直链（expires_in=0），private 返回预签名
4. 配额：上传前校验每用户占用，超限 413；新增 GET /admin/files/usage
5. 头像：POST /auth/profile/avatar（仅图片、≤2MB、256px 方图）+
   GET /api/v1/avatars/{user_id}（公开、缓存 1 小时）
6. 列表接口返回 category、thumb_url、uploaded_by_name；筛选参数改为 category
7. OpenAPI 同步

约束：
- 头像不依赖桶是否公开
- 缩略图生成不得阻塞主请求超过 2 秒（大图先拒绝或降级为不生成）
- 旧数据兼容：category 为空时按 MIME 现算

验收按卡片 §3 十二条逐条贴证据：
- 第 3、4 条贴 public 裸链 200 与 private 过期后 403
- 第 7 条贴缩略图 URL 的尺寸
完成后停下报告，不要做前端。
```
