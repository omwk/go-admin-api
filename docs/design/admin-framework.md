# 通用后台框架设计方案

- 定位：**可复用的多租户后台基础框架**，业务域（商品/订单/同步/计费）作为可插拔模块后置
- 盘点基线：`2321bd8`（接口清单见 `docs/api/inventory.md`）
- 目标：让「一个新后台」能在框架层直接开工，而不是每个项目重写身份、权限、通知、配置

---

## 1. 分层

```
kernel/          与业务无关的能力：db / cache / crypto / idem / outbox / httpx / audit / notifier / scope
platform/        框架级领域：identity(用户/角色/菜单/部门/会话) · config(系统配置) · dict(字典)
                 · notify(通知中心) · file(文件) · audit(审计查询) · tenant(租户)
modules/         业务域：catalog / order / sync / share / billing / gateway …
apps/            api / worker / cli
```

**判断标准**：如果换一个业务（比如做 CRM 而不是电商），这段代码还要不要？要 → 平台层；不要 → 业务域。

按这个标准，现在最缺的恰恰是**平台层**。

---

## 2. 缺失清单

### P0 · 没有它框架不成立

| # | 能力 | 现状 | 缺口 |
|---|---|---|---|
| 1 | 账号生命周期 | 只有登录/刷新/注销 | 邀请注册、忘记密码、改自己密码、改自己资料、我的会话、强制改密 |
| 2 | 部门管理 | 表存在、`dept_id` 必填 | **无任何部门接口**，建用户时无法选部门 |
| 3 | 系统管理回读 | 只有写接口 | 用户/角色/菜单的详情与关联回读（12 个 GET） |
| 4 | 邮件通道 | 仅 env 配置，单一实现 | 多服务商、DB 配置、连通性测试、模板、投递日志 |
| 5 | 组织与权限初始化 | seed 写死 | 新租户开通时如何生成默认角色/菜单/管理员 |

### P1 · 通用后台标配

| # | 能力 | 现状 | 说明 |
|---|---|---|---|
| 6 | 系统配置中心 | `sys_setting` 表存在，无管理接口 | 分组、类型校验、只读/可写、变更审计 |
| 7 | 数据字典 | 状态枚举硬编码在代码与前端 | `sys_dict`：类型 + 项 + 排序 + 启用；前端下拉与展示统一取值 |
| 8 | 审计日志查询 | `audit_log` 表存在，无接口 | 按操作者/资源/时间/结果过滤；只读、不可改 |
| 9 | 通知中心 | inbox 表 + 业务写入，无通用接口 | 列表、已读、全部已读、偏好设置 |
| 10 | 文件管理 | 上传/下载可用 | 列表、删除、引用检查、存储抽象（MinIO/OSS/S3） |
| 11 | 登录安全 | 5 次失败锁 15 分钟 | 登录历史、设备列表、手动解锁、密码策略（长度/复杂度/有效期/历史） |
| 12 | 错误码字典 | 常量散落各处 | `GET /meta/error-codes` 供前端做 i18n 映射 |
| 13 | 租户管理 | 表存在，无接口 | 见 §6 的决策问题，可能不需要 |

### P2 · 按需

| # | 能力 | 触发条件 |
|---|---|---|
| 14 | 导入（Excel/CSV） | 业务需要批量录入 |
| 15 | 定时任务管理 | 运维要自助改 cron |
| 16 | Webhook 出站 | 客户要事件推送 |
| 17 | 回收站 / 软删恢复 | 误删需要找回 |
| 18 | 仪表盘聚合 | 首页要 KPI |
| 19 | MFA（TOTP/短信） | 有合规或安全要求 |
| 20 | SSO（OIDC/LDAP/企微/钉钉） | 企业客户要求 |
| 21 | 后端 i18n | 需要多语言界面文案 |
| 22 | OpenAPI 聚合 | 对外发布 API 文档 |

---

## 3. 邮件通道设计（多服务商）

### 3.1 目标

- 支持常见服务商，可切换、可回退
- 配置存库、密钥加密、可测试、有投递日志
- 模板可管理，邀请/重置/通知共用

### 3.2 数据模型

```
email_provider
  id / tenant_id(可空=全局) / name / kind / config(jsonb, 加密) /
  from_address / from_name / enabled / priority / created_at

kind ∈ smtp | sendgrid | mailgun | aliyun_dm | tencent_ses | resend | aws_ses

email_template
  id / tenant_id / code / locale / subject / body / format(html|text) /
  version / enabled

email_delivery
  id / tenant_id / template_code / to_hash / provider_id /
  status(pending|sent|failed) / attempts / last_error / created_at / sent_at
```

### 3.3 发送链路

```
业务调用 notifier.Send(模板码, 收件人, 变量)
  → 取模板（租户覆盖 > 全局）
  → 渲染（变量白名单，禁止任意表达式）
  → 选 provider（按 priority，enabled 且未熔断）
  → 投递；失败换下一个；全部失败 → 记 failed + 站内通知管理员
  → 记 email_delivery（收件人只存哈希 + 域）
```

### 3.4 各服务商差异

| kind | 认证 | 备注 |
|---|---|---|
| smtp | host/port/user/pass/TLS | 通用兜底，企业邮箱都支持 |
| sendgrid | API Key | 海外常用 |
| mailgun | API Key + domain | |
| aliyun_dm | AccessKey + Region | 国内，需先申请发信地址与模板 |
| tencent_ses | SecretId/SecretKey + Region | 国内 |
| resend | API Key | 新兴，配置最简 |
| aws_ses | AK/SK + Region | 全球 |

**首版建议**：实现 `smtp` + `aliyun_dm` + `sendgrid` 三个即可覆盖绝大多数场景；其余按同一接口扩展。

### 3.5 管理接口

```
GET    /admin/notifications/providers
POST   /admin/notifications/providers
PUT    /admin/notifications/providers/{id}
DELETE /admin/notifications/providers/{id}
POST   /admin/notifications/providers/{id}/test     # 发一封测试邮件
GET    /admin/notifications/templates
PUT    /admin/notifications/templates/{code}
GET    /admin/notifications/deliveries              # 投递记录，含失败原因
```

### 3.6 安全

- `config` 字段用现有 Sealer 加密（AAD 绑 provider id + kind）
- 测试接口限流，防止被当发信机
- 收件人只存 `hash + 域名`，不存完整邮箱
- 模板渲染只允许白名单变量，禁止表达式求值

---

## 4. 账号生命周期设计

| 能力 | 接口 | 要点 |
|---|---|---|
| 邀请注册 | `POST /admin/invites`、`GET`、`DELETE /{id}`、`POST /auth/invite/accept` | 32 字节令牌，库中存哈希；默认 7 天、单次；可预置部门与角色 |
| 忘记密码 | `POST /auth/password/forgot`、`POST /auth/password/reset` | 与邀请共用令牌设施；30 分钟、单次 |
| 修改自己密码 | `PUT /auth/password` | 校验旧密码；成功后撤销**其他**会话 |
| 修改自己资料 | `PUT /auth/profile` | 昵称/头像；邮箱变更需二次验证（P2） |
| 我的会话 | `GET /auth/sessions`、`DELETE /auth/sessions/{sid}`、`POST /auth/logout-all` | 只操作自己的 |
| 强制改密 | 登录响应带 `password_change_required` | 仅管理员建号路径需要；邀请注册不需要 |

---

## 5. 建议批次

| 批次 | 内容 | 人日 |
|---|---|---:|
| **F1 账号可用** | 邀请注册、忘记密码、改密、改资料、我的会话 | 6 |
| **F2 组织完整** | 部门 API、系统管理回读 12 个 | 3 |
| **F3 通知通道** | email provider + 模板 + 测试 + 投递日志 | 5 |
| **F4 配置与字典** | 系统配置 API、数据字典、错误码字典 | 4 |
| **F5 审计与文件** | 审计查询、文件管理、存储抽象 | 4 |
| **F6 安全增强** | 登录历史、解锁、密码策略 | 3 |
| **合计** | | **25** |

F1 依赖 F3 的邮件通道（邀请与重置都要发信）。若 SMTP 暂不可用，邀请链接可由管理员复制转达，不阻塞。

---

## 6. 需要决策的问题

### 6.1 租户模型（影响最大）

1. **这套系统是单租户自用，还是对外多租户 SaaS？**
   - 单租户：租户管理整块可砍，`tenant_id` 保留但不暴露
   - 多租户：需要租户 CRUD、开通流程、配额、租户级配置覆盖
2. **一个用户能否属于多个租户？**（决定 `sys_user` 是否要拆成「账号 + 租户成员」两张表）

### 6.2 组织

3. **部门是几级树？能否跨租户？** 是否属于「岗位/职位」而不是树？
4. **用户登录标识用什么？** 用户名 / 手机号 / 邮箱，是否都允许？

### 6.3 安全

5. **是否要 MFA？** 若需要，TOTP 还是短信（短信又要接服务商）
6. **是否要 SSO？** 企业微信 / 钉钉 / OIDC，这决定要不要做第三方登录适配层
7. **密码策略**：最小长度、复杂度、有效期、历史不可复用条数各是多少

### 6.4 通知

8. **除邮件外，是否要短信、企业微信、钉钉、Webhook 通道？** 需不需要统一的「通道配置」抽象
9. **邮件服务商优先接哪几个？**（建议 SMTP + 阿里云 + SendGrid）

### 6.5 其他

10. **文件存储要支持哪些？** MinIO / 阿里云 OSS / AWS S3
11. **是否需要导入（Excel/CSV）？** 哪个模块先要
12. **前端是否要中英双语？** 决定后端错误文案是否也要 i18n
13. **审计日志保留多久、谁能查、是否要导出？**
