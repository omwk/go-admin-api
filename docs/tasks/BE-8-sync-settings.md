# BE-8 · 系统设置的同步策略组

- 类型：系统层补全（配置中心扩组）
- 预计：**0.2 人日**
- 分支：`feat/be-8-sync-settings`
- 依赖：S2（配置注册表与接口已合并）

---

## 1. 背景

上游数据映射本身属于**业务层**（`mapping_profile` / `mapping_version` / `schema_baseline` /
`schema_drift_event`，已有版本、漂移检测与回放语义），不应放进扁平的 `sys_setting`。

但映射过程里有三类**跨业务通用的策略**，属于系统层，应该在配置中心可调：

| 策略 | 为什么是系统级 |
|---|---|
| 未知枚举值怎么处理 | 任何同步都会遇到，与具体平台无关 |
| 漂移告警阈值 | 运维策略 |
| 是否允许自动提升基线 | 变更控制策略 |

现在这三项没有配置入口，只能改代码或直接改库。

## 2. 改动

在配置注册表（`internal/kernel/setting/defaults.go`）新增 `sync` 组三项：

```
sync.unknown_value_policy     string  enum: reject | keep_raw | fallback_null   默认 keep_raw
sync.drift_alert_threshold    number  1–10000                                    默认 1
sync.auto_promote_baseline    boolean                                           默认 false
```

说明：

- `unknown_value_policy`
  - `reject`：整批拒绝，进隔离区
  - `keep_raw`：原值落到 `attributes`，标准字段留空（默认，信息不丢）
  - `fallback_null`：标准字段置空并计数
- `drift_alert_threshold`：同一资源连续漂移多少次触发告警（对接通知通道）
- `auto_promote_baseline`：漂移连续出现且匹配基线时是否自动提升；默认关，变更控制优先

## 3. 约束

- **只加配置项，不改同步逻辑**。本卡交付的是"可配置"，实际读取策略在业务层卡里接
- 注册表校验沿用 S2 机制（类型/范围/枚举/只读/未注册拒绝）
- 配置项变更写审计（沿用 S2）
- 不新增表、不新增迁移（`sys_setting` 已存在）

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 分组可见 | `GET /admin/settings` 返回四组（security/system/notification/sync），sync 三项齐全 |
| 2 | 默认值 | 未设置时返回默认值：keep_raw / 1 / false |
| 3 | 枚举校验 | `unknown_value_policy=drop` → 400 并指明 key |
| 4 | 范围校验 | `drift_alert_threshold=0` 与 `10001` → 400；`1` → 200 |
| 5 | 布尔校验 | `auto_promote_baseline="yes"` → 400 |
| 6 | 回显 | PUT 后 GET 返回新值 |
| 7 | 审计 | 每次 PUT 产生一条审计，含 key 与前后值 |
| 8 | 租户隔离 | 租户 A 改值不影响租户 B |
| 9 | 契约 | `system.yaml` 的 settings 响应含三项 |
| 10 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

## 5. 不做

- 在同步逻辑里读取这三项（业务层卡）
- 映射表本身的配置界面
- 告警规则的复杂表达式

## 6. Codex prompt

```
实现 docs/tasks/BE-8-sync-settings.md。

必读：本卡、internal/kernel/setting/（S2 注册表与校验）、
internal/modules/system/settings.go、api/openapi/system.yaml

改动：
1. 配置注册表新增 sync 组三项（含默认值、枚举、范围）
2. 复用 S2 的校验与审计，不写新机制
3. OpenAPI 响应示例更新

约束：
- 不改同步逻辑，只加配置项
- 不新增表或迁移

验收按卡片 §4 十条逐条贴证据。
完成后停下报告，不要做前端。
```
