# 数据字典现状评估与使用计划

- 日期：2026-11-15
- 后端状态：BE-IDN-008 补齐为 **9 个 system 类型、33 项**（含原有 user_status）；覆盖默认租户、全部存量租户和新租户。
- 前端状态：既有消费保留；统一业务标签、字典页本地状态兜底及前端审查报告状态更新留待前端任务。本次不改前端。

---

## 1. 现状盘点

### 1.1 后端代码种子（BE-IDN-008）

| type | scope | 项数 | 值域（按 sort） |
|---|---|---|---|
| `order_status` | system | 8 | pending_payment / pending_shipment / shipped / completed / closed / refunding / refunded / unknown |
| `sync_run_status` | system | 5 | running / success / partial / failed / cancelled |
| `user_status` | system | 3 | active / disabled / locked |
| `product_status` | system | 3 | draft / active / archived |
| `tenant_status` | system | 2 | active / suspended |
| `audit_result` | system | 2 | success / failure |
| `enable_status` | system | 2 | active / disabled |
| `delivery_status` | system | 4 | pending / sent / failed / skipped |
| `invite_status` | system | 4 | active / expired / revoked / used |

每项均包含 label/value/sort/color；状态默认 active，extra 默认空对象。同步值域直接对齐
`000006` 的 CHECK，订单值域直接对齐 `orderDisplaySQL` 的展示投影，不改事实状态机。

### 1.2 前端使用登记

```
apps/admin/src/views/catalog/product/index.vue     useMetaDictionary('product_status')
apps/admin/src/views/catalog/product/detail.vue    useMetaDictionary('product_status')
```

`product_status` 已具备后端供给，`GET /api/v1/meta/dicts/product_status` 返回三项且均含 color。
前端审查报告 §2 还登记了 6 处 user_status 消费；字典页的 dictionary-form.vue、item-form.vue
应改成本地固定 active/disabled 选项，不依赖可停用的远端 user_status。当前源码尚无该兜底，
后端允许停用、编辑和重新启用 user_status；后端接口测试通过不代表前端自举已经通过。

### 1.3 大量硬编码文案（抽样）

| 位置 | 现状 |
|---|---|
| 部门页 | `status === 'active' ? '启用' : '停用'` |
| 角色页 | 同上 |
| 菜单页 | 同上 |
| 用户页 | `text === 'active' ? '启用' : '禁用'` |
| 审计页 | `record.result === 'success' ? '成功' : '失败'` |
| 登录历史 | 同上 |
| 通知渠道/模板 | `record.enabled ? '启用' : '停用'` |
| 租户页 | 状态直接显示后端值（`suspended`） |

---

## 2. 原则：字典只管「展示」，不管「语义」

这是决定"哪些该字典化"的前提：

| 该进字典 | 不该进字典 |
|---|---|
| 展示文案（已支付 / 待付款） | 状态机允许的转换（paid → shipped） |
| 颜色、排序、是否启用 | 权限码、错误码 |
| 租户想改的说法 | 接口字段名、枚举值本身 |

**判断标准**：如果字典里改错一个值会让业务逻辑出错，那它就不该在字典里。
前端始终用 `value` 判断逻辑，只用字典取 `label / color`。

---

## 3. 候选清单

### P0 · 供给已实现，消费按前端任务接入

| 字典类型 | scope | 值来源 | 消费页面 |
|---|---|---|---|
| `product_status` | system | `draft / active / archived` | 商品列表、详情 |
| `tenant_status` | system | `active / suspended` | 租户列表 |
| `audit_result` | system | `success / failure` | 审计日志、登录历史 |
| `enable_status` | system | `active / disabled` | 部门、角色、菜单 |

> 三者值域相同，可用一个 `enable_status` 类型复用，避免三份重复。

### P1 · 建议补（业务可读性）

| 字典类型 | scope | 值来源 |
|---|---|---|
| `delivery_status` | system | `pending / sent / failed / skipped` |
| `sync_run_status`（本卡已修正） | system | `running / success / partial / failed / cancelled` |
| `invite_status`（本卡已供给） | system | `active / expired / revoked / used` |
| `inquiry_status` | business | 询盘状态（后端定义） |
| `share_link_status` | business | 分享链接状态 |
| `shop_status` | business | `active / paused / disabled` |
| `credential_status` | business | `active / expiring / expired / needs_reauth / disabled` |

### P2 · 可按需

| 类型 | 说明 |
|---|---|
| `menu_type` | `dir / menu / button` |
| `data_scope` | `all / dept / dept_and_child / self / custom` |
| `billing_statement_status` | 账单状态 |
| `reconcile_diff_status` | 对账差异状态 |

### 不建议字典化

| 项 | 原因 |
|---|---|
| 权限码、错误码 | 语义固定，字典化会让「改文案」变成「改权限」 |
| 订单的 `payment_status` / `after_sales_status` | 参与展示态计算（`orderDisplaySQL`），语义必须固定在代码 |
| 通知 `template` 代号 | 与后端模板表绑定，改代号会断投递 |

---

## 4. 落地方案

### 阶段一：字典供给（BE-IDN-008，后端已实现）

- `system_dict_seed_data.go` 集中声明完整集合；`seedDictsForTenant(ctx, tenantID)` 由默认 seed 和 Provision/Materialize 共用，并复用外层事务。
- `ON CONFLICT DO NOTHING` 只补缺失；已有 name/status/label/sort/color/extra 均不覆盖。已有 business 类型不自动改 scope、不补其条目。
- `dict-backfill` 为所有现存租户补齐缺失供给，可在生产运行；重复执行无重复项，不覆盖自定义业务文案。
- `dict-reset` 是开发用破坏性命令，删除每个租户的全部字典及条目后重种，`app.env=prod` 在连接数据库前拒绝。它会删除自定义业务字典并恢复默认文案。
- 重置按租户加字典写锁、在单个事务中完成删除与重种，失败租户整体回滚；已完成租户输出前后数量，失败后可重跑。
- 本次开发数据库已用 reset 移除旧 sync 的 pending/succeeded、旧 order 的 paid/cancelled；sync 的 cancelled 是合法值，必须保留。
- 接口无变化：仍使用原有 admin CRUD、meta 数组和错误码。system 字典可写 name/status，项可写 label/sort/color/status/extra；type/value/scope/builtin 等不可改，码值不能增删。

加载当前环境的 CH_* 配置后执行：

```powershell
go run ./cmd/cli seed           # 需要 CH_SEED_ADMIN_PASSWORD，只补默认租户
go run ./cmd/cli dict-backfill  # 所有存量租户，只补不覆盖
go run ./cmd/cli dict-reset     # 仅开发环境，清空所有租户字典后重种
```

CLI 为独立进程，运行中的 API 的 meta 缓存最多保留 60 秒；reset 后重启开发 API 或等待缓存过期再检查页面。
`dict-reset` 只修复开发数据；生产升级用 seed/backfill，绝不自动清空或覆盖存量配置。

### 阶段二：前端替换（前端，1.5 天）

- 建统一组件 `DictTag`（按 type + value 渲染 label/color，缺项回退原值）
- 替换第 1.3 节列出的所有硬编码
- 字典缺失时**显示原值**而不是空白（可观测性：显示原值能立刻发现字典没配）

### 阶段三：治理（可选）

- 字典和字典项写操作已有事务审计。
- 所有 system 类型均禁止增删码值及删除类型；文案、排序、颜色与启停可维护。
- customer_source / customer_level 仍需盘点后另卡接入 business，本卡不处理。

---

## 5. 验收口径

1. 商品列表的状态列来自字典（改 label 后刷新即变）
2. 租户列表显示「已停用」而不是 `suspended`
3. 审计/登录历史显示「成功/失败」来自字典
4. 任意页面在字典缺项时显示原值，不出现空白
5. 后端种子包含本卡完整 9 类型；`/meta/dicts/{type}` 可查，Provision 后字典数与默认租户一致
6. 前端不再有硬编码的业务 status 文案（字典维护页本地 active/disabled 选项属于自举例外；前端任务验收）
7. 后端十二项验收记录见 `docs/tasks/BE-IDN-008-dict-supply.md`；第 9 条页面自举和第 11 条前端报告状态待前端任务完成

---

## 6. 工作量

| 阶段 | 端 | 人日 |
|---|---|---|
| 阶段一 补种子 | 后端 | 0.5 |
| 阶段二 前端替换 | 前端 | 1.5 |
| 阶段三 治理 | 后端 | 0.5（可选） |
