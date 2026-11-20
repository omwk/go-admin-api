# BE-IDN-021 · MinIO 存储配置与文件管理

- ID: BE-IDN-021
- 类型: 新增
- 仓库: 后端
- 状态: 已完成（本地验收通过）
- 预计: 2.5 人日
- 分支: `feat/be-idn-021-storage`
- 依赖: 无

---

## 1. 需求（2026-11-20 确认）

1. MinIO 配置**存数据库**，不写环境变量
2. Secret **接口返回明文**，方便管理员查看与修改（仅平台管理员）
3. **支持多个配置**，可切换默认
4. 所有上传（店铺文件、导出、文件库）统一走 MinIO

## 2. 数据模型

新表 `storage_provider`（迁移 `000037`）：

```sql
CREATE TABLE storage_provider (
  id bigserial PRIMARY KEY,
  tenant_id bigint NOT NULL REFERENCES tenant(id),   -- 平台租户
  name text NOT NULL,                                 -- 显示名，如「主 MinIO」
  endpoint text NOT NULL,                             -- host 或 https://host[:port]
  port int NOT NULL DEFAULT 443 CHECK (port BETWEEN 1 AND 65535),
  bucket text NOT NULL,
  use_ssl boolean NOT NULL DEFAULT true,
  region text NOT NULL DEFAULT 'us-east-1',
  access_key text NOT NULL,
  secret_enc bytea NOT NULL,                          -- 加密存储
  public_base_url text NOT NULL DEFAULT '',
  is_default boolean NOT NULL DEFAULT false,
  enabled boolean NOT NULL DEFAULT true,
  created_at, updated_at timestamptz NOT NULL DEFAULT now()
);
-- 每个租户至多一个默认
CREATE UNIQUE INDEX storage_provider_default ON storage_provider(tenant_id) WHERE is_default;
```

`file_object` 增加 `provider_id bigint`（迁移同卡），记录上传时使用的配置，
避免切换默认后旧文件无法下载。

## 3. 接口（平台管理员）

```
GET    /admin/storage-providers                列表（secret 返回明文）
POST   /admin/storage-providers                新建
GET    /admin/storage-providers/{id}           详情（明文）
PUT    /admin/storage-providers/{id}           修改
DELETE /admin/storage-providers/{id}           删除（默认配置不可删，需先切换）
POST   /admin/storage-providers/{id}/test      测试连接
POST   /admin/storage-providers/{id}/default   设为默认
```

响应示例：

```json
{ "id": 1, "name": "主 MinIO", "endpoint": "oss.uuo.me", "port": 443,
  "bucket": "oss", "use_ssl": true, "region": "us-east-1",
  "access_key": "0G0…", "secret_key": "pM+ma…（明文）",
  "public_base_url": "", "is_default": true, "enabled": true }
```

安全控制：

- 仅平台管理员（`system:storage:manage`）可读写；其他角色 404
- 返回明文是**有意的产品决定**（方便查看/修改），依赖权限 + HTTPS + 审计
- 审计 `storage.provider.create/update/delete/test`，`after` 中 secret 记为 `***`
- 列表与详情响应 `Cache-Control: no-store`

## 4. 统一存储客户端

- 只保留 `infra/storage/s3.go`（minio SDK），**删除 shop 的手写 HTTP 实现**
- `endpoint` 归一化：`host` 或 `https://host[:port]` → SDK 需要 host + Secure
- `port` 参与归一化（443 + use_ssl → https，9000 通常 http）
- 店铺文件、导出、文件库全部通过 `storage.Resolver` 取默认配置
- **不读环境变量**：未配置任何 provider 时，上传返回 409「对象存储未配置」

## 5. 文件库 API

```
POST   /admin/files          multipart: file, purpose
GET    /admin/files          ?purpose=&keyword=&page=&page_size=
GET    /admin/files/{id}       流式下载
GET    /admin/files/{id}/url   预签名，5 分钟
DELETE /admin/files/{id}
```

规则：

- 20MB 上限；MIME 白名单：`image/*`、PDF、CSV、XLSX、ZIP
- 对象键：`tenants/<tenant>/<purpose>/<yyyy>/<mm>/<random>-<safe-name>`
- purpose：`attachment` / `export` / `shop` / `snapshot`
- SHA256、租户隔离、审计 `file.upload` / `file.delete`
- 下载时按 `file_object.provider_id` 解析对应配置；为空时用默认

## 6. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 新建 | 201，列表中可见 |
| 2 | 明文返回 | GET 中 secret 为明文，与提交一致 |
| 3 | 加密存储 | 数据库 `secret_enc` 为密文，无明文 |
| 4 | 多配置 | 可建 2 条；默认唯一 |
| 5 | 切换默认 | 设为默认后旧默认自动取消 |
| 6 | 默认保护 | 删除默认 → 409；切换后可删 |
| 7 | 测试连接 | 正确 → ok；错误密钥 → access_denied；桶不存在 → bucket_missing |
| 8 | 免重启 | 保存后立即上传成功 |
| 9 | 上传 | 20MB 内成功；超限 413；非白名单 400 |
| 10 | 下载 | 流式与预签名均可用；切换默认后旧文件仍可下载（provider_id 生效） |
| 11 | 隔离 | A 租户读/删 B 租户文件 → 404 |
| 12 | 未配置 | 无 provider 时上传 → 409「对象存储未配置」 |
| 13 | 权限 | 非平台管理员访问 provider 接口 → 404 |
| 14 | 审计 | create/update/delete/test 各一条，secret 不出现 |
| 15 | 契约 | OpenAPI 更新，双向一致 |
| 16 | 全量 | `verify` 通过 |

## 7. 不做

- 租户自助配置存储（仅平台）
- 每租户独立桶
- 多配置自动分片/负载均衡（只做默认切换）
- 图片处理

## 8. Codex prompt

```
实现 docs/tasks/BE-IDN-021-storage.md。

分支：feat/be-idn-021-storage（从最新 main 5cd5d12 开）

必读：本卡、internal/infra/storage/s3.go、
internal/modules/shop/file.go 与 file_http.go（手写 HTTP 实现，需删除）、
catalog_export_runtime.go、internal/kernel/setting/（权限与审计参考）、
sealer 用法、migrations/（file_object、sys_setting）

改动：
1. 迁移 000037：storage_provider 表；file_object.provider_id
2. provider CRUD + test + set-default（secret 明文返回，加密存储）
3. 统一到 minio SDK，删除手写 HTTP 实现；endpoint 归一化；
   店铺文件/导出/文件库共用 Resolver；无 provider 时 409
4. 新增 internal/modules/file 文件库 API（20MB、白名单、租户隔离、预签名、审计）
5. 新增权限码 system:storage:manage 与 system:file:manage（seed 授予内置超管）
6. OpenAPI 同步

约束：
- 不读环境变量获取存储配置
- secret 加密存储；响应明文但禁止进入日志/审计
- 不跨模块 import 其他模块 repo/model

验收按卡片 §6 十六条逐条贴证据：
- 第 2、3 条同贴：响应明文 + 数据库密文查询
- 第 8 条：保存后不重启立即上传
- 第 10 条：切换默认后旧文件仍可下载
完成后停下报告，不要做前端。
```

## 9. 实现与验收记录（2026-11-20）

实际基线为本次指令指定的 `main d290619`，分支 `feat/be-idn-021-storage`。本卡仅实现后端。
Provider 的 7 个接口和文件库的 5 个接口分别同步至 `api/openapi/storage.yaml` 与 `api/openapi/file.yaml`。
既有 shop 文件测试迁入 file 模块并保留原行为断言；手写 HTTP 对象存储适配器已删除。

验收在自动销毁的 PostgreSQL 模板克隆库和临时 MinIO 容器中执行。测试每次生成随机凭据；
以下响应原值断言与密文查询属于同一个请求/记录，不输出 secret 本身。

```text
$ go test ./internal/bootstrap -run '^TestFileStorageIntegration' -count=1 -v
PASS
ok github.com/omwk/go-admin-api/internal/bootstrap 5.097s
```

| # | 验收结果与证据 |
|---|---|
| 1 | `TestFileStorageIntegrationProviderCRUDEncryptionAndAudit`：POST 201，GET 列表包含新记录；创建幂等重放返回同一 ID。 |
| 2 | 同一测试：创建、列表、详情的 `secret_key` 均与请求原值相等，详情与列表均 `Cache-Control: no-store`；与第 3 条联合证据见下方。 |
| 3 | 同一测试：查询 `secret_enc` 为非空密文，不包含提交的 secret 字节；幂等响应快照也加密。 |
| 4 | 同一测试创建两条配置，读取旧记录断言其 `is_default=false`；迁移提供 `storage_provider_default` 部分唯一索引。 |
| 5 | 同一测试调用 `POST /storage-providers/{id}/default` 返回 200，旧默认自动取消。 |
| 6 | 同一测试删除当前默认 409，切换默认后删除原配置 204；删除幂等重放仍为 204。 |
| 7 | `TestFileStorageIntegrationMinIOLiveConfigurationAndHistoricalDownload`：真实 MinIO 正确凭据 `ok`、随机错误密钥 `access_denied`、不存在的桶 `bucket_missing`。 |
| 8 | 同一测试在同一运行中的 router 上保存配置后立即上传 201；没有重建服务或重启；数据库 `file_object.provider_id` 等于该配置 ID。 |
| 9 | `TestFileStorageIntegrationMinIOUploadLimitsAndMIME`：图片、PDF、CSV、XLSX、ZIP 上传成功；精确 20 MiB 返回 201，20 MiB+1 返回 413，脚本和 ZIP 伪装 PNG 返回 400；用途/关键词过滤与分页通过。 |
| 10 | 历史下载测试切换到另一默认后，旧文件流式下载和 300 秒预签名下载均逐字节一致；导出测试也验证 provider_id 固化和历史桶清理。 |
| 11 | 历史下载测试创建另一有效租户与超管，使用其真实令牌 HTTP GET/DELETE 原租户文件均返回 404；service 路径也断言 404。 |
| 12 | `TestFileStorageIntegrationUnconfiguredAndPermissions`：无 provider 上传返回 409，消息为「对象存储未配置」。 |
| 13 | 同一测试普通角色访问 provider 写读接口均 404；历史下载测试中外租户超管也为 404；seed 给内置超管授予两个 manage 权限。 |
| 14 | CRUD 测试逐动作核对 create/update/delete 条数，连接测试核对三次 test 审计；所有 before/after 不含提交 secret，使用 `***`；文件 upload/delete 审计也核对条数。 |
| 15 | `TestFileStorageOpenAPIBidirectionalContract` 核对 12 个操作、权限、安全声明与 OpenAPI 双向一致；HTTP 验收同时验证 JSON 响应 schema。 |
| 16 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1` 返回 0，末行为 `all checks passed`；bootstrap 全量测试 `399.098s`。另执行 `make lint`、`make test` 均返回 0。 |

第 2、3 条同一记录的实际输出：

```text
GET secret_key equals submitted plaintext=true
SELECT encode(secret_enc,'hex') FROM storage_provider WHERE id=?
7b22636970686572223a22374576383243776a46773151706d30575748414a323134664b43623565435468494c77522f544946595234744f2b52534f4846642b6238337063554a656e52482b342f326c7235564a49356467336a6549705549756c4f793473394a6a5a644a712b5662797a43634d56773d222c226e6f6e6365223a22774b45747a7341775875624c4e676c48222c2276657273696f6e223a317d
```

文件删除在同一事务中删除元数据、写审计和 `file.cleanup` outbox，worker 在提交后执行对象删除。
`TestFileStorageIntegrationMinIODeleteRetriesAfterCommit` 验证事务内拒绝对象 IO、失败后重试、幂等删除只产生一条审计/清理事件，
并在编辑原 provider 桶之后仍按事件中的历史桶清理。未完成清理的 provider 删除返回 409。

迁移验证（隔离库，不对开发库执行 down）：

```text
$ go test ./internal/kernel/db -run 'TestMigrateIntegration' -count=1 -v
--- PASS: TestMigrateIntegrationReversibleAndRepeatable (4.71s)
--- PASS: TestMigrateIntegrationConcurrentStartup (1.79s)
ok github.com/omwk/go-admin-api/internal/kernel/db 7.342s
```

该命令覆盖全链 up、重复 up、down 至 0 再 up、结构不变量与并发启动。
本机 CGO 关闭，race 结果需在 CI 验证；当前没有 CI race 通过声明。

偏差说明：无需求偏差。保持既有权限登记位置，新权限追加到末尾；仅后端的两个权限按既有机制登记菜单投影豁免。
新审计 action 同步登记于 `docs/design/audit-events.md`，没有删除或放宽既有断言。

最终验证输出：

```text
$ powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1
ok github.com/omwk/go-admin-api/internal/bootstrap 399.098s
all checks passed
verify exit=0
$ make lint
golangci-lint run
exit=0
$ make test
ok github.com/omwk/go-admin-api/cmd/cli 33.798s
ok github.com/omwk/go-admin-api/internal/bootstrap (cached)
ok github.com/omwk/go-admin-api/internal/modules/file (cached)
ok github.com/omwk/go-admin-api/scripts/smoke (cached)
exit=0
```

自检：

- [x] `make lint` 与 `make test` 通过，输出见上方。
- [x] 迁移全链 up / down / up 与并发启动通过，命令见上方。
- [x] 新增 12 个接口已记录于本卡接口表及 OpenAPI。
- [x] `rg -n 'TODO|not implemented' internal/` 无匹配。
- [x] 审计逐条验证不包含提交的 secret；请求/错误响应不记录凭据；存储运行时代码不读取环境配置。
- [x] 所有改动 Go 文件均满足文件 ≤400 行、函数 ≤80 行，Go AST 检查输出 `File <=400 / function <=80: PASS`。实际检查命令：

```powershell
$storageChanged=@(git diff --name-only --diff-filter=ACM 2>$null)+@(git ls-files --others --exclude-standard)
$storageGo=@($storageChanged | Where-Object { $_.EndsWith('.go') })
& (Join-Path $env:TEMP 'be-idn-021-storage/check_lengths.exe') $storageGo
```

集成测试分片守卫通过：`Source 626 = shards 625 + excluded 1`；新增测试属于 core 的 File 域。

已清理：`testenv.Database` 的 `t.Cleanup` → 本次隔离克隆库/迁移 schema 随测试销毁；
`docker stop codex-be-idn-021-minio` → 临时容器自动移除（随后 `docker ps -a` 查询为空）；
`Remove-Item -LiteralPath <临时目录>/minio.env, <临时目录>/minio.json` → 两项 `Test-Path` 均为 `False`。
未向开发库写入本卡验收数据或修改开发配置，无需对开发库执行审计清理；未保留测试租户、用户、配置或对象。
仅保留不含明文凭据的验收日志与检查工具，供复核使用。
