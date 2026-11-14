# 数据字典现状评估与使用计划

- 日期：2026-11-15
- 结论：**字典目前只有 3 个类型，且前端真正消费的 `product_status` 根本不存在**
- 建议：分两步——先补齐「已在用/高价值」的字典，再把硬编码文案替换为字典消费

---

## 1. 现状盘点

### 1.1 后端只有 3 个类型

| type | scope | 项数 |
|---|---|---|
| `order_status` | business | 6 |
| `sync_run_status` | system | 4 |
| `user_status` | system | 3 |

### 1.2 前端已在用，但后端没有

```
apps/admin/src/views/catalog/index.vue     useMetaDictionary('product_status')
apps/admin/src/views/catalog/detail.vue    useMetaDictionary('product_status')
```

`product_status` **没有种子**，所以商品状态列实际显示的是回退值（原值或空）。
这正是「字典形同虚设」的最直接证据：**消费方已就位，供给方是空的**。

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

### P0 · 必须补（前端已在用或用户直接可见）

| 字典类型 | scope | 值来源 | 消费页面 |
|---|---|---|---|
| `product_status` | business | `draft / active / archived` | 商品列表、详情 |
| `tenant_status` | system | `active / suspended` | 租户列表 |
| `audit_result` | system | `success / failure` | 审计日志、登录历史 |
| `dept_status` / `role_status` / `menu_status` | system | `active / disabled` | 部门、角色、菜单 |

> 三者值域相同，可用一个 `enable_status` 类型复用，避免三份重复。

### P1 · 建议补（业务可读性）

| 字典类型 | scope | 值来源 |
|---|---|---|
| `delivery_status` | system | `pending / sent / failed / skipped` |
| `sync_job_status` | system | `pending / running / succeeded / failed` |
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

### 阶段一：补齐种子（后端，0.5 天）

- 新增上述 P0 类型（`enable_status` 合并三个 enable 场景）
- 每项含 `label / value / sort / color`
- 校验：**前端引用的字典类型必须存在于种子**——后端无法扫描前端，改为在
  `docs/design/dictionary-usage.md` 登记「前端使用清单」，并在后端种子里对齐

### 阶段二：前端替换（前端，1.5 天）

- 建统一组件 `DictTag`（按 type + value 渲染 label/color，缺项回退原值）
- 替换第 1.3 节列出的所有硬编码
- 字典缺失时**显示原值**而不是空白（可观测性：显示原值能立刻发现字典没配）

### 阶段三：治理（可选）

- 字典项变更记录到审计（目前无）
- `enable_status` 这类通用类型禁止租户删除

---

## 5. 验收口径

1. 商品列表的状态列来自字典（改 label 后刷新即变）
2. 租户列表显示「已停用」而不是 `suspended`
3. 审计/登录历史显示「成功/失败」来自字典
4. 任意页面在字典缺项时显示原值，不出现空白
5. 后端种子包含 P0 全部类型；`/meta/dicts/{type}` 可查
6. 前端不再有硬编码的 status 文案（grep 检查列入验收）

---

## 6. 工作量

| 阶段 | 端 | 人日 |
|---|---|---|
| 阶段一 补种子 | 后端 | 0.5 |
| 阶段二 前端替换 | 前端 | 1.5 |
| 阶段三 治理 | 后端 | 0.5（可选） |
