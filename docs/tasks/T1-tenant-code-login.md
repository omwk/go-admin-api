# T1 · 登录改用租户代码

- 类型：**设计缺陷修复**（P0）
- 预计：**2 人日**（仅后端）
- 分支：`feat/t1-tenant-code-login`
- 依赖：无
- 来源：`docs/design/system-layer-gaps-v2.md` §2.B

---

## 1. 问题

```jsonc
// 现状
POST /auth/login { "tenant_id": 1, "account": "admin", "password": "..." }
```

三宗罪：

1. **数字 id 可枚举**：遍历 1..N 撞库，比枚举用户名容易得多
2. **`tenant.code` 无用途**：建了唯一 code，登录却不用
3. **前端写死** `VITE_PLATFORM_TENANT_ID=1`：非平台租户的用户在 UI 上**根本无法登录**

这不是体验问题——**多租户能力在界面上等于不存在**。

## 2. 目标

```
POST /auth/login { "tenant": "acme", "account": "admin", "password": "..." }
```

- `tenant` 是租户 **code**（字符串，全局唯一）
- 不传时使用配置 `tenant.default_code`（默认 `default`）
- 数字 `tenant_id` 停止对外暴露，兼容期保留一个版本

## 3. 改动

### 3.1 后端

| # | 改动 |
|---|---|
| 1 | 登录请求字段：新增 `tenant`（string，max 64）；`tenant_id` 标记 deprecated |
| 2 | 解析顺序：`tenant` → `tenant_id`（兼容，记 WARN）→ `tenant.default_code` |
| 3 | 按 code 查租户（走 `tenant` 表唯一索引），不存在 → 统一 401，**不区分「租户不存在」与「密码错误」** |
| 4 | 失败计数键改为 `login:fail:<tenantCode>:<account>`；另加租户级限速 `login:rate:<tenantCode>` |
| 5 | 停用租户仍返回 403/20111（与 S5 一致），但在**密码校验之后**判定，避免提前泄露租户状态 |
| 6 | `GET /auth/me` 增加 `tenant_code` 字段，便于前端展示当前租户 |

> 第 3、5 条是安全细节：先比密码再报「租户已停用」，否则攻击者可用 20111 探测租户是否存在。

### 3.2 前端（移到 SF-6，本卡不做）

本卡只交付后端契约；前端改动见 `docs/tasks/SF-6.md`（前端仓库），
包括企业代码输入、默认值与错误文案。

后端需要保证的契约：

- 请求字段名 `tenant`（string），缺省时用 `tenant.default_code`
- 错误文案统一为「企业代码、账号或密码错误」，不区分租户不存在与密码错误

### 3.3 配置

```
tenant.default_code = "default"     # 未传 tenant 时使用
```

## 4. 兼容与迁移

- 老客户端传 `tenant_id`：继续可用，服务端映射为 code，响应头 `X-Tenant-Deprecated: 1`
- 下个版本移除 `tenant_id`
- 文档 `docs/api/integration.md` 更新登录示例

## 5. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | code 登录 | `tenant=default` 登录成功 |
| 2 | 非平台租户 | 用 `acme` 登录成功（该租户有 active 用户时） |
| 3 | 未知 code | 401，且与密码错误返回**同一** code/message |
| 4 | 兼容 | 仍传 `tenant_id=1` 可登录，响应头有 deprecated 标记 |
| 5 | 默认值 | 不传 tenant 时落到 `default` |
| 6 | 停用租户 | 密码正确 + 租户停用 → 403/20111；密码错误 → 401（不泄露停用状态） |
| 7 | 限流 | 同一 code 大量失败触发租户级 429 |
| 8 | 计数隔离 | 同账号在租户 A 失败不影响租户 B |
| 9 | `/auth/me` | 返回 `tenant_code` |
| 10 | 契约 | OpenAPI 更新 |
| 11 | 全量 | `verify` 通过 |

## 6. 不做

- 子域名/自定义域登录（P2）
- 登录页租户选择器（下拉枚举租户会泄露租户列表）
- 手机号/邮箱登录改造（已支持，保持不变）

## 7. Codex prompt

```
实现 docs/tasks/T1-tenant-code-login.md。

必读：本卡、docs/design/system-layer-gaps-v2.md §2.B、
internal/modules/identity/auth_http.go、auth_login.go、
internal/modules/identity/tenant*.go（租户与 code）、
internal/kernel/config/（新增 tenant.default_code）、
api/openapi/identity.yaml

后端：
1. 登录新增 tenant 字段（code），解析顺序 tenant → tenant_id(兼容+警告) → 默认 code
2. 未知租户与密码错误返回同一 401 文案
3. 停用租户在密码校验之后判定
4. 失败计数按 (code, account)，另加租户级限速
5. /auth/me 返回 tenant_code

前端：
1. 登录页加「企业代码」，默认 default，记住上次输入
2. 移除 VITE_PLATFORM_TENANT_ID 隐式传参

验收按卡片 §5 十二条逐条贴证据。
完成后停下报告。
```
