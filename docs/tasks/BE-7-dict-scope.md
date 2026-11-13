# BE-7 · 字典区分系统域与业务域

- 类型：系统层修正（S2 的语义缺口）
- 预计：**0.3 人日**
- 分支：`feat/be-7-dict-scope`
- 依赖：S2（字典 CRUD 已合并）

---

## 1. 问题

`sys_dict` 只有 `builtin` 标记「是否 seed 产生、能否删除」，**没有归属域**。

后果：

- 升级时 `seed` 会 upsert 全部内置字典，把运营改过的 `order_status` 文案覆盖回去
- 前端无法按「系统 / 业务」分组展示，运营看到一堆不该改的框架枚举

## 2. 改动

### 2.1 数据

`sys_dict` 增加一列：

```
scope text not null default 'business' check (scope in ('system','business'))
```

迁移把现有三条归类：

| type | scope | 理由 |
|---|---|---|
| `user_status` | system | 身份域的固定状态 |
| `sync_run_status` | system | 同步引擎状态 |
| `order_status` | business | 业务状态，租户可能有自定义流程 |

### 2.2 行为

| 操作 | system | business |
|---|---|---|
| 删除字典/项 | 409（与现在 builtin 一致） | 允许（清空后） |
| 改 label / 排序 | 允许（租户可改显示文案） | 允许 |
| seed 升级覆盖 label | **不覆盖已有租户的 label**，只补缺失项 | 不覆盖 |
| 新增项 | 禁止（由代码定义） | 允许 |

### 2.3 接口

- `GET /admin/dicts?scope=system|business` 支持按域过滤
- `GET /meta/dicts/{type}` 不变
- 响应增加 `scope` 字段

### 2.4 seed 规则

`seed` 对 system 字典只做：

```
缺类型 → 建
缺项   → 补
已有项的 label / sort → 不动
```

对 business 字典完全不 seed（现三条中的 order_status 保留现状，仅标记 scope）。

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 迁移 | 三条字典 scope 正确；默认值 business 对新建生效 |
| 2 | 过滤 | `?scope=system` 返回 2 条，`?scope=business` 返回 1 条 |
| 3 | 系统字典删除 | 删 system 字典 → 409 |
| 4 | 业务字典删除 | 清空后删 business 字典 → 204 |
| 5 | label 保留 | 改 `user_status` 某项 label 后重跑 seed，label 不变 |
| 6 | 补缺失 | 删掉 `user_status` 的某项后重跑 seed，该项被补回 |
| 7 | 新增项保护 | 向 system 字典 POST 项 → 400 |
| 8 | 前端取值 | `/meta/dicts/order_status` 响应含 scope |
| 9 | 契约 | OpenAPI 更新 |
| 10 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

## 4. Codex prompt

```
实现 docs/tasks/BE-7-dict-scope.md。

必读：本卡、internal/modules/system/dict*.go（S2 实现）、
internal/bootstrap/system_dict_seed.go、migrations/000025/000026、
api/openapi/system.yaml

改动：
1. 新迁移：sys_dict.scope 列（默认 business，约束枚举），现有三条归类
2. system 字典：禁删、禁加项；seed 只补缺失，不覆盖已有 label/sort
3. business 字典：保持现有行为
4. 接口支持 ?scope= 过滤，响应含 scope
5. OpenAPI 更新

约束：
- 不改 /meta/dicts/{type} 的既有字段语义
- 迁移号 000028

验收按卡片 §3 十条逐条贴证据。
完成后停下报告，不要做前端。
```
