# BE-IDN-035 · 对内 API 接入（一期：定义与凭据）

- ID: BE-IDN-035
- 类型: 新增
- 仓库: 后端
- 状态: 待实现
- 预计: 2 人日
- 分支: `feat/be-idn-035-integration-base`
- 依赖: 无
- 依据: `docs/design/integration-design.md`（决策已确认）

---

## 0. 事实核对

```
1) integration 权限码
   Select-String internal/modules/identity/permission/*.go -Pattern 'integration'
   → 0 命中

2) 通用 API 定义表
   information_schema: integration / endpoint / http_api
   → 0 行

3) 迁移最大编号
   migrations/*.up.sql 最大 → 000041

4) Sealer 接口（internal/kernel/crypto/keyring.go:16）
   Seal(plaintext []byte, aad string) (cipher, nonce []byte, keyVersion int, err error)
   Open(cipher, nonce []byte, aad string, keyVersion int) ([]byte, error)

5) 现有 SSRF 校验只存在于通知个人渠道
   internal/kernel/notifier/channel/personal.go:58 personalURL / :72 publicIPs
   → 无通用实现，需抽到 kernel
```

## 1. 一期范围

只做「定义 + 凭据 + 连通性测试」。端点定义、调试、日志、同步接入留给二期。

## 2. 改动

### 2.1 通用出站地址守卫（kernel）

新增 `internal/kernel/netguard`：

```go
// Validate 校验出站地址：https（dev/test 允许 http+回环）、无凭据、无 fragment、
// 主机解析后全部为公网地址（禁回环/私网/链路本地/元数据地址）。
func Validate(ctx context.Context, raw string, allowLocal bool) (*url.URL, error)
```

要求：

- 复用 `personal.go` 的 DNS 检查逻辑（`LookupIP` 后逐 IP 判断 `IsGlobalUnicast && !IsPrivate && !IsLoopback`）
- 把通知个人渠道改为调用它（行为不变，测试保持通过）
- 防止 DNS 重绑定：解析结果在请求时再次校验（Phase 1 只需在测试连接时校验）

### 2.2 表（迁移 000042）

```sql
CREATE TABLE integration (
  id bigserial PRIMARY KEY,
  tenant_id bigint NOT NULL REFERENCES tenant(id),
  name text NOT NULL,
  base_url text NOT NULL,
  auth_type text NOT NULL CHECK (auth_type IN ('none','api_key','bearer','basic','oauth2_client')),
  auth_config_enc bytea,           -- 加密的 JSON
  auth_nonce bytea,
  auth_key_version int,
  timeout_ms int NOT NULL DEFAULT 10000 CHECK (timeout_ms BETWEEN 1000 AND 60000),
  retry_max int NOT NULL DEFAULT 0 CHECK (retry_max BETWEEN 0 AND 5),
  qps_limit int NOT NULL DEFAULT 0 CHECK (qps_limit >= 0),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
  extra jsonb NOT NULL DEFAULT '{}'::jsonb,   -- 预留（分页等）
  created_by bigint NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, name)
);
```

- 名称租户内唯一（避免界面混淆）
- 不建 `tenant_id` 外键索引以外的东西；按租户查询使用索引
- `auth_config_enc` 存加密 JSON：`api_key` → `{location,name,value}`；`basic` → `{username,password}`；`bearer` → `{token}`；`oauth2_client` → `{token_url,client_id,client_secret,scope}`

### 2.3 接口

```
GET    /admin/integrations              列表（不含凭据）
POST   /admin/integrations              创建
GET    /admin/integrations/{id}         详情（凭据掩码）
PUT    /admin/integrations/{id}         更新（名称/base_url/超时/重试/限流/状态）
PUT    /admin/integrations/{id}/credentials  单独更新凭据
POST   /admin/integrations/{id}/credentials/reveal  查看明文
POST   /admin/integrations/{id}/test    连通性测试
DELETE /admin/integrations/{id}         删除（仅当无端点引用；二期有此约束）
```

规则：

- 创建/更新 `auth_type` 变更时必须同时提供新凭据；仅改其他字段时凭据保持不变
- 响应中凭据字段为掩码：`{"configured": true, "type": "api_key", "hint": "sk_…9f"}`
- `reveal`：`Cache-Control: no-store`，审计 `integration.credential.reveal`，每租户每日 20 次
- `test`：按 auth_type 附加认证后请求 `base_url`（GET，超时 5 秒），返回
  `{ ok, status_code, latency_ms, error? }`；错误码仅暴露 `network_unreachable` /
  `invalid_url` / `auth_failed` / `timeout`
- 删除用软删：`status` 置 `deleted` 不物理删（二期端点的引用安全）；列表默认不返回

### 2.4 权限与审计

新增权限码（登记到 registry 与 seed）：

```
integration:list    查看接入
integration:manage  创建/更新/删除/凭据
integration:debug   连通性测试与（二期）调试
```

新增审计 action（**先登记 `docs/design/audit-events.md`**）：

```
integration.create / integration.update / integration.delete
integration.credential.update / integration.credential.reveal / integration.test
```

### 2.5 OpenAPI

新增 `api/openapi/integration.yaml`，含 8 个接口与错误码。

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 创建 | 201；列表可见；名称重复 → 409 |
| 2 | 凭据加密 | 数据库 `auth_config_enc` 为密文，无明文；同租户可解密 |
| 3 | 掩码 | 详情返回 `configured=true` 与 hint，不含明文 |
| 4 | 查看 | reveal 返回明文，`no-store`，审计一条且不含明文 |
| 5 | 查看限流 | 第 21 次 → 429 |
| 6 | 更新 | 改名/超时/限流生效；不传凭据时凭据保持 |
| 7 | 切换认证方式 | auth_type 变更但未给凭据 → 400 |
| 8 | 连通性 | 真实 https 地址 → ok；错误密钥按类型返回 auth_failed |
| 9 | SSRF | `http://127.0.0.1`、`http://10.0.0.1`、`file://`、含凭据 URL → 400 |
| 10 | 删除 | 软删后列表不含；GET 详情 404 |
| 11 | 租户隔离 | 跨租户 GET/PUT/DELETE → 404 |
| 12 | 权限 | 仅 list 权限可看列表，写操作 403 |
| 13 | 审计登记 | 六个 action 均在 `audit-events.md`；一致性测试通过 |
| 14 | 契约 | OpenAPI 双向一致 |
| 15 | 全量 | `verify` 通过 |

## 4. 不做

- 端点定义（二期）
- 在线调试与调用日志（二期）
- 同步引擎接入（三期）
- OpenAPI 导入

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-035-integration-base.md。

分支：feat/be-idn-035-integration-base（从最新 main 1b1602a 开）

必读：本卡 §0 事实核对、docs/design/integration-design.md（决策已确认）、
internal/kernel/notifier/channel/personal.go（SSRF 逻辑，需抽取复用）、
internal/kernel/crypto/keyring.go（Sealer）、
internal/modules/gateway/（模块分层参考：service/store/http）、
internal/modules/identity/permission/registry.go、docs/design/audit-events.md

改动：
1. 新增 internal/kernel/netguard：Validate(ctx, raw, allowLocal)
2. 通知个人渠道改用 netguard（行为不变）
3. 迁移 000042：integration 表
4. 新增 internal/modules/integration：CRUD + 凭据更新/查看 + 连通性测试 + 软删
5. 权限码三个 + 审计 action 六个（先登记文档）
6. OpenAPI integration.yaml

约束：
- 凭据加密存储，响应掩码；reveal 单独接口 + 审计 + 限流
- 出站地址只允许 https 公网（dev/test 允许回环 http）
- 不做端点与调试（二期）

验收按卡片 §3 十五条逐条贴证据：
- 第 2 条贴数据库密文查询
- 第 8 条贴真实 https 地址的测试结果
- 第 9 条逐个非法地址贴 400
完成后停下报告，不要做前端。
```
