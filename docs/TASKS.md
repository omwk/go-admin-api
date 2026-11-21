# 任务编号规则与索引

- 目的：让任务卡编号能直接回答三个问题——**哪个仓库、哪个领域、新增还是修复**
- 状态：2026-11-14 起生效；旧编号**冻结不改名**，只在此登记

---

## 1. 旧编号为什么乱

| 前缀 | 当初含义 | 问题 |
|---|---|---|
| `M0`–`M10` | 后端里程碑（Milestone） | 清晰，保留；tag 用 `v0.Mx` |
| `F1`/`F2`/`F3` | 平台层批次（Framework） | 与前端 `FE-1` 只差一个字母，易混 |
| `S0`–`S5` | 系统层批次（System） | **与 M9 前的 S1/S2/S3 重号**（见下） |
| `SF-1`–`SF-6` | 系统层前端页（System Frontend） | 与 `FE-*` 并存，归属不清 |
| `BE-1`–`BE-8` | 后端任务（Backend） | 只表达仓库，不表达领域 |
| `FE-1`–`FE-3` | 前端任务 | 同上 |
| `T1`/`T2`/`T6` | 第二轮系统层批次（Tenant?） | 字母含义不明，且 T4/T5 已并入 SF-6 |
| `FOLLOWUP-*` | 缺陷跟进 | 可读，但不在序列里 |

**已经发生的重号**（这是真正会造成误解的地方）：

```
S1-timeout-budget.md      ← 早期测试预算
S1-dept-crud.md           ← 系统层部门
S2-test-runtime.md        ← 早期测试运行时
S2-config-dict.md         ← 系统层配置字典
S3-smoke.md               ← 冒烟脚本
S3a-inbox.md              ← 系统层通知（用 a 后缀避让）
```

---

## 2. 新规则

```
<仓库>-<领域>-<序号>[-<可选短名>]
```

| 段 | 取值 | 说明 |
|---|---|---|
| 仓库 | `BE` / `FE` | 后端 / 前端 |
| 领域 | 见下表 | 固定 3 字母代码 |
| 序号 | 两位数字，从 01 起 | **每个「仓库+领域」独立计数，永不复用** |

领域代码：

| 代码 | 范围 |
|---|---|
| `IDN` | 身份：登录、用户、角色、菜单、部门、租户、会话 |
| `SYS` | 系统设置：配置、字典、通知、审计、文件 |
| `CAT` | 商品、类目、库存、导出 |
| `ORD` | 订单 |
| `SHR` | 客户、报价、分享链接、询盘 |
| `BIL` | 计费、钱包、账单、对账 |
| `GWY` | 开放网关 |
| `SYN` | 同步、映射、漂移、重放 |
| `PLT` | 工程底座：迁移、seed、CI、脚本、文档工具 |
| `UI` | 前端外壳：布局、首页、404、登录页 |

**类型不进 ID**，写在卡片首行的元信息块里（因为修复可能升级为重构，ID 不应变）：

```markdown
- ID: BE-IDN-012
- 类型: 修复 | 新增 | 重构 | 文档
- 仓库: 后端
- 状态: 进行中 | 待评审 | 已合并
- 依赖: BE-IDN-011
```

示例：`BE-IDN-012-tenant-code-login.md`

---

## 3. 编号管理

- 编号由**写卡片的人**分配，分配前查本文 §4 索引，取该「仓库+领域」下一个空号
- 一个编号对应一张卡；编号不复用，即使卡片取消也保留占位
- 合并后把状态更新为「已合并」并填 PR 号

---

## 4. 索引（截至 2026-11-14）

### 4.1 后端

| 现有文件名 | 类型 | 领域 | 状态 | 说明 |
|---|---|---|---|---|
| `BE-1-user-filters.md` | 修复 | IDN | 已合并 #27 | 用户列表 keyword/status |
| `BE-4-catalog-contract.md` | 修复 | CAT | 已合并 #31 | 错误信封 + 列表类目字段 |
| `BE-5-category-descendants.md` | 新增 | CAT | 已合并 #32 | 类目后代筛选 |
| `BE-6-dev-orders.md` | 新增 | PLT | 待排期 | 开发用订单造数（业务层解锁后再做） |
| `BE-7-dict-scope.md` | 新增 | SYS | 已合并 #41 | 字典系统/业务分域 |
| `BE-8-sync-settings.md` | 新增 | SYS | 已合并 #42 | 配置中心 sync 组 |
| `F1-account.md` | 新增 | IDN | 已合并 #30 | 邀请/找回/改密 |
| `F2-org.md` | 新增 | IDN | 已合并 #28 | 部门树与 14 个回读 |
| `F3-channels.md` | 新增 | SYS | 已合并 #29 | 通知通道与投递日志 |
| `S1-dept-crud.md` | 新增 | IDN | 已合并 #33 | 部门 CRUD |
| `S1b-dept-permissions.md` | 修复 | IDN | 已合并 #34 | 部门权限投影 |
| `S1c-root-dept.md` | 修复 | IDN | 已合并 #36 | 根部门语义收敛 |
| `S2-config-dict.md` | 新增 | SYS | 已合并 #35 | 配置与字典 |
| `S3a-inbox.md` | 新增 | SYS | 已合并 #37 | 收件箱与偏好 |
| `S4-audit.md` | 新增 | SYS | 已合并 #38 | 审计查询/登录历史/解锁 |
| `S5-tenant.md` | 新增 | IDN | 已合并 #43 | 租户管理与模板开通 |
| `FOLLOWUP-S4-login-metadata.md` | 修复 | SYS | 已合并 #39 | 登录审计 IP/UA |
| `FOLLOWUP-SEED-authz-cache.md` | 修复 | PLT | 已合并 #40 | seed 后缓存失效 |
| `BE-IDN-001-tenant-code-login.md` | 修复 | IDN | 已合并 #44 | 登录改用租户 code |
| `BE-IDN-002-tenant-admin-recovery.md` | 修复 | IDN | 已合并 #45 | 租户管理员重签发 |
| `BE-IDN-003-profile-sessions.md` | 修复 | IDN | 已合并 #46 | 资料与会话字段补全 |
| `BE-IDN-004-self-registration.md` | 新增 | IDN | 已合并 #47 | 可配置自助注册 |
| `BE-IDN-005-logout-others.md` | 新增 | IDN | 已合并 #49 | 下线其他设备 |
| `BE-IDN-006-super-scope.md` | 修复 | IDN | 已合并 #48 | 超管无角色的数据范围拒绝 |
| `BE-IDN-007-scope-guard.md` | 修复 | IDN | 暂缓（可选） | 超管范围守卫（矩阵 + 登记表），系统层收尾后按需排 |
| `BE-IDN-008-dict-supply.md` | 修复 | SYS | 已合并 #50 | 字典供给修复（租户覆盖/值域/白名单） |
| `BE-IDN-009-audit-semantics.md` | 修复 | SYS | 已合并 #51 | 审计语义：快照/差异/来源/命名/搜索 |
| `S1-timeout-budget.md` | 重构 | PLT | 已合并 | 早期测试预算 |
| `S2-test-runtime.md` | 重构 | PLT | 已合并 | 早期测试运行时 |
| `S3-smoke.md` | 新增 | PLT | 已合并 | S3 冒烟脚本 |
| `S0-ci-test-sharding.md` | 重构 | PLT | 已合并 | CI 分片 |
| `D1-replay-eta-lease.md` | 修复 | SYN | 已合并 | 重放 ETA 租约 |

### 4.2 前端

| 现有文件名 | 类型 | 领域 | 状态 | 说明 |
|---|---|---|---|---|
| `FE-1.md` | 新增 | UI | 已合并 #1 | 登录与聚合菜单 |
| `FE-1.5.md` | 新增 | IDN | 已合并 #3 | 用户/角色/菜单管理 |
| `FE-2.md` | 新增 | CAT | 已合并 #2 | 商品列表与详情 |
| `FE-3.md` | 新增 | ORD | 待排期 | 订单列表与详情（含 PII 重认证） |
| `SF-1.md` | 新增 | IDN | 已合并 #4 | 部门管理页 |
| `SF-1b.md` | 修复 | IDN | 已合并 #5 | 部门入口修正 |
| `SF-2.md` | 新增 | SYS | 已合并 #6 | 通知中心与渠道 |
| `SF-3.md` | 新增 | SYS | 已合并 #7 | 审计/登录历史/解锁 |
| `SF-4.md` | 新增 | SYS | 已合并 #8 | 字典与配置页 |
| `SF-5.md` | 新增 | IDN | 已合并 #9 | 租户管理页 |
| `FE-UI-001.md` | 新增 | UI | 已合并 #10 | 登录企业代码/资料/登出/首页 |
| `FE-IDN-001.md` | 新增 | IDN | 已合并 #11 | 租户页增强（ID 列/管理员状态/重发邀请） |
| `FE-IDN-003.md` | 新增 | IDN | 已合并 #13 | 会话页下线模式 |
| `FE-IDN-004.md` | 重构 | SYS | 已合并 #14 | 状态文案改用字典 |
| `FE-IDN-005.md` | 修复 | SYS | 已合并 #15 | 审计日志可读性 |
| `FE-IDN-006.md` | 修复 | IDN | 已合并 #16 | 开通向导表单校验体验 |
| `BE-IDN-010-template-editable.md` | 新增 | IDN | 已合并 #52 | 租户模板可编辑（空白/角色编辑/复制） |
| `BE-IDN-011-tenant-delete.md` | 新增 | IDN | 已合并 #53 | 租户归档与彻底删除 |
| `FE-IDN-007.md` | 新增 | IDN | 已合并 #17 | 租户模板编辑页 |
| `FE-IDN-008.md` | 新增 | IDN | 已合并 #18 | 租户归档与删除入口 |
| `BE-IDN-012-menu-seed.md` | 修复 | SYS | 已合并 #54 | 菜单种子补全与归类 |
| `BE-IDN-013-hidden-routes.md` | 修复 | SYS | 已合并 #55 | 隐藏路由投影 |
| `BE-IDN-014-channel-errors.md` | 修复 | SYS | 已合并 #56 | 通道配置错误可诊断 |
| `FE-IDN-009.md` | 重构 | SYS | 已合并 #19 | 移除硬编码路由，菜单完全驱动 |
| `FE-IDN-010.md` | 修复 | SYS | 已合并 #20 | 通知渠道表单修正 |
| `BE-IDN-015-delivery-readability.md` | 修复 | SYS | 已合并 #57 | 投递记录可读与可操作 |
| `FE-IDN-011.md` | 修复 | SYS | 已合并 #21 | 投递记录页改造 |
| `BE-IDN-016-account-mail.md` | 修复 | IDN | 已合并 #58 | 账号邮件模板与绝对链接 |
| `BE-IDN-017-template-name.md` | 修复 | SYS | 已合并 #59 | 模板显示名（修回归） |
| `FE-IDN-012.md` | 修复 | IDN | 已合并 #22 | 账号链接与验证页适配 |
| `BE-IDN-018-notification-complete.md` | 修复 | SYS | 已合并 #61 | 通知完整性：偏好/变量/预览/公告 |
| `FE-IDN-013.md` | 修复 | SYS | 已合并 #23 | 通知前端：变量面板/角标/公告 |
| `BE-IDN-019-notification-routing.md` | 新增 | SYS | 已合并 #62 | 通知路由：个人渠道与机器人约束 |
| `FE-IDN-014.md` | 新增 | IDN | 已合并 #24 | 个人通知方式与偏好 |
| `FE-IDN-015.md` | 修复 | IDN | 已合并 #25 | 个人资料可编辑与通知设置页 |
| `BE-IDN-021-storage.md` | 新增 | SYS | 已合并 #64 | MinIO 存储配置与文件管理 |
| `BE-IDN-022-storage-lookup.md` | 修复 | SYS | 已合并 #65 | 对象存储下载（Bucket Lookup 模式） |
| `BE-IDN-023-file-types.md` | 修复 | SYS | 已合并 #66 | 上传类型白名单扩展 |
| `BE-IDN-024-storage-menus.md` | 修复 | SYS | 已合并 #67 | 存储菜单与上传人信息 |
| `BE-IDN-025-file-model.md` | 新增 | SYS | 已合并 #68 | 文件分类/访问模式/配额/缩略图/头像 |
| `BE-IDN-027-put-size.md` | 修复 | SYS | 已合并 #69 | 缩略图与头像上传失败（size=-1） |
| `BE-IDN-026-menu-component.md` | 重构 | SYS | 已合并 #70 | 菜单组件简写与头像过滤 |
| `BE-IDN-023-file-types.md` | 修复 | SYS | 已合并 #66 | 上传类型白名单扩展 |
| `BE-IDN-024-storage-menus.md` | 修复 | SYS | 已合并 #67 | 存储菜单与上传人信息 |
| `BE-IDN-025-file-model.md` | 新增 | SYS | 已合并 #68 | 文件分类/访问模式/配额/缩略图/头像 |
| `BE-IDN-027-put-size.md` | 修复 | SYS | 已合并 #69 | 缩略图与头像上传失败（size=-1） |
| `BE-IDN-026-menu-component.md` | 重构 | SYS | 已合并 #70 | 菜单组件简写与头像过滤 |
| `FE-IDN-016.md` | 新增 | SYS | 已合并 #26 | 存储配置与文件管理页 |
| `FE-IDN-017.md` | 修复 | SYS | 已合并 #27 | 文件页改版与头像上传 |
| `FE-IDN-018.md` | 重构 | SYS | 已合并 #28 | 组件注册表简写化 |
| `BE-IDN-028-thumb-worker.md` | 修复 | SYS | **待实现** | 大图缩略图异步生成 |
| `FE-IDN-019.md` | 修复 | SYS | **待实现** | 缩略图异步状态展示 |
| `BE-IDN-020-email-change.md` | 修复 | IDN | 已合并 #63 | 邮箱变更验证 |
| `FE-IDN-002.md` | 新增 | IDN | 已合并 #12 | 注册开关设置与注册页 |

### 4.3 里程碑（后端，保留旧编号）

`M0`–`M10` 见 `docs/milestones/`，tag 为 `v0.Mx`。里程碑是「一个阶段的交付」，
与任务卡不是同一层级，因此不并入新编号。

---

## 5. 今后写卡流程

```
1. 查 §4，确定「仓库+领域」下一个空号
2. 卡片首行写元信息块（ID / 类型 / 仓库 / 状态 / 依赖）
3. 在本索引追加一行
4. 合并后更新状态与 PR 号
```

> 旧卡片不改名：它们已被 PR、提交信息与 `docs/progress.md` 引用，改名会制造断链。
> 新编号从下一张卡开始使用。
