# BE-IDN-010 · 租户模板可编辑

- ID: BE-IDN-010
- 类型: 新增
- 仓库: 后端
- 状态: 待实现
- 预计: 1.5 人日
- 分支: `feat/be-idn-010-template-editable`
- 依赖: S5（租户模板与开通，已合并）

---

## 1. 问题

现状：

```
POST /admin/tenant-templates  { source_tenant_id: <必填>, code, name }
PUT  /admin/tenant-templates/{id}  { name, description }     ← 只能改名
```

由此产生三个后果：

1. **必须有一个"样板租户"才能建模板** —— 而样板租户只有 default，
   所以实际上永远只有一个模板
2. **建出来的模板改不了** —— 想做个「只读版」（2 个角色、无写权限），
   只能先真的开一个这样的租户，再克隆它
3. **模板无法复用** —— 两个模板想只差一个角色的权限，得克隆两次再分别建租户

模板应该是**可编辑的角色与权限组合**，不是租户的只读快照。

## 2. 目标

```
模板 = 一组角色定义（角色名 + 数据范围 + 权限码 + 可见菜单）
     + 菜单树（框架菜单，通常所有模板一致）

创建方式二选一：从租户快照 / 空白模板
创建之后：可以增删改角色、调整每个角色的权限
```

## 3. 改动

### 3.1 创建支持空白模板

```
POST /admin/tenant-templates
{ "code": "readonly", "name": "只读版", "description": "...", "source_tenant_id": null }
```

- `source_tenant_id` 省略 → 空白模板：`roles=[]`，`menus` 取当前租户的框架菜单树
  （菜单是框架对象，与租户无关，空白模板也必须有，否则开通后侧边栏为空）
- 提供 `source_tenant_id` → 行为与现在一致

### 3.2 更新支持编辑角色

```
PUT /admin/tenant-templates/{id}
{
  "name": "...", "description": "...",
  "roles": [
    { "code": "operator", "name": "运营", "data_scope": "self", "status": "active",
      "permissions": ["trade:order:list", "trade:order:read"],
      "menus": ["/trade", "/trade/orders"] },
    { "code": "viewer", "name": "只读", "data_scope": "self", "status": "active",
      "permissions": ["trade:order:list"], "menus": ["/trade", "/trade/orders"] }
  ]
}
```

校验：

| 项 | 规则 |
|---|---|
| role.code | 必填、唯一、≤64，`^[a-z][a-z0-9_]*$` |
| role.name | 必填、≤128 |
| role.data_scope | `all / dept / dept_and_child / self / custom` |
| role.permissions | 每个码必须在权限注册表中存在；去重 |
| role.menus | 每个路径必须存在于模板的 menus 中；去重 |
| roles 数量 | 0–50；为 0 时允许保存，但开通时提示「该模板不含角色」 |

`menus` 本身**不允许通过 PUT 修改**（框架菜单树的增删属于平台升级，不属于租户模板）。

### 3.3 复制模板

```
POST /admin/tenant-templates/{id}/clone  { "code": "readonly2", "name": "只读版-2" }
```

用途：在已有模板上做小改动（只差一个角色的权限）时，不必从头建。

### 3.4 模板详情返回可编辑结构

```
GET /admin/tenant-templates/{id}
→ { id, code, name, description, roles:[...], menus:[...], created_at, updated_at }
```

`roles` 直接返回可回填的结构；`menus` 返回路径与名称（用于前端展示可勾选项）。

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 空白模板 | 不传 source_tenant_id 创建成功，roles 为空、menus 非空 |
| 2 | 编辑角色 | PUT 提交 2 个角色，GET 回读一致 |
| 3 | 权限校验 | 不存在的权限码 → 400 且指明是哪个码 |
| 4 | 菜单校验 | 角色引用模板中不存在的菜单路径 → 400 |
| 5 | 角色唯一 | 重复 role.code → 400 |
| 6 | 空角色 | roles=[] 可保存；开通时返回警告字段（非错误） |
| 7 | 复制 | clone 后两模板角色独立，改一个不影响另一个 |
| 8 | 开通验证 | 用「只读版」模板开通租户：角色数与权限与模板一致 |
| 9 | 旧模板兼容 | 从租户快照创建的模板可正常编辑与开通 |
| 10 | 越权 | 非平台管理员操作模板 → 404 |
| 11 | 契约 | OpenAPI 更新，双向一致 |
| 12 | 全量 | `verify` 通过 |

## 5. 不做

- 模板版本管理（只保留最新）
- 模板继承（模板 A 基于模板 B）
- 通过 PUT 修改菜单树
- 前端页面（另卡）

## 6. Codex prompt

```
实现 docs/tasks/BE-IDN-010-template-editable.md。

分支：feat/be-idn-010-template-editable（从最新 main 开）

必读：本卡、internal/modules/identity/tenant_template.go、
internal/bootstrap/identity_tenant_snapshot.go、identity_tenant_materialize.go、
internal/modules/identity/model/tenant.go（TemplateRole/TemplateMenu）、
internal/modules/identity/permission/registry.go、api/openapi/system.yaml

改动：
1. POST 模板：source_tenant_id 可选；省略时建空白模板（menus 取当前租户框架菜单）
2. PUT 模板：支持 roles 全量替换（校验 code/name/data_scope/permissions/menus）
3. POST /tenant-templates/{id}/clone
4. GET 详情返回可编辑的 roles 与 menus
5. 开通时若模板 roles 为空，返回 warning 字段而非错误

约束：
- menus 不允许通过 PUT 修改
- 权限码必须存在于注册表；菜单路径必须存在于模板 menus
- 不新增表；模板 JSON 结构不变

验收按卡片 §4 十二条逐条贴证据，第 8 条要用「只读版」模板真实开通一个租户验证。
完成后停下报告，不要做前端。
```
