# M4b2 第一轮：存储发布能力待决策

日期：2026-11-05。分支：`feat/M4b2-export`，从 `main` 创建。
范围：000009、T_export 完整内容物化、CSV 流式上传、发布与下载。
不包含：第二轮的并发准入、pending 限额与定期清理。

## 阻塞点

现有存储端口无法实现已要求的对象版本保护和签名下载，需要确认本轮存储实现方式。

## 证据

- `internal/modules/shop/file.go`：ObjectStore 只有 Put、Copy、Delete、Open，没有条件发布、对象版本或预签名端口。
- `internal/modules/shop/http_store.go`：使用可选 Basic Auth 面向存储网关；Put/Copy 先查存在再写入，存在竞态，不是条件创建；Copy 通过 GET 后 PUT 实现。
- `internal/modules/shop/file_http.go`：现有下载通过应用转发字节，没有生成签名 URL。
- `internal/kernel/config/config.go`：已有 endpoint、bucket、access_key、secret_key、use_ssl，没有存储版本能力配置。
- ADR-018 §5：旧 attempt 迟到上传不能覆盖新成果，URL 有效期 min(1h, 对象剩余寿命)。
- `docs/proposals/M4b-review.md` §Q5：指出仅 DB fence 无法阻止迟到存储 IO，须条件创建或不可变版本并绑定确切版本。

## 选项

A. 为导出增加原生 S3/MinIO 适配器，允许引入维护中的官方兼容 SDK，并要求导出桶开启版本管理。

- 私有 attempt 暂存对象使用独立服务端生成 key；完整上传后读取并校验行数、字节数、SHA-256。
- 发布至 `exports/{tenant}/{export_id}.csv`，保存返回的不可变 version ID。
- 完成事务通过 attempt 条件更新登记 file_object；meta 保存精确版本、摘要和行数，过时代次不能更新 job。
- 下载只对已登记的精确版本签名，tenant 和 owner 校验在签名前完成；即使旧上传迟到创建另一个版本，签名仍指向已发布版本。
- 优点：版本保护和预签名使用存储原生能力，容易在真实 MinIO 上验收。
- 代价：新增依赖，需要导出桶启用版本管理；必须测试 versionId 下载和迟到上传。

B. 沿用现有 HTTP 存储网关，由网关补充条件发布、不可变版本读取和预签名协议。

- 先提供网关 API、鉴权、条件写失败语义、版本标识与私有桶约定，再实现导出适配器。
- 优点：沿用当前部署边界。
- 代价：依赖仓库外网关实现与联调；当前代码及配置不足以推断这些契约。

推荐 A；需要确认允许新增 SDK 及导出桶可开启版本管理。未确认前不修改生产实现或迁移。

## 决策后实施顺序与影响文件

1. `migrations/000009_export_rows.up.sql` / `.down.sql`：新增完整投影、排序键、ordinal、attempt、生成时间；增量 job attempt/row_total；复用 started_at。唯一索引覆盖 `(export_id, attempt, ordinal)`。
2. `internal/modules/catalog/export_*.go`：请求/响应、冻结筛选条件、字段版本、存储端口、worker、CSV 防注入和下载授权。完整投影仅含 id/title/价格区间/SKU 数/类目/状态/更新时间。
3. `internal/bootstrap/catalog_export*.go`：复用商品过滤/排序查询；在 3 秒事务内 INSERT SELECT 物化；后续只按 ordinal 读取，每批不超过 1000；attempt 条件完成与 durable outbox 派发。
4. `internal/bootstrap/identity.go` / `worker.go` / `scope.go`：装配导出 API、worker 事件与显式查询声明。
5. `api/openapi/catalog.yaml` / `docs/milestones/M4.md`：记录既有导出路径的请求、进度、下载 URL 与本轮范围。
6. `internal/kernel/db/integration_test.go` 及新增导出集成测试：增补迁移全集与 9→8→9 断言；验证快照、10 万行性能、强杀恢复、旧 attempt 迟到、越权 404、公式转义、摘要不匹配拒绝发布。
7. A 另涉及 `go.mod` / `go.sum`、导出 S3 适配器及桶配置/集成环境；B 先补齐网关协议。

## 验收命令

`powershell -File scripts/verify.ps1 -Integration -Migration`

本次只完成分支创建与阻塞分析，尚未实现功能或执行验收；不得视作 M4b2 交付。
