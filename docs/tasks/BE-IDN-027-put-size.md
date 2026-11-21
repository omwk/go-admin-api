# BE-IDN-027 · 缩略图与头像上传失败（size=-1）

- ID: BE-IDN-027
- 类型: 修复
- 仓库: 后端
- 状态: 已实现并验收（2026-11-21；契约差异与流式导出例外见验收记录）
- 预计: 0.3 人日
- 分支: `feat/be-idn-027-put-size`
- 依赖: BE-IDN-025（已合并）
- 发现方式: 合并后用真实 MinIO 手工验证

---

## 1. 现象（实测）

| 操作 | 结果 |
|---|---|
| 上传 2 KB **文本** | ✅ 201 |
| 上传 11 字节**伪 PNG**（仅文件头） | ✅ 201（不生成缩略图） |
| 上传**真实 PNG**（244 B / 800×400） | ❌ **500 `object upload failed`**，耗时约 7 秒 |
| 上传**真实 PNG 作为头像** | ❌ **500 `object upload failed`**，耗时约 6 秒 |

即：**所有需要生成缩略图或头像的图片上传全部失败**，而这正是 BE-IDN-025 新加的功能。

## 2. 根因

`internal/infra/storage/file_object.go:30`：

```go
func (f FileObject) Put(ctx context.Context, key, mime string, r io.Reader) (bool, error) {
	_, err := f.S3.PutFile(ctx, key, mime, r, -1)   // ← size = -1
	return err == nil, err
}
```

`PutFile` 把 `-1` 直接传给 minio 的 `PutObject`：**未知长度会走流式 multipart**，
对自定义域名/路径风格的 MinIO，该路径在 5 MB 分片、单线程下等待超时后失败。

- 主文件上传用的是已知大小（实测正常）
- 缩略图（`thumbnail()`）与头像（`avatar`）走 `Put(..., -1)` → 失败

## 3. 改动

1. `FileObject.Put` 改为显式传长度：

```go
func (f FileObject) Put(ctx context.Context, key, mime string, r io.Reader) (bool, error) {
	if seeker, ok := r.(io.Seeker); ok { … }        // 或者由调用方传 size
}
```

推荐直接改签名：

```go
Put(ctx, key, mime string, r io.Reader, size int64) (bool, error)
```

- `thumbnail()`：`int64(len(thumbnail))`
- 头像：`int64(len(avatarPNG))`
- 其余调用点补长度

2. 若确实需要未知长度（暂无），必须显式设置 `PartSize` 并确认可用。

3. 回归测试：用真实 MinIO（或本地 S3 兼容服务）上传一张真实 PNG，
   断言主文件、缩略图、头像三者均 200 且字节一致。

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 真实 PNG 上传 | 201，返回 thumb_url |
| 2 | 缩略图 | thumb_url 200，尺寸 ≤320 |
| 3 | 头像上传 | 200，`/api/v1/avatars/{id}` 200 |
| 4 | JPEG/GIF | 同样成功 |
| 5 | WebP | 无缩略图但上传成功 |
| 6 | 文本/PDF | 行为不变 |
| 7 | 代码扫描 | 不再出现 `PutFile(..., -1)` 或 `Put(..., -1)` |
| 8 | 真实端点 | 用 oss.uuo.me 实测通过（贴请求与结果） |
| 9 | 全量 | `verify` 通过 |

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-027-put-size.md。

分支：feat/be-idn-027-put-size（从最新 main 9cdf9e0 开）

必读：本卡、internal/infra/storage/file_object.go、
internal/modules/file/image.go（thumbnail）、avatar.go、
internal/infra/storage/s3.go（PutFile）

改动：
1. Put 接口显式传 size；缩略图与头像传真实长度
2. 移除所有 size=-1 的调用
3. 回归测试：真实 PNG 上传后 主文件/缩略图/头像 三者均可读且字节一致
4. 扫描断言：代码中不存在 PutFile(..., -1)

约束：
- 不改变对象键格式
- 不改变 MIME 校验与配额逻辑

验收按卡片 §4 九条逐条贴证据：
- 第 8 条用 oss.uuo.me 真实端点，贴 201 + thumb_url 200 的请求输出
完成后停下报告，不要做前端。
```

## 6. 实现与验收记录（2026-11-21）

- 分支从 `main / origin/main 8d9e65b` 创建：`feat/be-idn-027-put-size`。
- 文件端口与 `FileObject.Put` 显式接受 `size int64`，SDK 使用该长度。
- 主文件使用 multipart 实际长度；内存 reader 使用剩余长度；缩略图传 `int64(len(thumbnail))`。
- 头像已在 `avatar.go` 将编码结果长度写入 `Input.SizeBytes`，本次由 staging 调用继续传给 SDK。
- 上传响应复用现有 `project`，包含可读的 `thumb_url`，幂等响应也覆盖该字段。
- 对象键、MIME 校验、用户配额、图片解码与缩略图时限均保持现有实现；没有新增依赖、迁移或前端改动。
- 回归覆盖：真实 S3 读写、原图逐字节比较、PNG 缩略图与头像的独立预期 JPEG 逐字节比较、对象 GET 与 URL GET 比较、已知长度及错误路径、幂等重放、负长度调用扫描。

详细命令与请求输出见 [验收证据](evidence/BE-IDN-027/acceptance.md)。
