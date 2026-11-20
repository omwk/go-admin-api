# BE-IDN-021 · MinIO 存储配置与文件管理

- ID: BE-IDN-021
- 类型: 新增
- 仓库: 后端
- 状态: 待实现
- 预计: 2 人日
- 分支: `feat/be-idn-021-storage`
- 依赖: 无

---

## 1. 现状

| 项 | 现状 |
|---|---|
| 对象存储客户端 | 已有 `internal/infra/storage/s3.go`（minio SDK），但**只从环境变量读配置** |
| 上传 | 只有店铺模块的 `POST /admin/shop/files`（业务用途） |
| 管理端文件库 | **没有** |
| 配置界面 | **没有**，改配置要改 `.env` 并重启 |

用户要求：能在后台配置 MinIO，并用它做统一的上传。

## 2. 配置项（设置分组 `storage`）

平台级配置（仅平台租户管理员可读写），密钥加密存储：

| key | 类型 | 说明 |
|---|---|---|
| `endpoint` | string | 主机名，如 `oss.uuo.me`（不含协议） |
| `port` | number | 1–65535，默认 443 |
| `bucket` | string | 桶名，如 `oss` |
| `use_ssl` | bool | 是否 HTTPS（443 时通常 true） |
| `region` | string | 默认 `us-east-1`（MinIO 忽略但 SDK 需要） |
| `access_key` | string | 可见 |
| `secret_key` | secret | **加密存储，接口永不回显** |
| `public_base_url` | string | 可选，用于拼接可公开访问的地址 |

### 2.1 设置框架增加 secret 类型

`kernel/setting` 的 `Item` 增加 `Secret bool`：

- 写入：用现有 sealer 加密后存 `sys_setting.value`
- 读取（管理接口）：返回 `"******"`，不返回密文
- 写入空字符串：表示「保持不变」，不覆盖已有值
- 内部读取（存储客户端）：解密后使用

### 2.2 测试连接

```
POST /admin/settings/storage/test
→ { "ok": true, "bucket_exists": true, "latency_ms": 42 }
```

失败返回具体原因：

| 情况 | code |
|---|---|
| 连不上 | `network_unreachable` |
| 认证失败 | `access_denied` |
| 桶不存在 | `bucket_missing`（ok=false，但连接正常） |
| 配置不完整 | `invalid_config` + 缺失字段 |

### 2.3 统一存储实现（重要）

现状有**两套**对象存储实现：

| 实现 | 用在哪 | 问题 |
|---|---|---|
| `infra/storage/s3.go`（minio SDK） | 导出任务 | 正常 |
| `shop.NewHTTPObjectStore`（手写 HTTP 签名） | 店铺文件上传 | **认证失败**（实测 2026-11-20：endpoint=oss.uuo.me 时 `object storage existence check failed`，而同一组密钥用 minio SDK 可正常 `BucketExists` 与列举对象） |

要求：

- **统一到 minio SDK**（`infra/storage/s3.go`），删除手写 HTTP 实现
- 店铺文件、导出、新的文件库全部使用同一 Resolver
- `endpoint` 配置接受 `host` 或 `https://host[:port]` 两种写法，内部归一化
- `port` 与 `use_ssl` 参与归一化：443 + true → `https://host`

> 这就是用户「上传文件功能使用 minio」的直接诉求：不是加一个上传接口，
> 而是让**所有上传都走同一套可用的 MinIO 客户端**。

### 2.4 运行时解析

新增 `storage.Resolver`：

```
优先使用 settings（storage 分组）
  ↓ 未配置
回退环境变量 CH_STORAGE_*
  ↓ 都没有
报错「对象存储未配置」
```

保存配置后**立即生效**（版本号 + 客户端缓存失效），不需要重启。
导出任务与店铺文件也改用同一个 Resolver。

## 3. 文件库 API

新增 `internal/modules/file`（独立仓储，复用 `file_object` 表，不跨模块 import）：

```
POST   /admin/files            multipart: file, purpose
GET    /admin/files            ?purpose=&keyword=&page=&page_size=
GET    /admin/files/{id}        流式下载（Content-Disposition 保留原文件名）
GET    /admin/files/{id}/url    预签名 GET，5 分钟有效
DELETE /admin/files/{id}
```

规则：

- 大小上限 20MB（可配置 `storage.max_upload_mb`，默认 20）
- MIME 白名单：`image/*`、`application/pdf`、`text/csv`、`application/vnd.openxmlformats-officedocument.spreadsheetml.sheet`、`application/zip`
- 对象键：`tenants/<tenant>/<purpose>/<yyyy>/<mm>/<random>-<safe-name>`
- 文件名清洗：去路径、限长 200、禁止控制字符
- SHA256 校验（与现有 `file_object` 一致）
- 租户隔离：只能访问本租户文件；平台管理员可跨租户（审计）
- 删除：先删对象再软删记录；对象不存在视为成功
- 审计：`file.upload` / `file.delete`（含文件名、大小、purpose）
- `purpose` 枚举：`attachment`（通用）、`export`、`shop`（已有）

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 配置保存 | PUT 成功后 GET 返回配置，secret 为 `******` |
| 2 | 密文存储 | 数据库 `sys_setting` 中 secret 为密文，不含明文 |
| 3 | 保持原值 | PUT 时 secret 传空 → 旧密钥仍可用（测试连接成功） |
| 4 | 测试连接 | 正确配置 → ok=true；错误密钥 → `access_denied` |
| 5 | 桶缺失 | 桶不存在 → `bucket_missing`，不报网络错误 |
| 6 | 免重启 | 保存配置后立即上传成功（不重启进程） |
| 7 | 上传 | 20MB 内成功，返回 id/url/sha256；超限 413 |
| 8 | 类型限制 | 非白名单类型 → 400 |
| 9 | 下载 | GET 流式返回，文件名正确；预签名 URL 可直接访问 |
| 10 | 隔离 | A 租户不能读/删 B 租户文件（404） |
| 11 | 删除 | 删除后列表消失，对象存储中不存在 |
| 12 | 回退 | 清空 settings 后仍可用环境变量配置（迁移期兼容） |
| 13 | 权限 | 非平台管理员读写配置 → 404；文件接口需 `system:file:manage` |
| 14 | 契约 | OpenAPI 更新，双向一致 |
| 15 | 全量 | `verify` 通过 |

## 5. 不做

- 多租户独立存储桶（后续）
- 图片压缩/缩略图
- 分片上传与大文件断点续传
- 文件分享外链（预签名已够）

## 6. Codex prompt

```
实现 docs/tasks/BE-IDN-021-storage.md。

分支：feat/be-idn-021-storage（从最新 main 1e0cfea 开）

必读：本卡、internal/infra/storage/s3.go、internal/kernel/config/config.go（Storage）、
internal/kernel/setting/（Registry/Item/读写服务）、
internal/modules/shop/file.go（现有上传参考）、
catalog_export_runtime.go（S3 用法）、sealer 用法、migrations/（sys_setting 与 file_object）

改动：
1. 设置框架支持 Secret 类型（加密存储、读取掩码、空值保持）
2. 新增 storage 设置分组（平台级，含 9 个配置项，写入后立即生效）
3. POST /admin/settings/storage/test（四种失败原因）
4. storage.Resolver：settings 优先、env 回退；导出与店铺文件改用同一 Resolver
5. 新增 internal/modules/file：上传/列表/下载/预签名/删除，含大小与类型限制、租户隔离、审计
6. 新增权限码 system:file:manage（seed 并授予内置超管）
7. OpenAPI 同步

约束：
- secret 明文不得出现在任何响应、日志、审计中
- 不跨模块 import 其他模块的 repo/model（file 模块自带仓储）
- 不改 shop 上传的对外契约

验收按卡片 §4 十五条逐条贴证据：
- 第 2 条贴数据库查询，证明无明文
- 第 6 条贴「保存后不重启即上传成功」
- 第 10 条贴跨租户 404
完成后停下报告，不要做前端。
```
