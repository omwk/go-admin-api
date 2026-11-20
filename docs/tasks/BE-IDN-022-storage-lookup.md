# BE-IDN-022 · 对象存储下载修复（Bucket Lookup 模式）

- ID: BE-IDN-022
- 类型: 修复
- 仓库: 后端
- 状态: 已完成（验收通过）
- 预计: 0.3 人日
- 分支: `feat/be-idn-022-storage-lookup`
- 依赖: BE-IDN-021（已合并）
- 发现方式: 合并后用真实 MinIO 手工验证

---

## 1. 现象

用真实 MinIO（`oss.uuo.me`，桶 `oss`）验证：

| 操作 | 结果 |
|---|---|
| 上传（`POST /admin/files`） | ✅ 201 |
| 预签名 URL（`GET /files/:id/url`） | ✅ 200，直接访问返回 47 字节 |
| **流式下载（`GET /files/:id`）** | ❌ **500 `export object unavailable`** |
| 删除 | ✅ 204 |

## 2. 根因（已用探针证实）

`infra/storage/s3.go` 创建 minio 客户端时**没有设置 `BucketLookup`**，默认 `Auto`。
对自定义域名（`oss.uuo.me`）Auto 会走 virtual-host 或区域探测，导致 HEAD 失败：

```
lookup Auto      → stat 失败：Access Denied
lookup Path      → stat 成功：size=14
lookup DNS       → dial tcp: lookup oss.oss.uuo.me: no such host
```

同一对象用路径风格预签名可以正常读取（`https://oss.uuo.me/oss/...`），
证明对象存在、密钥正确，**只是客户端寻址方式不对**。

同时说明上传路径与下载路径用的不是同一个客户端（上传成功、下载失败）。

## 3. 改动

1. `storage.S3` 创建客户端时设置：

```go
minio.New(host, &minio.Options{
    Creds:        credentials.NewStaticV4(ak, sk, ""),
    Secure:       useSSL,
    BucketLookup: minio.BucketLookupPath,   // 自定义域名/MinIO 场景
})
```

2. provider 增加可选字段 `path_style`（默认 true）：
   - `true`（默认）：`BucketLookupPath`，适配 `oss.uuo.me/oss/...`
   - `false`：`Auto`，适配 AWS S3 等虚拟主机风格
3. 上传、下载、预签名、导出、店铺文件**必须使用同一个客户端**（由 Resolver 统一创建并缓存）。
4. 新增回归测试：路径风格端点下，上传 → 下载 → 预签名 三者内容一致。

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 流式下载 | 真实 MinIO 下 `GET /files/:id` 200，字节与上传一致 |
| 2 | 预签名 | 仍可用 |
| 3 | 统一客户端 | 上传/下载/预签名/导出使用同一 Resolver 实例 |
| 4 | 虚拟主机风格 | `path_style=false` 时对 AWS 风格端点可用（隔离测试） |
| 5 | 迁移 | 000038 增加 `storage_provider.path_style boolean default true` |
| 6 | 契约 | OpenAPI 增加 path_style |
| 7 | 全量 | `verify` 通过 |

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-022-storage-lookup.md。

分支：feat/be-idn-022-storage-lookup（从最新 main 823b8a2 开）

必读：本卡、internal/infra/storage/s3.go、resolver.go、provider.go、
internal/modules/file/file.go、api/openapi/storage.yaml

改动：
1. 迁移 000038：storage_provider.path_style boolean NOT NULL DEFAULT true
2. S3 客户端按 path_style 设置 BucketLookup（默认 Path）
3. 上传/下载/预签名/导出/店铺文件共用 Resolver 创建的同一个客户端
4. 回归测试：路径风格端点下 上传→下载→预签名 内容一致
5. OpenAPI 同步

约束：
- 不改变已有对象键格式
- 不读环境变量

验收按卡片 §4 七条逐条贴证据：
- 第 1 条用真实 MinIO（oss.uuo.me / 桶 oss）实测并贴字节对比
完成后停下报告，不要做前端。
```

## 6. 实施计划（2026-11-20）

起点：`823b8a2`；分支：`feat/be-idn-022-storage-lookup`。

- [x] 先增加寻址风格与 Resolver 复用回归测试，运行并确认失败。
- [x] 新增 000038 up/down；贯通 Provider、StorageInput、数据库读写与审计的 `path_style`，省略时为 true，显式 false 保留 Auto。
- [x] S3 两个构造入口明确设置 BucketLookup；Resolver 按有效配置缓存客户端，配置变更替换缓存，仍逐次查询默认配置与历史 provider。
- [x] API 文件、店铺文件、商品/订单导出和 worker 清理注入同一进程内的 Resolver；保留历史桶与对象键格式。
- [x] 补齐路径风格上传/下载/预签名字节一致性、AWS 风格隔离测试、配置更新与并发缓存测试；同步 OpenAPI。
- [x] 执行 verify、lint/test、集成分片守卫、迁移全链与 up/down/up；真实 MinIO `oss.uuo.me`/`oss` 经 HTTP handler 验证并记录字节数及 SHA-256，清理本卡对象与隔离测试数据。

## 7. 接口变更

无新增路由；现有接口的增量契约如下。

| 接口 | 变更 |
|---|---|
| `POST /api/v1/admin/storage-providers` | 请求可选 `path_style`，省略为 true；响应包含该字段 |
| `PUT /api/v1/admin/storage-providers/:id` | 请求可选 `path_style`，显式 false 保留 Auto；响应包含该字段 |
| `GET /api/v1/admin/storage-providers`、`GET /api/v1/admin/storage-providers/:id` | Provider 响应增加必填 boolean `path_style` |

## 8. 验收记录（2026-11-20）

### §4 七项证据

| # | 验收结果与证据 |
|---|---|
| 1 | 真实 `oss.uuo.me` / `oss`：上传 201；`GET /api/v1/admin/files/:id` 200；上传/流式下载均为 **46 字节**，`bytes.Equal=true`，下列 SHA-256 完全一致。显式 `-count=3` 连续三轮通过 |
| 2 | 预签名接口 200；实际访问签名 URL 返回 200、46 字节，`bytes.Equal=true`，SHA-256 同上传；未输出签名 URL 或凭据 |
| 3 | `require.Same` 验证 Default/历史 provider、文件（purpose=shop）与导出绑定复用 Resolver 缓存的对象；16 并发解析复用同一客户端；配置或密钥变更替换缓存；API/worker 各进程只创建一个 Resolver，分别注入文件、商品/订单导出及清理链路 |
| 4 | 隔离 SDK 签名测试和 DB/API 测试通过：`path_style=false` 真实持久化，签名 host=`storage-test.s3.dualstack.us-east-1.amazonaws.com`、path=`/tenants/1/shop/lookup.csv`；true 使用原 host 与桶路径。该项不访问 AWS 网络 |
| 5 | 000038 up/down 新文件；隔离库 down=37 → up=38、dirty=false，原行默认 true；列断言 boolean / NOT NULL / DEFAULT true；另有完整 up→down 到 0→up、并发启动、结构不变量全部通过 |
| 6 | `api/openapi/storage.yaml` 输入可选、默认 true，Provider 响应必填；配置集成测试经现有 OpenAPI 响应校验器验证创建/更新及显式 false |
| 7 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1` exit=0，最后一行 `all checks passed`；vet、lint、全量 `go test ./...` 全部通过；另执行 `make lint` / `make test` 均 exit=0 |

```text
upload bytes=46
stream GET=200 bytes=46 equal=true
presigned GET=200 bytes=46 equal=true
upload_sha256 = 292cd44c0ce0107f9edfe7ecc2e3de975b53241fdcf8bfd24e33b25bd0ca11bc
stream_sha256 = 292cd44c0ce0107f9edfe7ecc2e3de975b53241fdcf8bfd24e33b25bd0ca11bc
presign_sha256 = 292cd44c0ce0107f9edfe7ecc2e3de975b53241fdcf8bfd24e33b25bd0ca11bc
```

验收命令与输出（省略普通日志和随机隔离库名）：

```text
$ go test ./internal/bootstrap -run '^TestFileStorageIntegrationLivePathStyleLookup$' -count=3 -v -args -storage-lookup-config ../../.env
--- PASS: TestFileStorageIntegrationLivePathStyleLookup (13.00s)
--- PASS: TestFileStorageIntegrationLivePathStyleLookup (15.23s)
--- PASS: TestFileStorageIntegrationLivePathStyleLookup (14.17s)
ok  github.com/omwk/go-admin-api/internal/bootstrap 42.513s

$ go test ./internal/bootstrap -run '^TestFileStorageIntegrationPathStyle' -count=1 -v
BE-IDN-022 #3/#4/#6: live DB default=true; explicit false persisted; file/export bindings share cached client; AWS Auto presigns virtual-host URL
BE-IDN-022 #5: down=37, up=38 dirty=false; existing row path_style=true; boolean NOT NULL DEFAULT true
ok  github.com/omwk/go-admin-api/internal/bootstrap 1.381s

$ go test ./internal/infra/storage -count=1
ok  github.com/omwk/go-admin-api/internal/infra/storage 0.748s
$ go test -race ./internal/infra/storage -count=1
ok  github.com/omwk/go-admin-api/internal/infra/storage 2.007s

$ go test ./internal/kernel/db -run '^(TestMigrateIntegration|TestMigrationIntegration)' -count=1 -v
--- PASS: TestMigrateIntegrationReversibleAndRepeatable (5.49s)
--- PASS: TestMigrateIntegrationConcurrentStartup (1.59s)
--- PASS: TestMigrationIntegrationPartitionInvariants (0.95s)
--- PASS: TestMigrationIntegrationTenantAndIdempotencyUniqueness (0.14s)
--- PASS: TestMigrationIntegrationVersionDefaultsAndCompositeKeys (0.13s)
ok  github.com/omwk/go-admin-api/internal/kernel/db 8.406s

$ make lint
golangci-lint run
exit=0

$ powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1
== go vet ==
== golangci-lint ==
== go test ==
ok  github.com/omwk/go-admin-api/internal/bootstrap 410.315s
ok  github.com/omwk/go-admin-api/internal/infra/storage 0.161s
all checks passed

$ make test
go test ./...
ok  github.com/omwk/go-admin-api/cmd/cli 36.323s
ok  github.com/omwk/go-admin-api/internal/bootstrap (cached)
ok  github.com/omwk/go-admin-api/internal/infra/storage (cached)
ok  github.com/omwk/go-admin-api/scripts/smoke (cached)
exit=0

$ powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
Source 629 = shards 628 + excluded 1
PASS: every integration test has exactly one shard or an explicit exclusion.

$ rg -n 'TODO|not implemented' internal/
无匹配
$ git diff --check
exit=0
```

日志检查：检查新增生产代码 diff，无新增日志调用或环境变量读取；审计仅增加 boolean `path_style`，密钥字段沿用 `***`。真实验收只输出状态码、字节数、哈希和共享对象断言；不输出密钥或签名 URL。测试凭据由随机数生成；临时诊断代码已删除。

尺寸检查：PowerShell 遍历 `git diff --name-only -- '*.go'` 与 `git ls-files --others --exclude-standard -- '*.go'`，统计文件行数和 gofmt 顶层 `func` 到结束括号的跨度，超出 400/80 即抛错：`Go size guard: files=20, max_file=267 lines, max_function=78 lines`。

可复现的尺寸守卫（提交后将 `git diff HEAD` 的基线换成 `823b8a2`）：

```powershell
$paths = @((git diff HEAD --name-only -- '*.go'), (git ls-files --others --exclude-standard -- '*.go')) |
    ForEach-Object { $_ } | Sort-Object -Unique
foreach ($path in $paths) {
    $lines = @(Get-Content -LiteralPath $path)
    if ($lines.Count -gt 400) { throw "file too long: $path" }
    $start = -1
    for ($i = 0; $i -lt $lines.Count; $i++) {
        if ($lines[$i] -match '^func ') { $start = $i }
        if ($start -ge 0 -and $lines[$i] -eq '}') {
            if ($i - $start + 1 -gt 80) { throw "function too long: ${path}:$($start + 1)" }
            $start = -1
        }
    }
}
```

已清理：真实验收的 t.Cleanup → SDK 删除本卡远端对象并确认不可读取，DELETE 文件 API 删除元数据，fixture 删除隔离数据库及审计；三轮均输出 cleanup 成功。开发库仅读取既有加密 provider，没有新增租户、用户、配置或审计；未保留本卡测试数据。

## 9. 偏差说明

- 寻址与复用修复后，真实端点仍出现额外 HEAD 失败、GET 可读的情况。`S3.Get` 改为通过同一个缓存 SDK 客户端的 `Core.GetObject`，在一次签名 GET 中取得流和内容长度；保留原端口、错误消息、对象键及外部 API。回归端点拒绝 HEAD，验证流式下载仍可用，修复前红、修复后绿。
- Auto 模式构造 SDK 前去掉 443/80 默认端口，避免固定版本 SDK 将带默认端口的 AWS 域名识别为普通端点；Path 模式保留原规范化端口。无新增依赖、Go 升级或前端改动。
