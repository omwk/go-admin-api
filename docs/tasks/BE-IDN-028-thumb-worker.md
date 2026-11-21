# BE-IDN-028 · 大图缩略图异步生成

- ID: BE-IDN-028
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
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
