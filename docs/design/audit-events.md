# 审计事件登记

BE-IDN-009。此表是 audit_log action 的登记表；新增 action 必须同时更新登记与一致性测试。权限码仍使用冒号，写入审计时统一为点号。

操作者与资源名称在业务事务中固化；查询只读快照列。名称按 Unicode 字符截断至 128 字符。未知账号不保存用户输入的账号名。后台任务使用 system 身份；没有 HTTP 请求时 IP/UA 为空。

更新只记录变化字段，无变化不记；新建 before=null；删除 after=null；跨租户读每请求一条，before=null，after={accessed_tenants,count}。失败记录保存错误码与固定安全文案。敏感值统一为 ***。

BE-IDN-011 的租户彻底删除保留 `after={code,rows:{table:count}}`；rows 仅允许非负整数，包含令牌表的计数而不包含令牌内容。此事件与删除在同一事务中写入，归属平台操作者租户；原审计记录保留。

| Action | 含义 |
|---|---|
| `billing.adjustment.apply` | 计费 / 调账 / 执行 |
| `billing.adjustment.open` | 计费 / 调账 / 创建 |
| `billing.entitlement.grant` | 计费 / 权益 / 授权 |
| `billing.plan.save` | 计费 / 套餐 / 保存 |
| `billing.reconcile.compensate` | 计费 / 对账 / 补偿 |
| `billing.statement.generate` | 计费 / 账单 / 生成 |
| `billing.statement.issue` | 计费 / 账单 / 出账 |
| `billing.statement.issue.reason` | 计费 / 账单 / 出账 / 原因说明 |
| `billing.wallet.topup` | 计费 / 钱包 / 充值 |
| `file.delete` | 文件 / 删除（元数据与持久化清理事件同事务） |
| `file.upload` | 文件 / 上传（名称、用途、大小与 SHA256） |
| `gateway.client.create` | 网关 / 客户端 / 创建 |
| `gateway.client.status` | 网关 / 客户端 / 状态调整 |
| `gateway.client.update` | 网关 / 客户端 / 更新 |
| `gateway.resource.grant` | 网关 / 资源 / 授权 |
| `gateway.resource.revoke` | 网关 / 资源 / 撤销 |
| `gateway.route.grant` | 网关 / 路由 / 授权 |
| `gateway.route.revoke` | 网关 / 路由 / 撤销授权 |
| `gateway.route.save` | 网关 / 路由 / 保存 |
| `gateway.secret.revoke` | 网关 / 凭据 / 撤销 |
| `gateway.secret.rotate` | 网关 / 凭据 / 轮换 |
| `gateway.secret.reveal` | 网关 / 凭据 / 查看明文（审计不含明文） |
| `identity.login` | 身份 / 登录（成功或失败） |
| `identity.password.reset.link` | 身份 / 密码 / 重置 / 链接 |
| `identity.reauth` | 身份 / 重认证（成功或失败） |
| `identity.register` | 身份 / 注册 |
| `identity.register.verify` | 身份 / 注册 / 验证 |
| `notification.delivery.retry` | 通知 / 投递 / 重试 |
| `notification.personal.delete` | 通知 / 个人目的地 / 删除（加密快照，不含明文 URL） |
| `notification.personal.save` | 通知 / 个人目的地 / 保存（仅掩码与状态） |
| `notification.personal.test` | 通知 / 个人目的地 / 试发（仅投递 ID） |
| `notification.preferences.update` | 通知 / 偏好 / 更新 |
| `notification.provider.delete` | 通知 / 渠道 / 删除 |
| `notification.provider.save` | 通知 / 渠道 / 保存 |
| `notification.provider.test` | 通知 / 渠道 / 试发 |
| `notification.settings.update` | 通知 / 配置 / 更新 |
| `notification.template.save` | 通知 / 模板 / 保存 |
| `notification.template.test` | 通知 / 模板 / 试发（模板与投递 ID，不含收件地址或变量值） |
| `order.pii.read` | 订单 / 敏感信息 / 读取 |
| `permissions.release` | 权限 / 增量发布 |
| `share.inquiry.pii.read` | 分享 / 询盘 / 敏感信息 / 读取 |
| `share.inquiry.submit` | 分享 / 询盘 / 提交 |
| `share.inquiry.update` | 分享 / 询盘 / 更新 |
| `share.link.lifecycle.revoke` | 分享 / 链接 / 生命周期 / 撤销 |
| `share.privacy.cleanup` | 分享 / 隐私 / 清理 |
| `share.visit.read` | 分享 / 访问明细 / 读取 |
| `shop.create` | 店铺 / 创建 |
| `storage.provider.create` | 存储配置 / 新建（secret 与 access_key 仅记录 ***） |
| `storage.provider.update` | 存储配置 / 修改或切换默认（secret 与 access_key 仅记录 ***） |
| `storage.provider.delete` | 存储配置 / 删除（脱敏 before 快照，after=null） |
| `storage.provider.test` | 存储配置 / 连接测试（secret 与 access_key 仅记录 ***） |
| `sync.baseline.promote` | 同步 / 基线 / 提升 |
| `sync.drift.ack` | 同步 / 漂移 / 确认 |
| `sync.drift.ignore` | 同步 / 漂移 / 忽略 |
| `sync.drift.resolve` | 同步 / 漂移 / 解决 |
| `system.dept.create` | 系统 / 部门 / 创建 |
| `system.dept.delete` | 系统 / 部门 / 删除 |
| `system.dept.update` | 系统 / 部门 / 更新 |
| `system.dict.create` | 系统 / 字典（类型或条目） / 创建 |
| `system.dict.delete` | 系统 / 字典（类型或条目） / 删除 |
| `system.dict.update` | 系统 / 字典（类型或条目） / 更新 |
| `system.menu.create` | 系统 / 菜单 / 创建 |
| `system.menu.delete` | 系统 / 菜单 / 删除 |
| `system.menu.update` | 系统 / 菜单 / 更新 |
| `system.notify.broadcast` | 系统 / 通知 / 公告（标题、渠道、范围、人数） |
| `system.role.create` | 系统 / 角色 / 创建 |
| `system.role.delete` | 系统 / 角色 / 删除 |
| `system.role.grant` | 系统 / 角色 / 授权 |
| `system.role.update` | 系统 / 角色 / 更新 |
| `system.session.kick` | 系统 / 会话 / 强踢 |
| `system.setting.update` | 系统 / 配置组 / 更新 |
| `system.tenant.admin.invite` | 系统 / 租户 / 管理员 / 邀请 |
| `system.tenant.archive` | 系统 / 租户 / 归档 |
| `system.tenant.disable` | 系统 / 租户 / 停用 |
| `system.tenant.enable` | 系统 / 租户 / 启用 |
| `system.tenant.list` | 系统 / 租户 / 跨租户列表访问 |
| `system.tenant.members` | 系统 / 租户 / 跨租户成员访问 |
| `system.tenant.provision` | 系统 / 租户 / 开通 |
| `system.tenant.purge` | 系统 / 租户 / 彻底删除（code 与各表行数） |
| `system.tenant.read` | 系统 / 租户 / 读取 |
| `system.tenant.rename` | 系统 / 租户 / 改名 |
| `system.tenant.restore` | 系统 / 租户 / 恢复 |
| `system.tenant.template.clone` | 系统 / 租户 / 模板 / 复制 |
| `system.tenant.template.create` | 系统 / 租户 / 模板 / 创建 |
| `system.tenant.template.delete` | 系统 / 租户 / 模板 / 删除 |
| `system.tenant.template.update` | 系统 / 租户 / 模板 / 更新 |
| `system.user.create` | 系统 / 用户 / 创建 |
| `system.user.delete` | 系统 / 用户 / 删除 |
| `system.user.grant` | 系统 / 用户 / 授权 |
| `system.user.logout.others` | 系统 / 用户 / 登出 / 其他会话 |
| `system.user.profile` | 系统 / 用户 / 个人资料 |
| `system.user.reset` | 系统 / 用户 / 重置 |
| `system.user.unlock` | 系统 / 用户 / 解锁 |
| `system.user.update` | 系统 / 用户 / 更新 |
| `trade.customer.delete` | 贸易 / 客户 / 删除 |
| `trade.customer.save` | 贸易 / 客户 / 保存 |
| `trade.pricelist.delete` | 贸易 / 报价单 / 删除 |
| `trade.pricelist.items` | 贸易 / 报价单 / 明细 |
| `trade.pricelist.save` | 贸易 / 报价单 / 保存 |
| `trade.share.link.create` | 贸易 / 分享 / 链接 / 创建 |
| `trade.share.link.revoke` | 贸易 / 分享 / 链接 / 撤销 |

动态族：`trade.<entity>.<verb>` 的 entity/verb 来自现有固定调用；`sync.drift.<verb>` 仅允许 ack/resolve/ignore。测试扫描事件构造、审计 helper、权限常量与动态族，禁止未登记的静态 action。

`cli audit-prune --before <RFC3339> [--force]` 按严格小于时间清理；prod 必须 --force。事务局部维护标志仅在这次删除中生效，普通 UPDATE/DELETE/TRUNCATE 仍由触发器阻止。
