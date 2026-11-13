# S3a · 通知中心补全（后端）

- 类型：系统层补全（`docs/design/system-layer-plan.md` S3）
- 预计：**1.5 人日**
- 分支：`feat/s3a-inbox`
- 依赖：F3（通道管理）、既有 inbox

---

## 1. 现状（先纠正上一版的判断）

通知能力**比想象中完整**，已有：

| 接口 | 状态 | 说明 |
|---|---|---|
| `GET /admin/notifications` | ✅ | 站内信分页列表 |
| `PUT /admin/notifications/:id/read` | ✅ | 标记已读 |
| `GET/PUT /admin/notification-settings` | ✅ | 通道开关（租户级） |
| `GET/POST/PUT/DELETE /notifications/providers` | ✅ F3 | 服务商配置 |
| `POST /notifications/providers/:id/test` | ✅ F3 | 连通性测试 |
| `GET/PUT /notifications/templates` | ✅ F3 | 模板 |
| `GET /notifications/deliveries`、`POST .../retry` | ✅ F3 | 投递记录与重试 |

缺失的是**收件箱的完整性**与**页面投影**。

## 2. 缺口

| # | 缺口 | 影响 |
|---|---|---|
| 1 | 无未读数接口 | 顶栏角标无法显示 |
| 2 | 无「全部已读」 | 几十条要逐条点 |
| 3 | 列表只能按分页，无 `unread` / 类型 / 渠道筛选 | 找不到未读 |
| 4 | 无**用户级**通知偏好 | 租户级开关不够细：A 想看邮件、B 只想站内 |
| 5 | 通知相关权限全用 `system:setting:write` | 语义是「维护通知渠道」，授权粒度过粗 |
| 6 | 无菜单/权限投影 | 前端看不到入口 |

## 3. 改动

### 3.1 收件箱

```
GET  /admin/notifications?unread=true&template=...&page=1&page_size=20
GET  /admin/notifications/unread-count        → { "count": 3 }
POST /admin/notifications/read-all            → { "updated": 12 }
```

- 列表新增 `unread`（bool）、`template`（string）两个筛选；`channel` 默认 `inbox`
- 只返回 `target = 'user:<当前用户>'` 的行，跨用户不可见
- `read-all` 只影响自己的未读

### 3.2 用户级通知偏好

新表：

```
notification_preference (
  id, tenant_id, user_id, template, channel,
  enabled, created_at, updated_at
  唯一 (tenant_id, user_id, template, channel)
)
```

```
GET /admin/notifications/preferences    我的偏好（含默认值）
PUT /admin/notifications/preferences    批量保存
```

规则：

- 未设置时按租户级 `notification-settings` 决定（继承）
- 显式关闭后，**站内信仍然写入**（否则用户永远不知道自己错过了什么），只是不推送外部渠道
- 模板清单来自 `notify_template` 表，未知模板 → 400

### 3.3 权限码

新增三个码，避免与「维护渠道」混用：

```
system:notify:list      查看收件箱与投递记录
system:notify:manage    通道/模板/投递重试
system:notify:read      标记已读（默认所有登录用户都有，可不单独授权）
```

`system:setting:write` 保留给配置中心。旧的通道接口改挂 `system:notify:manage`。

> 这属于**权限语义调整**：现有超管会自动获得（S1b 的增量补权），
> 其他角色若已授权 `system:setting:write`，需要在升级说明中列出「需补授 `system:notify:manage`」。

### 3.4 菜单投影

```
通知中心   /system/notifications        system:notify:list
通知渠道   /system/notifications/channels  system:notify:manage
```

两个页面都挂在「系统」目录下。投影一致性测试（S1b）会强制校验。

### 3.5 接口契约补充

以下路径均带 `/api/v1` 前缀，响应使用统一 envelope：

| 方法 / 路径 | 权限 | `data` / 请求 |
|---|---|---|
| `GET /admin/notifications` | `system:notify:list` | `{list,total,page,page_size}`；`unread=true/false`、`template`、`channel`（默认 `inbox`） |
| `GET /admin/notifications/unread-count` | `system:notify:list` | `{count}`，仅自己的未读站内信 |
| `POST /admin/notifications/read-all` | 登录即可（`system:notify:read` 默认能力） | `{updated}`，仅自己的未读站内信 |
| `PUT /admin/notifications/{id}/read` | 登录即可（`system:notify:read` 默认能力） | `{}`，重复调用保留首次 `read_at`；非本人/不可见通知 404 |
| `GET /admin/notifications/preferences` | 登录即可 | `{preferences:[{template,channel,enabled,inherited}]}` |
| `PUT /admin/notifications/preferences` | 登录即可 | 请求 `{preferences:[{template,channel,enabled}]}`；返回同 GET 的有效偏好 |
| `GET/PUT /admin/notification-settings` | `system:notify:manage` | 仅返回 `email_enabled` / `webhook_enabled`，写入配置仍加密 |
| `GET /admin/notifications/deliveries` | `system:notify:list` | 脱敏投递分页；其余 F3 管理/重试接口均使用 `system:notify:manage` |

- 偏好只覆盖当前登录用户，模板来源为本租户/全局 `notify_template`。
- 外部渠道为 `email/sms/wecom/dingtalk/webhook`；站内信固定保留，不接受关闭站内信的偏好。
- 未覆盖的偏好继承租户通知设置及已启用的 F3 服务商；偏好不是服务商配置，不会创建或启用服务商。
- PUT 对提供的键批量 upsert，未提供的键保持原状；1–500 项，未知模板/渠道、缺少 `enabled` 或重复键均 400，整批回滚。
- 新增写接口及单条已读/租户设置支持可选 `Idempotency-Key`；同键同请求重放首次结果，不重复更新/审计；不同请求 409。
- 旧通道与 F3 均在入队和实际发送前检查显式关闭；管理员主动发起的服务商连通性测试不受个人偏好限制。

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 未读数 | 3 条未读 → `unread-count` 返回 3 |
| 2 | 全部已读 | 调用后未读数为 0，且返回 updated=3 |
| 3 | 筛选 | `unread=true` 只返回未读；`template` 过滤正确 |
| 4 | 越权 | A 用户看不到 B 用户的站内信；`read-all` 不影响他人 |
| 5 | 偏好继承 | 未设置偏好时按租户设置生效 |
| 6 | 偏好覆盖 | 关闭某模板的邮件后，站内信仍写入、邮件不投递 |
| 7 | 未知模板 | PUT 未知模板 → 400 |
| 8 | 权限 | 无 `system:notify:manage` 调用通道接口 → 403；有则成功 |
| 9 | 菜单投影 | `/auth/menus` 出现两个新页面；投影一致性测试通过 |
| 10 | 契约 | `system.yaml` 更新，双向一致 |
| 11 | 全量 | `scripts/verify.ps1 -Integration -Migration` 通过 |

## 5. 升级说明

- 已有环境升级后需重跑 `seed`（补权限码与菜单，密码不重置）
- seed 提交后自动递增当前默认租户所有用户的授权缓存版本，`/auth/menus` 下一次请求即可看到通知中心、通知渠道等新增项，无需清缓存或重新登录，其他租户不受影响。若输出缓存失效警告，seed 仍已成功；先恢复 Redis 并重跑 seed，仅在失效失败时使用 [本地开发文档 §9](../api/local-dev.md#9-升级后更新权限与菜单) 的应急清理命令。
- 授权了 `system:setting:write` 但未授权 `system:notify:manage` 的**非超管**角色，
  升级后调用通道接口会 403 —— 需管理员补授

## 6. 不做

- 通知模板可视化编辑（只提供接口）
- 站内信删除/归档
- 移动端推送
- 按店铺责任人分发（首版全租户管理员，见计划文档）

## 7. Codex prompt

```
实现 docs/tasks/S3a-inbox.md。

必读：本卡、internal/kernel/notifier/http.go（现有 inbox 三接口）、
internal/kernel/notifier/channel/（F3 的 RegisterManagement）、
internal/bootstrap/credential_notification_http.go、
internal/modules/identity/seed_catalog.go、api/openapi/system.yaml

范围：
1. 收件箱：unread-count、read-all、列表新增 unread/template 筛选
2. 通知偏好：新表 + GET/PUT，未设置时继承租户设置
3. 权限：新增 system:notify:list / manage / read，通道接口改挂 manage
4. 菜单投影：通知中心、通知渠道两个页面（挂在 /system 下）
5. OpenAPI 更新

约束：
- 站内信只按 target='user:<id>' 过滤，禁止跨用户
- 偏好显式关闭只影响外部渠道，站内信仍写入
- 迁移号从 000027 起

验收按卡片 §4 十一条逐条贴证据。
完成后停下报告，不要做前端。
```
