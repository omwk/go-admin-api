# BE-IDN-021 · MinIO 存储配置与文件管理

- ID: BE-IDN-021
- 类型: 新增
- 仓库: 后端
- 状态: 待实现
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
