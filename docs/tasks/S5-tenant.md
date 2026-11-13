# S5 · 租户管理与开通（模板快照）

- 类型：系统层补全（`docs/design/system-layer-plan.md` S5）
- 预计：**4 人日**
- 分支：`feat/s5-tenant`
- 依赖：S1–S4（默认菜单/角色/权限已齐）

---

## 1. 决策：默认角色与菜单从「模板快照」来

不硬编码默认角色，也不每次从某个租户实时复制，而是**快照模板**：

```
从现有租户「保存为模板」→ tenant_template（含角色与菜单的快照）
开通新租户 → 从模板物化 → 建首个管理员邀请
```

理由：

| 方案 | 优 | 劣 |
|---|---|---|
| 硬编码默认角色 | 简单 | 改一次要发版；不同业务线无法用不同初始权限 |
| 从指定租户**实时**复制 | 灵活 | 源租户被改动后，下次开通结果悄悄变了，不可复现 |
| **模板快照（选定）** | 可复现、可审阅、可多套模板 | 多一张表与一次物化逻辑 |

模板只存**框架层**对象：角色、角色-权限、角色-菜单、系统配置（可选）。
**不复制**业务数据（店铺/商品/订单）。

## 2. 数据模型

```
tenant_template (
  id, code, name, description,
  roles jsonb,        -- [{code,name,data_scope,permissions:[codes],menus:[paths]}]
  settings jsonb,     -- 可选：租户级配置覆盖
  created_at, updated_at
  唯一 (code)
)
```

`tenant` 表补一列：

```
provisioned_from bigint null   -- 来源模板 id，便于追溯
```

## 3. 接口

### 3.1 模板

```
GET    /admin/tenant-templates
POST   /admin/tenant-templates                 从现有租户生成快照 { source_tenant_id, code, name }
GET    /admin/tenant-templates/{id}
PUT    /admin/tenant-templates/{id}            改名称/描述（不改快照内容）
DELETE /admin/tenant-templates/{id}            未被使用才可删
```

权限：`system:tenant:manage`。

### 3.2 租户

```
GET    /admin/tenants                          列表（含状态、创建时间、来源模板）
GET    /admin/tenants/{id}
POST   /admin/tenants                          开通：{ code, name, template_id, admin_username, admin_email? }
PUT    /admin/tenants/{id}                     改名（code 不可改）
POST   /admin/tenants/{id}/disable             停用
POST   /admin/tenants/{id}/enable              启用
```

权限：`system:tenant:list` / `system:tenant:manage`。

### 3.3 开通行为（关键）

`POST /admin/tenants` 在**同一事务**内：

1. 校验 `code` 唯一（全局，不区分租户）
2. 建 `tenant` 行
3. 按模板物化角色与授权（角色 code 冲突 → 换新租户内不冲突）
4. 按模板物化菜单（按钮节点一并创建）
5. 创建管理员用户（`is_super=true`，随机初始密码 **不落库不明文**，改用**邀请令牌**）
6. 返回 `{ tenant_id, invite_url }` —— 管理员点链接自己设密码

> 用邀请而非初始密码：与 F1 一致，避免「初始密码经手多人」。

### 3.4 停用语义

- `disable` → 该租户所有用户登录返回 403/20111「租户已停用」
- 已登录会话：下次刷新令牌时拒绝
- 数据保留，不删除

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 生成模板 | 从租户 A 生成模板，快照含 A 的全部角色与菜单路径 |
| 2 | 开通租户 | 用模板开通 B：角色数量、权限、菜单与模板一致 |
| 3 | 邀请 | 返回 invite_url，点开可设密码并登录 B |
| 4 | 隔离 | B 的数据对 A 不可见；B 管理员看不到 A 的用户 |
| 5 | 菜单 | B 登录后侧边栏与 A 一致（按钮权限码齐全） |
| 6 | code 唯一 | 重复 code 开通 → 409 |
| 7 | 事务 | 物化中途失败 → 租户行不残留（模拟权限缺失） |
| 8 | 停用 | 停用后 B 的用户登录 403；刷新令牌失败 |
| 9 | 启用 | 重新启用后可登录 |
| 10 | 模板保护 | 被使用的模板删除 → 409 |
| 11 | 越权 | A 的管理员读写 B 的租户 → 404 |
| 12 | 权限 | 无 `system:tenant:manage` → 403 |
| 13 | 菜单投影 | 「租户管理」页出现在系统目录；投影一致性测试通过 |
| 14 | 契约 | 新增接口进 OpenAPI，双向一致 |
| 15 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

## 5. 不做

- 租户计费/套餐绑定（属业务层）
- 租户数据导出与删除
- 跨租户用户（已定：一用户一租户）
- 模板版本管理（只保留最新快照）

## 6. Codex prompt

```
实现 docs/tasks/S5-tenant.md。

必读：本卡、docs/design/system-layer-plan.md S5、
internal/modules/identity/（seed 与权限物化逻辑可复用）、
internal/bootstrap/（事务与审计写法）、
api/openapi/identity.yaml、system.yaml

范围：
1. 0000xx 迁移：tenant_template 表 + tenant.provisioned_from
2. 模板 CRUD（从现有租户生成快照）
3. 租户开通（同一事务物化角色/菜单 + 建管理员 + 返回邀请链接）
4. 停用/启用（登录与刷新令牌都要拦）
5. 权限码 system:tenant:list / manage 与菜单投影

约束：
- 物化只复制角色/权限/菜单/可选配置，不复制业务数据
- 管理员用邀请令牌，不生成明文初始密码
- code 全局唯一；停用不删数据
- 迁移号在 S4 之后（若 S4 用了 000028，则本卡用 000029）

验收按卡片 §4 十五条逐条贴证据。
完成后停下报告，不要做前端。
```
