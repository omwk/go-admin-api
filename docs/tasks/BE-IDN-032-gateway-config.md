# BE-IDN-032 · 网关基地址与 CORS 配置

- ID: BE-IDN-032
- 类型: 新增
- 仓库: 后端
- 状态: 待实现
- 预计: 0.5 人日
- 分支: `feat/be-idn-032-gateway-config`
- 依赖: BE-IDN-029/030

---

## 1. 决定（2026-11-24）

API 域名**不硬编码、也不放系统配置**，而是放在**环境变量配置**：

```
CH_GATEWAY_PUBLIC_BASE_URL=https://api.example.com
CH_GATEWAY_ALLOWED_ORIGINS=https://admin.example.com,https://a.example.com
```

理由见 §2。

## 2. 为什么不放代码 / 不放数据库

| 方案 | 问题 |
|---|---|
| 硬编码 | 违反 AGENTS §3「不硬编码 URL」；本地/测试/生产三套域名无法区分 |
| 系统配置（数据库） | 域名是**部署身份**：改它等于迁移（DNS、证书、客户端配置全要跟着改）。
  数据库配置会被租户管理员误改，且改完立即生效 → 外部客户端报告 401 却找不到原因 |
| 环境变量（选定） | 与 `app.public_url` 一致；随部署版本管理；改域名走发布流程（重启），
  是一次可审计的运维动作而不是一个开关 |

**关键原则**：部署身份类配置（域名、证书、端口）走环境变量；
业务策略类配置（配额、开关、文案）走数据库设置。

## 3. 改动

### 3.1 配置项

```
gateway.public_base_url   string   可选；http(s) origin，无路径/凭据/查询
gateway.allowed_origins   []string 可选；CORS 允许来源，逗号分隔
```

校验（启动时，**2026-11-24 修订**）：

- `public_base_url` 为空时按以下顺序回退：
  1. 用已配置的 `app.public_url` 的 origin（应用路径保留给应用，打一条 warn）
  2. `app.env` 非 `prod`（包括 `dev`、`test` 或**未设置**）→ 用 `http://127.0.0.1:18080` 并 warn
  3. `app.env == "prod"` 且上面都没有 → **启动失败**并指明配置项
- 非空时必须是 `http(s)://host[:port]`，不允许带路径、查询、fragment、用户信息
- `allowed_origins` 每项同规则；为空表示不启用 CORS（服务端调用不需要）

> 为什么回退到 `app.public_url`：多数部署只有一个对外域名，网关与管理端同域；
> 强制要求两个配置项只会让升级时的每一个环境都启动失败。
> 只有**显式 prod 且两者皆空**才拒绝启动——这才是真正的配置错误。

### 3.2 只读展示接口

| 方法 | 路径 | 权限 | 响应 `data` |
|---|---|---|---|
| GET | `/api/v1/admin/gateway/info` | `gateway:client:list` | `public_base_url`、`allowed_origins` |

```
GET /api/v1/admin/gateway/info
→ { "public_base_url": "https://api.example.com", "allowed_origins": ["https://admin.example.com"] }
```

用途：管理端在客户端页展示调用示例（`{base}/open/v1/usage`），避免文档与配置不一致。

- 权限：`gateway:client:list`
- 不提供写入接口（要改就改环境变量并重启）

### 3.3 CORS 从配置读取

现状 `internal/kernel/httpx/middleware.go:47` 硬编码 `Access-Control-Allow-Origin: *`。

改为：

- `allowed_origins` 非空：只对匹配来源回写，并加 `Vary: Origin`
- 为空：不回写 CORS 头（默认无跨域）

> 当前 `*` 对带 `Authorization` 的接口虽不直接导致凭据泄露，但等于允许任意网页读取响应，
> 应当收紧。

### 3.4 OpenAPI servers

`internal/modules/gateway/openapi.yaml` 的 `servers` 在启动时使用配置注入，部署版通过 `GET /openapi/gateway.yaml` 提供。

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | prod 强校验 | 两个地址皆空时启动失败，错误含配置项名。只配 `app.public_url` 时网关回退并 warn |
| 2 | 格式校验 | 带路径/查询/凭据 → 启动失败 |
| 3 | 本地默认 | dev/test/空环境且两个地址皆空时使用 `http://127.0.0.1:18080` 并 warn |
| 4 | 展示接口 | 返回配置值；无写接口（POST → 405/404） |
| 5 | CORS 允许 | 配置了来源 → 匹配来源有 CORS 头，不匹配无 |
| 6 | CORS 关闭 | 未配置 → 无 `Access-Control-Allow-Origin` |
| 7 | Vary | 有 CORS 响应含 `Vary: Origin` |
| 8 | 调用示例 | 客户端页示例与 `public_base_url` 一致（后端提供字段） |
| 9 | 契约 | OpenAPI 更新 |
| 10 | 全量 | `verify` 通过 |

已清理：`docker stop codex-be-idn-032-pg codex-be-idn-032-redis` → 隔离容器自动删除，验收数据随容器销毁。临时二进制和凭据文件已删除。

## 5. 不做

- 多域名 / 动态域名（需求已移除）
- 管理端修改域名（有意不提供）
- 按客户端分配不同域名

## 6. Codex prompt

```
实现 docs/tasks/BE-IDN-032-gateway-config.md。

分支：feat/be-idn-032-gateway-config（从最新 main 5500d2b 开）

必读：本卡、docs/design/gateway-domains.md（已标注需求移除）、
internal/kernel/config/gateway.go、config.go（校验与 defaults）、
internal/kernel/httpx/middleware.go（CORS 硬编码处，第 45-47 行）、
internal/modules/gateway/open_http.go、openapi.yaml

改动：
1. 新增 gateway.public_base_url 与 gateway.allowed_origins，含启动校验
2. GET /admin/gateway/info（只读）
3. CORS 改为按配置；空配置不回写；加 Vary: Origin
4. OpenAPI servers 使用配置
5. 文档与 defaults 同步

约束：
- 不提供域名写入接口
- 不改变现有网关鉴权与配额逻辑

验收按卡片 §4 十条逐条贴证据：
- 第 1、2 条贴启动失败输出
- 第 5、6 条贴带/不带 CORS 头的响应
完成后停下报告，不要做前端。
```
