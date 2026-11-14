# BE-IDN-008 · 补齐字典种子

- ID: BE-IDN-008
- 类型: 新增
- 仓库: 后端
- 状态: 待实现
- 预计: 0.5 人日
- 分支: `feat/be-idn-008-dict-seed`
- 依赖: BE-7（字典分域，已合并）

---

## 1. 背景

前端已经在用 `useMetaDictionary('product_status')`，但后端**没有这个字典类型**，
导致商品状态列显示回退值。字典目前只有 3 个类型，覆盖不了系统层页面。

评估详见 `docs/design/dictionary-usage.md`。

## 2. 改动

在字典种子（`internal/bootstrap/system_dict_seed.go` 或等价位置）新增：

### 2.1 系统字典（scope=system，只读项）

| type | name | 项 |
|---|---|---|
| `enable_status` | 启用状态 | `active` 启用（绿）、`disabled` 停用（灰） |
| `tenant_status` | 租户状态 | `active` 正常（绿）、`suspended` 已停用（灰） |
| `audit_result` | 操作结果 | `success` 成功（绿）、`failure` 失败（红） |
| `delivery_status` | 投递状态 | `pending` 待发送（橙）、`sent` 已发送（绿）、`failed` 失败（红）、`skipped` 已跳过（灰） |
| `sync_job_status` | 同步任务状态 | `pending` / `running` / `succeeded` / `failed` |

### 2.2 业务字典（scope=business）

| type | name | 项 |
|---|---|---|
| `product_status` | 商品状态 | `draft` 草稿（灰）、`active` 在售（绿）、`archived` 已归档（灰） |

> `order_status`、`user_status`、`sync_run_status` 已存在，不动。

## 3. 规则

- 全部用既有 seed 机制（`system 字典只补缺失、不覆盖已有 label`）
- 每项含 `label / value / sort / color`，`color` 取前端主题可识别的值：`green/red/orange/gray/blue`
- `product_status` 为 business：运营可改文案
- 其余为 system：只读，随代码升级
- 不新增表、不新增迁移（种子走 seed）

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 类型存在 | `GET /admin/dicts?scope=system` 含 5 个新类型；business 含 `product_status` |
| 2 | 项数正确 | 每个类型的项数与上表一致 |
| 3 | 前端取值 | `GET /meta/dicts/product_status` 返回 3 项且带 color |
| 4 | 只读保护 | 删除 `audit_result` → 409；向其中加项 → 400 |
| 5 | 业务可改 | 修改 `product_status` 某项 label 成功，重跑 seed 后保持 |
| 6 | 幂等 | 连续两次 seed，项数不变、无重复 |
| 7 | 兼容 | 已有的 `order_status` / `user_status` / `sync_run_status` 不受影响 |
| 8 | 契约 | OpenAPI 无变化（同一接口）；文档 `dictionary-usage.md` 状态更新 |
| 9 | 全量 | `verify` 通过 |

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-008-dict-seed.md。

分支：feat/be-idn-008-dict-seed（从最新 main 开）

必读：本卡、docs/design/dictionary-usage.md、
internal/bootstrap/system_dict_seed.go（现有种子）、
internal/modules/system/dict*.go、BE-7 的 scope 规则

改动：
1. 新增系统字典：enable_status、tenant_status、audit_result、delivery_status、sync_job_status
2. 新增业务字典：product_status
3. 每项含 label/value/sort/color；system 只读、business 可改
4. 复用既有 seed 幂等机制，不新增表或迁移

验收按卡片 §4 九条逐条贴证据。
完成后停下报告，不要做前端。
```
