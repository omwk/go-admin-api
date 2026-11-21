# BE-IDN-027 验收证据

日期：2026-11-21（Asia/Shanghai）。分支：`feat/be-idn-027-put-size`，基线：`8d9e65b`。

## §4 逐条验收

| # | 证据 |
|---|---|
| 1 | `photo.png` 1349 B，真实 HTTP multipart POST → `201 code=0`，响应含 `thumb_url`；原图 GET → 200，逐字节一致。 |
| 2 | PNG 的 `thumb_url` → 200，JPEG 320×160，1480 B；与独立生成的预期 JPEG 以及直接对象 GET 逐字节一致。 |
| 3 | 头像 POST → **201**（保持已有 API/OpenAPI 契约），匿名 GET `/api/v1/avatars/2` → 200；JPEG 256×256、1619 B，与独立预期 JPEG 和对象 GET 逐字节一致。卡片写作 200 的差异见下文。 |
| 4 | JPEG 3955 B、GIF 54009 B，各上传 201；主文件与缩略图均 GET 200；主文件逐字节一致，缩略图均为 320×160 JPEG。 |
| 5 | WebP 42 B 上传 201，`thumb_url=null`，GET 200，逐字节一致。 |
| 6 | 文本 22 B、PDF 14 B，均上传 201、GET 200、逐字节一致；`thumb_url=null`。 |
| 7 | 生产代码 `PutFile(..., -1)` 扫描 0 命中；文件模块及 storage 的 `Put(..., -1)` 扫描守卫通过。独立 CSV 流式导出的既有未知长度调用见下文。 |
| 8 | 使用开发库中加密的默认 provider，只读解密后在隔离测试库复制配置；实际端点 `oss.uuo.me`、bucket `oss`，请求与结果如下。 |
| 9 | 全量 `scripts/verify.ps1` exit 0，最后一行 `all checks passed`；包含 `go vet ./...`、`golangci-lint run`、`go test ./...`。 |

## 真实端点请求与结果

```powershell
go test -count=1 -v ./internal/bootstrap -run '^TestFileStorageIntegrationKnownSizeRoundTrip$' -storage-lookup-config D:/code/go-admin-api/.env
```

使用真实 HTTP server 发出 multipart POST，随后对返回的 URL 发出真实 HTTP GET。完整签名和认证头不输出。

```text
BE-IDN-027 live endpoint=oss.uuo.me bucket=oss; development provider read only; database isolated
POST /api/v1/admin/files multipart file=photo.png bytes=1349 -> HTTP 201 code=0
response: id=1 mime=image/png size_bytes=1349 thumb_url=https://oss.uuo.me/oss/tenants/1/attachment/2026/11/de534238fb874d9d1df3cfed064e6970-photo.png.thumb.jpg [signature omitted]
GET https://oss.uuo.me/oss/tenants/1/attachment/2026/11/de534238fb874d9d1df3cfed064e6970-photo.png.thumb.jpg [signature omitted] -> HTTP 200; JPEG=320x160 bytes=1480 sha256=b942cf31b091beaef8f2588e34640ddd61ac7dcfd61b33d2be1753b564b06fe9; original HTTP 200 bytes=1349 equal=true
POST /api/v1/admin/files multipart file=photo.jpeg bytes=3955 -> HTTP 201 code=0
GET photo.jpeg.thumb.jpg -> HTTP 200; JPEG=320x160 bytes=1480; original HTTP 200 bytes=3955 equal=true
POST /api/v1/admin/files multipart file=photo.gif bytes=54009 -> HTTP 201 code=0
GET photo.gif.thumb.jpg -> HTTP 200; JPEG=320x160 bytes=21277; original HTTP 200 bytes=54009 equal=true
pixel.webp: upload=201 thumb_url=null GET=200 bytes=42 equal=true
notes.txt: upload=201 thumb_url=null GET=200 bytes=22 equal=true
document.pdf: upload=201 thumb_url=null GET=200 bytes=14 equal=true
POST /api/v1/auth/profile/avatar multipart file=avatar.png bytes=1349 -> HTTP 201 code=0
GET /api/v1/avatars/2 -> HTTP 200; JPEG=256x256 bytes=1619 sha256=564c159f1a811247bdb2e4c89af4757f8be56a25e91f561e2e8ca4b87b4968fc equal=true
--- PASS: TestFileStorageIntegrationKnownSizeRoundTrip (47.70s)
    --- PASS: TestFileStorageIntegrationKnownSizeRoundTrip/png (10.25s)
    --- PASS: TestFileStorageIntegrationKnownSizeRoundTrip/jpeg (7.45s)
    --- PASS: TestFileStorageIntegrationKnownSizeRoundTrip/gif (9.40s)
    --- PASS: TestFileStorageIntegrationKnownSizeRoundTrip/pixel.webp (4.10s)
    --- PASS: TestFileStorageIntegrationKnownSizeRoundTrip/notes.txt (4.00s)
    --- PASS: TestFileStorageIntegrationKnownSizeRoundTrip/document.pdf (4.61s)
    --- PASS: TestFileStorageIntegrationKnownSizeRoundTrip/avatar (6.34s)
PASS
ok github.com/omwk/go-admin-api/internal/bootstrap 48.586s
```

修复前，同一真实端点测试的 PNG 与头像 POST 均为 `HTTP 500 code=10002`，分别在 7 秒左右失败。

## 其他检查

```text
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1
== go vet ==
== golangci-lint ==
== go test ==
ok github.com/omwk/go-admin-api/cmd/cli 43.183s
ok github.com/omwk/go-admin-api/internal/bootstrap 427.639s
ok github.com/omwk/go-admin-api/internal/infra/storage 0.175s
ok github.com/omwk/go-admin-api/internal/kernel/db 14.925s
ok github.com/omwk/go-admin-api/internal/modules/file 6.188s
all checks passed

go test -count=1 ./internal/modules/file ./internal/infra/storage
ok github.com/omwk/go-admin-api/internal/modules/file 6.603s
ok github.com/omwk/go-admin-api/internal/infra/storage 6.344s

go test -count=1 -v ./internal/kernel/db -run '^TestMigrateIntegrationReversibleAndRepeatable$'
--- PASS: TestMigrateIntegrationReversibleAndRepeatable (4.27s)
ok github.com/omwk/go-admin-api/internal/kernel/db 4.378s

go test -count=1 -v ./internal/bootstrap -run '^TestFileModelIntegrationMigrationDefaultsAndReversibility$'
BE-IDN-025 migration: down=38; up=39 dirty=false; existing provider defaults public/1024; category/thumb_key nullable
--- PASS: TestFileModelIntegrationMigrationDefaultsAndReversibility (1.74s)
ok github.com/omwk/go-admin-api/internal/bootstrap 1.828s

powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
./internal/bootstrap: TestFileStorageIntegrationKnownSizeRoundTrip -> core
Source 647 = shards 646 + excluded 1
PASS: every integration test has exactly one shard or an explicit exclusion.

rg -n -U 'PutFile\([^;{}]*,\s*-\s*1\s*\)' internal --glob '*.go' --glob '!**/*_test.go'
PutFile(..., -1): 0 matches

rg -n 'TODO|not implemented' internal/
TODO / not implemented: 0 matches

git diff --check
exit 0
```

标准库 `go/parser` / `go/ast` 检查本次新增与修改的 Go 文件：9 个文件，最大文件 307 行，最长函数 71 行，满足 400/80 限制。审查本次 diff 和测试日志：没有新增生产日志，没有输出访问密钥、签名参数、JWT 或用户敏感信息。

迁移验证使用隔离库，执行全链 up → down 到 0 → up，以及文件模型 39 → 38 → 39；开发库 schema 不变。

## 清理

已清理：`cleanupFilePutSize` 对本次成功上传的 7 个文件逐个删除主文件和缩略图，并用对象 GET 确认不存在；再调用 `DELETE /api/v1/admin/files/{id}` → 204。隔离数据库及其中的用户、provider、头像设置和审计由 fixture 删除。

开发库仅用于读取默认 provider，未创建验收数据或修改系统配置，无本卡开发库审计需要 prune；真实 OSS bucket 的策略未修改，不保留测试对象。

## 偏差与限制

- 卡片 §4 #3 将头像上传列为 200，现有 handler、OpenAPI 和集成测试的创建契约均为 201。本次保留 201，头像读取为 200，不改 API 契约。
- CSV 导出是独立的 `catalog/port.ObjectStore`，长度由生成流确定，既有 `S3.Put` 显式配置 `PartSize: 5 << 20, NumThreads: 1`，符合卡片 §3 #2 的未知长度要求。本次不改导出链路；其生产调用及三个测试中的 `Put(..., -1)` 保留，文件上传链路与所有 `PutFile` 调用均无 -1。
- 真实端点验收期间出现过缩略图上传瞬时超时；单次 PUT 诊断接近现有 1.5 秒预算。没有扩大该时限或增加重试，最终完整七场景验收通过；公网波动仍可能触发既有时限。
