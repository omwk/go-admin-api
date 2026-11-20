# BE-IDN-022 · 对象存储下载修复（Bucket Lookup 模式）

- ID: BE-IDN-022
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
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

分支：feat/be-idn-022-storage-lookup（从最新 main 516b1bf 开）

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
