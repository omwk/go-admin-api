# S1c · 根部门语义收敛

- 类型：系统层修正（S1 的语义不一致）
- 预计：**0.3 人日**
- 分支：`feat/s1c-root-dept`
- 依赖：S1

---

## 1. 问题

S1 允许 `POST /admin/depts` 带 `parent_id=0` 创建**第二个根部门**，
但 `DELETE` 里有一条：

```go
if row.ParentID == 0 {
    return errs.New(errs.ErrInvalidParam, 400, "根部门不可删除")
}
```

结果：**能建、不能删**。

实测：

```
POST /admin/depts {"parent_id":0,"name":"探针部门"}  → 201, id=21
DELETE /admin/depts/21                              → 400 "根部门不可删除"
```

前端 SF-1 也因此做不出正确的入口——它给了「新增根部门」按钮，
每点一次就产生一条永远删不掉的脏数据。

## 2. 决策：必须且仅有一个根部门

理由：

- 数据范围里「本部门及下级」依赖 `path` 前缀，多根会让「全部」与「本部门」的边界含糊
- 前端树的渲染、拖拽、面包屑都假定单根
- 多根是「多公司」场景，那属于租户维度，不是部门维度

因此：

| 操作 | 规则 |
|---|---|
| `POST` parent_id=0 | 400「已存在根部门，请选择上级部门」 |
| `PUT` 把 parent_id 改为 0 | 400「根部门不可移动」 |
| `DELETE` 根部门 | 400（保持现状） |
| 根部门改名 / 改排序 | 允许 |
| 根的 `path` | 恒为 `"1"`，不参与重算 |

## 3. 改动

1. `admin_depts.go` 的创建与更新路径：`parent_id == 0` 且非根 → 400，带明确文案
2. 校验顺序：先判「是否存在根部门」，再判重名（避免先报重名再报根）
3. 测试：
   - 创建 `parent_id=0` → 400，且库中不新增行
   - 更新 `parent_id=0` → 400
   - 根部门改名 → 200，path 不变
4. `api/openapi/identity.yaml`：`parent_id` 描述补一句「0 仅用于种子根部门，API 不接受」

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 建根被拒 | `POST parent_id=0` → 400，message 指明需选择上级 |
| 2 | 无脏数据 | 拒绝后 `sys_dept` 行数不变 |
| 3 | 改根被拒 | `PUT {parent_id:0}` → 400 |
| 4 | 根可改名 | 根部门改名成功，`path` 仍为 `1` |
| 5 | 一级部门正常 | `parent_id=1` 创建成功，path=`1.<id>` |
| 6 | 删除语义不变 | 根部门删除仍 400；普通部门不受影响 |
| 7 | 契约 | OpenAPI 描述更新 |
| 8 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

## 5. 前端配套（另开卡片）

`SF-1b`：

- 去掉「新增根部门」按钮
- 改为「新增一级部门」，父级固定为根部门且不可改
- 表单里「上级部门」对一级部门隐藏

## 6. Codex prompt

```
实现 docs/tasks/S1c-root-dept.md。

必读：本卡、internal/modules/identity/admin_depts.go（创建/更新/删除路径）、
api/openapi/identity.yaml 的 depts 部分

改动：
1. 创建与更新拒绝 parent_id=0，返回 400 与明确文案
2. 校验顺序先判根部门存在性，再判重名
3. 补三条测试：建根被拒且无脏数据、改根被拒、根可改名且 path 不变
4. OpenAPI 描述更新

约束：
- 不改根部门删除行为
- 不改 path 生成规则

验收按卡片 §4 八条逐条贴证据。
完成后停下报告，不要做前端。
```
