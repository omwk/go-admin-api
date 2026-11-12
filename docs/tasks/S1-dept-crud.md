# S1 · 部门管理（后端 CRUD）

- 类型：系统层补全（`docs/design/system-layer-plan.md` S1）
- 预计：**1.5 人日**
- 分支：`feat/s1-dept-crud`
- 依赖：F2（部门树只读，已合并）

---

## 1. 现状

`sys_dept` 表存在，`GET /admin/depts`、`GET /admin/depts/{id}` 已实现（F2）。
没有增删改，所以部门结构只能靠 seed 脚本 —— 运营无法自助调整组织架构。

## 2. 接口

```
POST   /admin/depts            新建
PUT    /admin/depts/{id}       更新
DELETE /admin/depts/{id}       删除
```

权限：`system:dept:create` / `system:dept:update` / `system:dept:delete`（若已存在同名常量则复用）。

### 字段

| 字段 | 规则 |
|---|---|
| parent_id | 0 表示根；必须存在且同租户 |
| name | 1–64 字符；同租户同一父下不可重名 |
| sort | 整数，默认 0；同级内排序 |
| status | `active` / `disabled` |

`path` 由后端根据 parent 的 path 自动维护，**不接受客户端传入**。

## 3. 关键规则

### 3.1 建树

- 新建时 `path = parent.path + "." + newID`（根为 `"1"` 形式，与现有数据一致）
- 更新 `parent_id` 时，**整棵子树的 path 必须同步重算**
- 禁止把节点移到自己的后代下（成环）→ 400
- 禁止把节点移到自己下 → 400

### 3.2 删除保护

| 情况 | 行为 |
|---|---|
| 有子部门 | 409，提示先删/移走子部门 |
| 有在册用户 | 409，提示先调整用户部门 |
| 根部门 | 400，禁止删除 |
| 已锁定/禁用 | 允许删除（状态不阻断） |

删除为**软删还是物理删**：本表无 `deleted_at`，按**物理删除**实现，但必须先通过上述保护。

### 3.3 并发与一致性

- 同一父下的重名检查用唯一约束或事务内 `SELECT ... FOR UPDATE`，避免并发穿透
- 移动子树时整棵 path 更新必须在**同一事务**内
- 所有写操作受 `Idempotency-Key` 保护（沿用既有中间件）

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 新建 | 根下新建一级部门，`GET /depts` 树中出现，path 正确 |
| 2 | 新建子部门 | 二级部门 path = `1.2.5` 形式 |
| 3 | 重名 | 同父下重名 → 400/10001 |
| 4 | 循环 | 把父节点移到其子节点下 → 400 |
| 5 | 移动子树 | 移动带 2 层子树的部门，所有后代 path 同步更新 |
| 6 | 删除保护 | 有子部门 → 409；有用户 → 409；根部门 → 400 |
| 7 | 删除成功 | 空部门删除后树中消失 |
| 8 | 越权 | 跨租户操作 → 404 |
| 9 | 权限 | 无 `system:dept:create` 调用 → 403/20107 |
| 10 | 幂等 | 相同 `Idempotency-Key` 重复提交不产生两条 |
| 11 | 契约 | `identity.yaml` 更新，响应通过契约校验 |
| 12 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

## 5. 不做

- 部门成员批量调整
- 部门与角色的数据范围联动（已有，只读）
- 前端页面（S1 前端单独一张卡）

## 6. Codex prompt

```
实现 docs/tasks/S1-dept-crud.md。

必读：本卡、docs/design/system-layer-plan.md §2 S1、
internal/modules/identity/（部门读接口 F2 的实现）、
api/openapi/identity.yaml、docs/04-conventions.md

范围：
1. POST/PUT/DELETE /admin/depts
2. path 由后端维护；移动子树时整棵重算，同一事务
3. 禁止成环；同父重名 400；删除保护 409/400
4. 权限码 system:dept:create/update/delete
5. OpenAPI 更新

约束：
- parent 必须同租户，越权 404
- 写操作走 Idempotency-Key
- 不新增表

验收按卡片 §4 十二条逐条贴证据。
完成后停下报告，不要做前端页面。
```
