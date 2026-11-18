# BE-IDN-017 · 模板显示名

- ID: BE-IDN-017
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
- 预计: 0.5 人日
- 分支: `feat/be-idn-017-template-name`
- 依赖: BE-IDN-015（投递列表）、BE-IDN-016（模板重写）

---

## 1. 现象

BE-IDN-016 把模板 subject 改成了带变量的文案后，投递记录页的「模板」列变成：

```
验证你的 {{.AppName}} 邮箱
重置你的 {{.AppName}} 密码
```

根因：BE-IDN-015 的 `template_name` 直接取 `subject`：

```sql
coalesce(nullif(t.subject,''), d.template_code) AS template_name
```

subject 现在含占位符，未渲染时不可读。

## 2. 改动

### 2.1 模板表增加显示名

迁移 `000034`：`notify_template` 增加 `name text NOT NULL DEFAULT ''`。

内置模板种子填入：

| code | name |
|---|---|
| `identity.verify` | 验证注册邮箱 |
| `identity.reset` | 重置密码 |
| `identity.invite` | 账号邀请 |
| `notification.test` | 通道测试 |
| `notification.default` | 系统通知 |
| `credential.expiring.3d` | 凭据即将过期（3 天） |
| `credential.expiring.1d` | 凭据即将过期（1 天） |
| `credential.expired` | 凭据已过期 |
| `credential.needs_reauth` | 凭据需要重新授权 |
| `credential.failed` | 凭据校验失败 |
| `credential.stuck` | 凭据同步卡住 |
| `credential.recovered` | 凭据已恢复 |
| `credential.unknown` | 凭据状态未知 |
| `credential.protocol_pending` | 凭据协议待确认 |

### 2.2 投递列表取值顺序

```sql
coalesce(nullif(t.name,''), nullif(t.subject,''), d.template_code) AS template_name
```

- 有 name 用 name
- 旧数据/自定义模板无 name → subject（可能含占位符，属自定义内容）
- 都没有 → code

### 2.3 模板管理接口

`POST/PUT /admin/notifications/templates` 接受 `name`（≤128，可空）。
模板列表返回 `name`。

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 投递列表 | 「模板」列显示「验证注册邮箱」而不是含占位符的 subject |
| 2 | 兼容 | 无 name 的自定义模板回退 subject，再回退 code |
| 3 | 接口 | 模板 CRUD 支持 name，列表返回 |
| 4 | 迁移 | 000034 up/down 通过；存量行 name 为空 |
| 5 | 种子 | 重跑 seed 后内置模板 name 齐全 |
| 6 | 契约 | OpenAPI 模板对象增加 name |
| 7 | 全量 | `verify` 通过 |

## 4. 不做

- 模板多语言名称
- 投递记录页（前端无需改动，字段名不变）

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-017-template-name.md。

分支：feat/be-idn-017-template-name（从最新 main 8176b18 开）

必读：本卡、internal/bootstrap/notification_deliveries.go（template_name SQL）、
internal/bootstrap/notification_management.go（模板 CRUD）、
模板种子所在文件、migrations/ 模板表定义

改动：
1. 迁移 000034：notify_template.name
2. 种子填 14 条内置模板 name（见卡片 §2.1）
3. 投递列表 template_name 取值改为 name → subject → code
4. 模板 CRUD 支持 name，列表返回
5. OpenAPI 同步

约束：
- 不改 subject/body 语义
- 不改投递列表字段名

验收按卡片 §3 七条逐条贴证据。
完成后停下报告，不要做前端。
```
