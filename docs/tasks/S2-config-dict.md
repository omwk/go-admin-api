# S2 · 系统配置与数据字典（后端）

- 类型：系统层补全（`docs/design/system-layer-plan.md` S2）
- 预计：**3 人日**
- 分支：`feat/s2-config-dict`
- 依赖：无

---

## 1. 现状

| 能力 | 现状 |
|---|---|
| 系统配置 | `sys_setting` 表存在（`group_key` / `item_key` / `value jsonb`），**无任何接口**，值靠代码硬编码 |
| 数据字典 | **无表**，前端下拉与状态文案全部写死 |
| 错误码字典 | 常量散落，前端无法做统一文案映射 |

## 2. 系统配置

### 2.1 接口

```
GET /admin/settings              全部分组（含 schema 与当前值）
GET /admin/settings/{group}      单个分组
PUT /admin/settings/{group}      批量更新该组
```

权限：`system:setting:list` / `system:setting:update`。

### 2.2 注册表（关键设计）

配置项**不能是任意键值对**——没有 schema 的配置中心等于给生产库装了个随意改代码的入口。

在代码里维护一份注册表：

```go
type Item struct {
    Group    string   // security / notification / system
    Key      string   // password_min_length
    Type     string   // string | number | boolean | json
    Default  any
    Min, Max *float64 // number 类型
    Enum     []string // 可选值
    ReadOnly bool     // 展示但不可改
    Desc     string
}
```

首批条目（三组）：

| group | key | type | 说明 |
|---|---|---|---|
| security | `password_min_length` | number | 8–64，默认 8 |
| security | `password_require_mixed` | boolean | 默认 true |
| security | `password_expire_days` | number | 0–365，0 表示不过期，默认 90 |
| security | `password_history_size` | number | 0–20，默认 5 |
| security | `login_max_failures` | number | 3–20，默认 5 |
| security | `login_lock_minutes` | number | 1–1440，默认 15 |
| system | `system_name` | string | 1–64，默认 CommerceHub |
| system | `default_page_size` | number | 10–100，默认 20 |
| system | `timezone` | string | IANA 名称，默认 Asia/Shanghai |
| notification | `default_channel` | string | 枚举 email/sms/wecom/dingtalk/webhook |
| notification | `retry_max_attempts` | number | 0–10，默认 3 |

### 2.3 规则

- 未注册的 key → 400（**不能**接受任意键）
- 值按类型与范围校验，失败 400 并指明哪个 key
- `read_only` 项 PUT 忽略并返回 400
- 每次 PUT 记审计（before/after 摘要，不含敏感值）
- 租户可覆盖全局值：先查租户行，没有再落默认（本次只做租户级，全局默认在代码里）
- 配置值变更**不热更新运行中的应用**，除显式声明支持的项（本批：`password_*`、`login_*`、`default_page_size`）

> 热更新范围必须写清楚，否则「改了没生效」会成为反复出现的问题。

## 3. 数据字典

### 3.1 表

```
sys_dict        (id, tenant_id, type, name, status, created_at, updated_at)
sys_dict_item   (id, tenant_id, dict_id, label, value, sort, status, color, extra jsonb)
唯一：(tenant_id, type)、(tenant_id, dict_id, value)
```

### 3.2 接口

```
GET    /admin/dicts               类型列表
POST   /admin/dicts
PUT    /admin/dicts/{id}
DELETE /admin/dicts/{id}
GET    /admin/dicts/{id}/items
POST   /admin/dicts/{id}/items
PUT    /admin/dict-items/{id}
DELETE /admin/dict-items/{id}
GET    /meta/dicts/{type}         按类型取值（登录用户可读，供前端下拉）
```

权限：`system:dict:list` / `system:dict:create` / `system:dict:update` / `system:dict:delete`。

### 3.3 规则

- 删除字典前必须为空（有项 → 409）
- `value` 在字典内唯一
- 内置字典（如 `order_status`）标记 `builtin=true`，禁止删除但允许改 label 与排序
- `GET /meta/dicts/{type}` 返回启用项，按 sort 升序，带 60 秒缓存

### 3.4 种子

至少内置三份，供前端立即使用：

```
order_status     pending_payment / paid / shipped / completed / cancelled / refunded
user_status      active / disabled / locked
sync_run_status  pending / running / succeeded / failed
```

## 4. 错误码字典

```
GET /meta/error-codes
```

返回：

```json
[{ "code": 10001, "message": "参数错误", "http": 400, "domain": "common" }, ...]
```

来源：在 `internal/kernel/errs` 增加一张**可枚举的注册表**（现有常量改为从注册表引用，值不变），
避免手工维护两份。

前端用它做文案映射，缺失的码回退到后端 message。

## 5. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 配置读取 | `GET /admin/settings` 返回三组、11 项，含 schema 与默认值 |
| 2 | 配置写入 | PUT 合法值成功，`GET` 回显一致 |
| 3 | 类型校验 | `password_min_length="abc"` → 400 且指明 key |
| 4 | 范围校验 | `password_min_length=3` → 400；`=8` → 200 |
| 5 | 未注册键 | PUT 未注册 key → 400 |
| 6 | 只读项 | PUT `read_only` 项 → 400 |
| 7 | 审计 | 每次 PUT 产生一条审计，含 key 与前后值摘要 |
| 8 | 字典 CRUD | 建类型、建项、改、删（先清空）全通过 |
| 9 | 字典唯一 | 同类型重复 value → 409 |
| 10 | 内置字典 | 删除内置字典 → 409；改 label 成功 |
| 11 | 前端字典 | `GET /meta/dicts/order_status` 返回 6 项、按 sort 排序 |
| 12 | 错误码 | `GET /meta/error-codes` 覆盖现有全部常量，code 与常量逐字一致 |
| 13 | 越权 | 跨租户读写 → 404 |
| 14 | 契约 | 新增接口全部进 OpenAPI |
| 15 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

## 6. 不做

- 配置项热更新到所有运行节点（本批只支持列出的项）
- 全局配置（只做租户级）
- 字典项国际化
- 前端页面（单独一张卡）

## 7. Codex prompt

```
实现 docs/tasks/S2-config-dict.md。

必读：本卡、docs/design/system-layer-plan.md S2、
internal/kernel/errs/（错误码常量）、internal/modules/identity/（权限与审计写法）、
api/openapi/identity.yaml

范围：
1. 系统配置：注册表 + GET/PUT（三组 11 项），类型/范围/只读/未注册校验，审计
2. 数据字典：两张新表 + CRUD + /meta/dicts/{type}，内置三份字典种子
3. 错误码字典：errs 增加可枚举注册表（值不变），GET /meta/error-codes
4. 全部进 OpenAPI；权限码按卡片

约束：
- 配置项必须走注册表，禁止任意键
- 字典内置项禁止删除
- 审计不记录敏感值
- 新增迁移号从 000025 起

验收按卡片 §5 十五条逐条贴证据。
完成后停下报告，不要做前端。
```
