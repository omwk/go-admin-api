# BE-IDN-008 · 字典供给修复（按审查报告修订）

- ID: BE-IDN-008
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
- 预计: 1.2 人日
- 分支: `feat/be-idn-008-dict-supply`
- 依赖: BE-7（字典分域，已合并）
- 来源：`docs/reports/system-dictionary-2026-11-15/report.md`（前端仓库）
  + `docs/design/dictionary-usage.md`

---

## 1. 为什么要改（原卡片判断有误）

初版卡片只打算「补几个字典类型」。核对 Codex 的审查报告与实测后，发现三处**供给缺陷**：

### P0-1 · 新租户完全没有字典

```
tenant 1  → 3 个类型
tenant 2–14 → 0 个
```

`000026` 迁移只给「迁移当时已存在」的租户插了数据；
`identity_tenant_materialize.go` 的物化流程**不含字典**。

结果：任何新开通租户的 `/meta/dicts/*` 全空，所有字典驱动的 UI 回退原码。

### P0-2 · `sync_run_status` 值域与实际不符

| 字典现有 | 真实 `sync_run.status`（迁移 CHECK） |
|---|---|
| pending / running / succeeded / failed | running / success / partial / failed / cancelled |

四个值只有两个对得上，这个字典**从未生效过**。

### P0-3 · `order_status` 与订单展示态不匹配

| 字典现有 | 接口展示投影（`orderDisplaySQL`） |
|---|---|
| pending_payment / paid / shipped / completed / cancelled / refunded | pending_payment / pending_shipment / shipped / completed / closed / refunding / refunded / unknown |

## 2. 改动

### 2.1 新增/修正字典类型

| type | scope | 值 | 说明 |
|---|---|---|---|
| `product_status` | business | draft / active / archived | 前端已在调用，供给缺失 |
| `sync_run_status` | system | running / success / partial / failed / cancelled | 改为与真实 CHECK 一致（原值作废） |
| `order_status` | business | 8 个展示态 | 改为与 `orderDisplaySQL` 一致（原值作废） |
| `tenant_status` | system | active / suspended | 租户页直接显示原码 |
| `audit_result` | system | success / failure | 审计与登录历史 |
| `enable_status` | system | active / disabled | 部门/角色/菜单/渠道启用态 |
| `delivery_status` | system | pending / sent / failed / skipped | 投递记录 |
| `invite_status` | system | active / expired / revoked / used | 邀请列表 |

**旧类型处理（2026-11-15 决定）**：当前所有租户均为开发阶段数据，**不做兼容**。

- `sync_run_status` 直接改为真实值域，不建 v2
- `order_status` 直接改为订单展示态的 8 个值，不建 order_display_status
- 多余的历史 item 直接删除

实现方式：新增开发用 CLI `dict-reset`（`app.env=prod` 拒绝执行），
清空字典与项后按代码重新种入。`seed` 本身仍保持「只补不覆盖」的语义，
生产升级不受影响。

### 2.2 字典供给覆盖所有租户（P0）

新增 `seedDictsForTenant(ctx, tenantID)`：

- 由 `SeedIdentity`（默认租户）调用
- 由租户物化流程（`Provision` / `Materialize`）调用
- 幂等（`ON CONFLICT` 按 type/scope）
- **只补缺失，不覆盖已有 label/sort/color**

再补一个一次性回填：为**已存在但无字典的租户**补种（迁移或 CLI），
验收要求租户 2–14 都从 0 变成完整集合。

### 2.3 字典自举保护

字典管理页的「状态」下拉当前依赖 `user_status` 字典。若该字典被停用，
维护页自身不可用。

处理：`user_status` 标记为 `builtin` 且**禁止停用/删除**（不只是禁增删项），
或在字典页对状态列使用本地固定两值（active/disabled）兜底。
本卡采用后者 + 文档说明，避免字典页依赖任何字典。

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 类型齐全 | `/admin/dicts` 含上表 8 个类型，项数与值域一致 |
| 2 | 新租户 | 新开通租户自动拥有全部字典（用 Provision 后查询验证） |
| 3 | 存量租户 | 租户 2–14 回填后字典数与默认租户一致 |
| 4 | 值域一致 | `sync_run_status` 与迁移 CHECK 逐字一致；`order_status` 与 `orderDisplaySQL` 逐字一致 |
| 5 | 旧值作废 | `dict-reset` 后旧值（succeeded/paid/cancelled）不再存在 |
| 6 | 增量不覆盖 | 改某业务字典 label 后重跑 seed 与回填，label 保持 |
| 7 | 前端取值 | `/meta/dicts/product_status` 返回 3 项含 color |
| 8 | 只读边界 | system 类型：改 label 允许；加项/删除/改 value 返回 400/409 |
| 9 | 自举 | 停用 `user_status` 后字典页仍可打开与维护（走本地兜底） |
| 10 | 幂等 | 连续两次 seed + 回填，项数不变无重复 |
| 11 | 契约 | 无接口变化；`dictionary-usage.md` 与审查报告状态更新 |
| 12 | 全量 | `verify` 通过 |

## 4. 不做

- 不迁移 81 组展示候选中剩余的部分（按审查报告分批）
- 不做任何旧值兼容（当前均为开发数据）
- 不改任何业务状态机与 CHECK 约束
- 不动 `customer_source` / `customer_level`（需先盘点数据，见报告 P2/P3）

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-008-dict-supply.md。

分支：feat/be-idn-008-dict-supply（从最新 main 开）

必读：本卡、docs/reports/system-dictionary-2026-11-15/report.md §2、
docs/design/dictionary-usage.md、
internal/bootstrap/system_dict_seed.go、identity_seed.go、
identity_tenant_materialize.go、migrations/000026、000028、
internal/modules/system/dict_write.go

改动：
1. 修正/新增 8 个字典类型（见 §2.1），旧值直接作废
2. seedDictsForTenant：默认租户 seed 与租户物化都调用，幂等、只补不覆盖
3. 新增 cmd/cli dict-reset：清空与重种（prod 拒绝执行）
4. 用 dict-reset 或回填覆盖全部存量租户
5. 字典页状态列改用本地兜底两值，消除自举依赖

约束：
- 不保留旧值；但不改业务状态机与 DB CHECK
- 不覆盖租户已改的 label/sort/color
- 不改业务状态机与 DB CHECK

验收按卡片 §3 十二条逐条贴证据，第 2、3 条要贴租户字典数量对比。
完成后停下报告，不要做前端。
```
