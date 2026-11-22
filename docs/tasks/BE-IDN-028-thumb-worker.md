# BE-IDN-028 · 大图缩略图异步生成

- ID: BE-IDN-028
- 类型: 修复
- 仓库: 后端
- 状态: 已实现并验收
- 预计: 1.5 人日
- 分支: `feat/be-idn-028-thumb-worker`
- 依赖: BE-IDN-025/027
- 触发：用户实测 3.4 MB 图片有缩略图，12.3 MB 的 `thumb_url` 为 null

---

## 1. 现状

`internal/modules/file/image.go` 三个硬限制：

```go
imageInputLimit  = 4 << 20     // 4 MiB
imagePixelLimit  = 4_000_000   // 400 万像素
thumbnailBudget  = 1500ms
```

超过任一限制 → `renderImage` 返回错误 → `thumbnail()` **静默返回空**：

```go
thumbnail, err := renderImage(ctx, data, 320, false)
if err != nil {
    return "", nil      // 不支持/损坏/过大 → 保留原图，不报错
}
```

结果：手机拍的 12 MB 照片上传成功，但永远没有缩略图，且用户无任何提示。

## 2. 目标

| 图片 | 行为 |
|---|---|
| ≤4 MiB 且 ≤400 万像素 | 同步生成（现有行为，上传即返回 `thumb_url`） |
| 更大 | 上传立即成功，返回 `thumb_status=pending`；后台生成后变 `ready` |
| 解码失败/不支持 | `thumb_status=failed`，不无限重试 |

## 3. 改动

### 3.1 字段

迁移 `000040`：`file_object.thumb_status text NOT NULL DEFAULT ''`
取值：`''`（不需缩略图）/ `pending` / `ready` / `failed`

`thumb_key` 保持不变。

### 3.2 上传路径

```
图片且（≤4 MiB 且 ≤400 万像素）→ 同步生成 → thumb_status=ready
图片且超出            → thumb_status=pending，入队 asynq 任务 thumb:generate
非图片                → thumb_status=''
```

### 3.3 后台任务

新增 worker 任务 `thumb:generate`：

- 载荷：`{tenant_id, file_id}`
- 超时 60 秒；像素上限提高到 **4000 万**；输入上限提高到 **上传上限（20 MiB）**
- 成功后写 `thumb_key`、`thumb_status=ready`
- 失败（解码失败/不支持）→ `thumb_status=failed`，**不重试**
- 死信/异常 → `pending` 保持不变，由清理任务的兜底逻辑处理（见 3.5）
- 并发：worker 侧限制该任务并发为 2，避免内存峰值叠加

### 3.4 响应

`GET /admin/files` 与上传响应增加：

```json
{ "thumb_url": null, "thumb_status": "pending" }
```

`thumb_url` 仅在 `ready` 时非空。

### 3.5 兜底

新增小时级任务：扫描 `thumb_status=pending` 且 `created_at < now()-10min` 的记录，
重新入队；同一文件最多重试 3 次（`thumb_attempts` 计数，随迁移一起加）。

### 3.6 头像不适用

头像仍要求 ≤2 MiB、≤400 万像素，同步生成，超限直接 400（现状正确）。

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 小图 | ≤4 MiB 图片上传后立即有 `thumb_url`，`ready` |
| 2 | 大图 | 12 MB 图片上传 201，`thumb_status=pending`，`thumb_url=null` |
| 3 | 后台生成 | 数秒后重查 `thumb_status=ready`，`thumb_url` 200 且尺寸 ≤320 |
| 4 | 失败 | 损坏的大文件 → `failed`，不重试 |
| 5 | 兜底 | 人为滞留的 pending 记录被兜底任务重新入队 |
| 6 | 上限 | 4000 万像素内大图成功；超过 → `failed` |
| 7 | 并发 | 并发 5 张大图不导致 OOM，任务并发受限 |
| 8 | 头像 | 头像超限仍 400，不进入异步 |
| 9 | 迁移 | 000040 up/down 通过 |
| 10 | 契约 | OpenAPI 增加 `thumb_status` |
| 11 | 全量 | `verify` 通过 |

## 5. 不做

- 视频缩略图
- 客户端预生成缩略图
- 缩略图尺寸可选

## 实施与验收计划

基线：`main 5e38f21`；分支：`feat/be-idn-028-thumb-worker`。沿用现有文件服务、存储绑定与事务 outbox，不新增依赖。

- [x] 新增 000040 up/down、模型与显式读写投影；已有 thumb_key 的记录兼容为 ready。
- [x] 先添加上传状态回归测试，再实现同步阈值分流；pending 与 outbox 同事务持久化，原图成功不依赖 Redis/缩略图解码成功。
- [x] 添加缩略图 worker 与独立 asynq 队列：载荷仅 tenant_id/file_id，60 秒、20 MiB、4000 万像素、并发 2；解码失败终止，存储/超时异常保持 pending。
- [x] 添加小时级兜底：扫描创建超过 10 分钟的 pending，事务计数并重新投递，最多 3 次；覆盖重复、跨租户与删除竞态。
- [x] 同步 File OpenAPI、列表/metadata/上传响应及契约测试；thumb_url 仅 ready 时可用。
- [x] 在隔离数据库/schema、Redis 与对象桶上执行专项验收；同一张真实 12 MB 照片验证 pending→ready，显式开关运行并发 5 张的内存/耗时采样。
- [x] 执行 make lint、make test、verify、迁移全链与分组守卫；清理本卡数据，将命令、输出、偏差写入本卡验收记录后停止。

## 6. Codex prompt

```
实现 docs/tasks/BE-IDN-028-thumb-worker.md。

分支：feat/be-idn-028-thumb-worker（从最新 main e826e53 开）

必读：本卡、internal/modules/file/image.go（三个限制与静默返回）、
file.go（上传流程）、internal/bootstrap/worker.go（asynq 任务注册）、
internal/modules/file/upload.go、migrations/（file_object）

改动：
1. 迁移 000040：thumb_status、thumb_attempts
2. 上传：小图同步；大图 thumb_status=pending 并入队 thumb:generate
3. worker 任务 thumb:generate（60s、4000 万像素、并发 2），成功 ready / 失败 failed
4. 小时级兜底任务：滞留 pending 重新入队，最多 3 次
5. 列表与上传响应返回 thumb_status；thumb_url 仅 ready 时非空
6. OpenAPI 同步

约束：
- 头像路径不变（超限仍 400）
- 不改对象键格式
- 大图上传不得因缩略图失败而失败

验收按卡片 §4 十一条逐条贴证据：
- 第 2、3 条用同一张 12 MB 真实照片，贴上传响应与生成后的查询
- 第 7 条贴并发 5 张的内存/耗时
完成后停下报告，不要做前端。
```

## 7. 验收记录

真实照片来源：[Unsplash 森林照片](https://images.unsplash.com/photo-1441974231531-c6227db76b6e?w=8000&h=5000&fit=crop&q=100)。使用下载的 JPEG 原始内容，未通过填充字节凑大；12,480,881 bytes，8000×5000（40,000,000 像素），SHA256 `3aba233536ad772007ccf1d7edbd32f0992ac7fb6b99d70b1ef4c27887c1d1b0`。第 2、3 条为同一次上传、同一个文件 ID。环境：本地隔离 PostgreSQL 测试库、MinIO 临时桶、真实 Redis/asynq；响应与完整输出保存在 `docs/tasks/evidence/BE-IDN-028/`。

| # | 验收证据 |
|---|---|
| 1 | `TestFileThumbnailIntegrationUploadGenerateAndFailure`：1349-byte PNG POST 201，`thumb_status=ready`，`thumb_url` 非空。见 `functional.txt`。 |
| 2 | `TestFileThumbnailIntegrationRealPhoto`：12,480,881-byte 真实照片 POST 201，299 ms，响应 `{"id":1,"size_bytes":12480881,"thumb_url":null,"thumb_status":"pending"}`。完整响应见 `real-photo.txt`。 |
| 3 | 同文件 `GET /files/1/metadata` 200，上传开始后 1587 ms：`{"id":1,"thumb_status":"ready","thumb_url":"...real-photo.jpg.thumb.jpg"}`；列表同样 ready；缩略图实际 GET 200、JPEG 320×200；原图逐字节一致。见 `real-photo.txt`。 |
| 4 | 损坏的 >5 MiB JPEG 上传 201 pending，生成后 failed；重复执行无变化、`thumb_attempts=0`；队列任务 `MaxRetry=0`。见 `functional.txt` 与 `TestThumbnailQueueIdentifiersTimeoutAndNoRetry`。 |
| 5 | 人为将 pending 的 created_at 设为 20 分钟前：兜底重新入队，重复扫描不重复计数，最多三次；实际出队载荷 `{"tenant_id":1,"file_id":1}`，60 秒、retry=0。见 `functional.txt`。 |
| 6 | 真实照片正好 4000 万像素成功；10000×4001 PNG 超限生成 failed；另有小于 4 MiB 但大于 400 万像素的图片经异步成功。见 `real-photo.txt`、`functional.txt`。 |
| 7 | `THUMB_CAPACITY=1`：并发 5 次真实照片 HTTP 上传均 201 pending，上传总计 668 ms，全部缩略图完成总计 4334 ms，实际任务并发峰值 2，未 OOM。Go 堆基线 16,647,144 bytes，采样峰值 1,002,466,536 bytes（约 956 MiB），增量 985,819,392 bytes；测试进程包含五份 multipart 请求和 worker。见 `capacity.txt`。 |
| 8 | 2001×2001 头像 POST 400 / 10001，未创建异步头像任务；原有 >2 MiB 返回 413 的行为与路径保持。见 `functional.txt`，原头像回归测试保留。 |
| 9 | `TestFileThumbnailIntegrationMigration`：000040 down→39 / up→40，dirty=false；历史 thumb_key 回填 ready，新记录默认空状态/0，非法状态与重试数被约束拒绝。见 `functional.txt`。 |
| 10 | `api/openapi/file.yaml` 的 File schema 新增必填 `thumb_status`（空/pending/ready/failed），上传、列表和 metadata 均用此 schema；真实响应经 OpenAPI 校验，契约测试通过。 |
| 11 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -Integration -Migration`：退出 0，最终 `all checks passed`；单元 bootstrap 441.089s，独立集成 bootstrap 424.583s；CLI 迁移 up/down/up 输出 40/39/40 dirty=false。`make lint` 退出 0；`make test` 47 个包通过、0 失败。见 `verify.txt`、`lint.txt`、`test.txt`。 |

专项命令（对象存储凭据通过临时环境注入，不写入报告）：

```powershell
go test ./internal/modules/file ./internal/worker -run '(FileThumbnail|ThumbnailQueue)' -count=1
go test ./internal/bootstrap -run '^TestFileThumbnailIntegration' -count=1 -v
# 真实照片：THUMB_PHOTO_PATH 指向上面的 JPEG；THUMB_REDIS_ADDR 指向隔离 Redis。
go test ./internal/bootstrap -run '^TestFileThumbnailIntegrationRealPhoto$' -count=1 -v
# 容量测量必须显式设置 THUMB_CAPACITY=1；日常 CI 仅执行确定性的双槽回归。
go test ./internal/bootstrap -run '^TestFileThumbnailIntegrationFivePhotoCapacity$' -count=1 -v
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1
```

分组守卫：`Source 655 = shards 654 + excluded 1`；本卡测试全部属于 core，容量测试默认 Skip。尺寸检查：临时 Go AST 检查器检查全部变更 Go 文件，均 ≤400 行，所有函数均 ≤80 行，见 `size.txt`。`rg -n 'TODO|not implemented|fmt\.Print|log\.Print' internal` 无匹配，见 `static-scan.txt`。新增日志逐项审查：仅任务类型、标识、耗时和成功状态，无原始文件、密钥或用户敏感信息。

偏差说明：无。头像像素超限仍 400；原有字节超限 413 保持现有契约。新增字段按任务要求同步更新既有安全投影字段数量断言（11→12），并增加字段值断言；旧迁移测试先回滚 000040，再保留 000039 原有回滚断言。

自检：

- [x] `make lint`：最后一行 `golangci-lint run`，退出 0。
- [x] `make test`：47 个包通过、0 失败；最后汇总行 `ok github.com/omwk/go-admin-api/scripts/smoke (cached)`。
- [x] `go run ./cmd/cli migrate up` / `migrate down` / `migrate up`（verify -Migration 执行）：`version=40` / `39` / `40`，均 `dirty=false`；全链回滚、再 up、并发启动与结构不变量测试均通过。
- [x] 沿用上传、列表与 metadata 接口；响应字段已在 §3.4 及 OpenAPI 记录。
- [x] 无 TODO / 空实现 / 调试打印，日志不含敏感信息，检查方法与输出见上。
- [x] 单文件 ≤400 行 / 单函数 ≤80 行，全部变更 Go 文件 AST 检查通过。
- [x] 本地未运行 race；按仓库约定由合并前 CI 验证。完成后停止，不继续前端。

已清理：`go run ./cmd/cli audit-prune --before 2026-11-22T16:36:59Z`（本卡隔离验收库）→ `deleted=0`；测试 fixture 自动删除临时库/桶，本卡隔离 PostgreSQL 查询临时克隆库数量为 0；`docker stop be-idn-028-postgres be-idn-028-minio be-idn-028-redis` → 三个 `--rm` 容器全部移除、剩余 0。无人工临时租户/配置留在开发业务库；完整清理输出见 `cleanup.txt`。临时凭据文件已删除，公共照片保留在操作系统 TEMP 供复验；测试框架的迁移基线模板缓存按既有 testenv 机制保留，不含本卡验收业务数据。
