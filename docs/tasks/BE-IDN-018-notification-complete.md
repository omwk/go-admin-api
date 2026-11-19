# BE-IDN-018 · 通知完整性（后端）

- ID: BE-IDN-018
- 类型: 修复
- 仓库: 后端
- 状态: 已实现（本地验收通过）
- 预计: 2.5 人日
- 分支: `feat/be-idn-018-notification-complete`
- 依赖: 无
- 依据: `docs/design/notification-audit-2.md`

---

## 1. 目标

把通知从「一组接口」补成「一个能走通的功能」：偏好可读、模板可编辑可验证、
投递可诊断、公告可群发、站内消息有标题。

## 2. 改动

### 2.1 偏好：名称 + 事务性约束

`GET /admin/notifications/preferences` 返回：

```json
{ "template": "identity.verify", "name": "验证注册邮箱",
  "transactional": true, "enabled": { "email": true, "inbox": true } }
```

- `transactional=true`（`identity.verify` / `identity.reset` / `identity.invite`）
  → **不可关闭**，`PUT` 尝试关闭返回 409「安全邮件不可关闭」
- 其他模板可关

### 2.2 模板变量声明

`GET /admin/notifications/templates/variables` 返回按 code 分组的变量声明：

```json
{ "identity.verify": [
  { "name": "AppName", "description": "系统名称", "example": "CommerceHub", "required": true },
  { "name": "Link", "description": "验证链接", "example": "https://…", "required": true },
  { "name": "SupportEmail", "description": "客服邮箱，可为空", "example": "support@acme.com", "required": false }
] }
```

- 数据来源集中在代码（与 BE-IDN-016 的变量表同源）
- 保存模板时校验：正文引用的变量必须在该 code 的声明内，否则 400 并指明变量名
- 渲染仍用 `missingkey=error` 兜底

### 2.3 模板预览与测试发送

```
POST /admin/notifications/templates/{id}/preview
→ { "subject": "…", "body": "…" }       用声明中的 example 填充

POST /admin/notifications/templates/{id}/test
→ 发到服务商配置的测试收件人，返回投递 id
```

### 2.4 投递入队即生成掩码

- `Enqueue` 时若能解析收件人（用户邮箱/手机），立即写 `recipient_masked`
- 发送阶段仍以真实收件人为准；解析不到时保持 null
- 旧记录不回填（hash 不可逆），前端显示 `—`

### 2.5 公告群发

```
POST /admin/notifications/broadcasts
{
  "title": "系统维护通知",
  "body": "…",
  "channels": ["inbox", "email"],
  "audience": { "type": "all" }            // 或 {"type":"roles","ids":[3,5]} / {"type":"users","ids":[12,13]}
}
→ { "broadcast_id": 7, "recipients": 42 }
```

要求：

- 权限 `system:notify:broadcast`（新增权限码）
- 先预览人数：`POST /broadcasts/preview` 返回 `{recipients}`
- 写入 `notification`（channel=inbox，target=`user:<id>`，payload 含 title/body）
- 选 email 时同时入队 `notify_outbox`（模板 `notification.broadcast`）
- 审计 `system.notify.broadcast`，记录标题、渠道、范围、人数
- 幂等：`Idempotency-Key`
- 平台管理员可跨租户（仅平台租户内），租户管理员仅本租户

`GET /admin/notifications/broadcasts` 返回历史（标题、时间、人数、渠道）。

### 2.6 站内消息标题

- `notification.payload` 写入 `{title, body}`（广播来自输入；模板类来自模板 name）
- `GET /admin/notifications` 每条返回 `title`、`summary`（正文前 120 字）
- 模板筛选返回 `template` + `template_name`

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 偏好名称 | 返回 name；transactional 模板 PUT 关闭 → 409 |
| 2 | 偏好可关 | 普通模板可关闭，重读生效 |
| 3 | 变量声明 | 每个内置 code 有变量列表，含描述与示例 |
| 4 | 保存校验 | 引用未声明变量 → 400，message 含变量名 |
| 5 | 预览 | 返回渲染后的 subject/body，不含 `{{` |
| 6 | 测试发送 | 返回投递 id，投递记录出现 |
| 7 | 入队掩码 | pending 记录的 `recipient_masked` 非空 |
| 8 | 公告预览 | all/roles/users 三种范围人数正确 |
| 9 | 公告发送 | 目标用户收件箱出现消息，title/body 正确；非目标用户没有 |
| 10 | 公告+邮件 | 选 email 时产生对应 outbox 与投递记录 |
| 11 | 公告权限 | 无 `system:notify:broadcast` → 403；非平台管理员跨租户 → 404 |
| 12 | 公告审计 | 一条 `system.notify.broadcast`，含范围与人数 |
| 13 | 消息标题 | 列表返回 title/summary；模板类回退模板 name |
| 14 | 契约 | OpenAPI 更新，双向一致 |
| 15 | 全量 | `verify` 通过 |

## 4. 不做

- 公告定时发送、撤回、草稿
- 短信渠道的公告
- 多语言模板

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-018-notification-complete.md。

分支：feat/be-idn-018-notification-complete（从最新 main baf7c5b 开）

必读：本卡、docs/design/notification-audit-2.md、
internal/bootstrap/notification_preference.go、notification_management.go、
internal/kernel/notifier/（service.go 的 Enqueue 与 recordRecipient、template.go）、
internal/modules/identity/permission/registry.go（新增权限码）、
migrations/（notification 表结构）

改动：
1. 偏好返回 name 与 transactional；事务性模板拒绝关闭
2. 模板变量声明接口 + 保存校验
3. 模板预览与测试发送
4. 投递 Enqueue 即生成 recipient_masked
5. 公告群发（预览/发送/历史）+ 权限码 + 审计
6. 站内消息 title/summary；模板筛选返回名称
7. OpenAPI 同步

约束：
- 不记录 token 明文
- 公告复用现有 outbox，不新建投递机制
- 权限码新增后需 seed 并授予内置超管

验收按卡片 §3 十五条逐条贴证据；第 9、10 条要真实发送并核对目标用户。
完成后停下报告，不要做前端。
```

## 6. 实现接口表

路径前缀：/api/v1/admin/notifications。

| 方法 | 路径 | 权限 | 返回 |
|---|---|---|---|
| GET | /templates/variables | system:notify:manage | 按 code 分组的变量声明 |
| POST | /templates/{id}/preview | system:notify:manage | subject/body 示例预览 |
| POST | /templates/{id}/test | system:notify:manage | 投递 ID、掩码与状态 |
| POST | /broadcasts/preview | system:notify:broadcast | recipients |
| POST | /broadcasts | system:notify:broadcast | broadcast_id/recipients，201 |
| GET | /broadcasts | system:notify:broadcast | 分页历史 |

既有 preferences 增加 name/transactional；站内消息列表增加 title/summary/template_name。

## 7. 验收记录

实际基线 af80e33（本次指令优先于卡内旧 prompt）。十五条逐项结果、真实收件截图、命令输出、兼容说明与清理行数见 [验收记录](evidence/BE-IDN-018/acceptance.md)。

2026-11-19：verify -Integration → all checks passed；db 7.764s / bootstrap 395.670s / CLI 33.912s；本机未跑 race，待 CI。

已清理：临时 tenant=34 的维护标志限定 DELETE → 审计 5、outbox 1、投递 1、站内消息与历史 2、服务商 1、幂等记录 1、刷新令牌 3、用户 3、部门 1、租户 1；逐表复查为 0；无关审计 8 条保留。无临时配置改动。
