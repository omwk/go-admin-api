# BE-IDN-025 · 文件分类、访问模式与配额

- ID: BE-IDN-025
- 类型: 新增
- 仓库: 后端
- 状态: 待实现
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

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-025-file-model.md。

分支：feat/be-idn-025-file-model（从最新 main c22c535 开）

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
