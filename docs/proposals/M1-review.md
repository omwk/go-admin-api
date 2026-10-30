# M1 设计评审

> 状态：已裁决（2026-10-31）。Q1=B，Q2=A 改 depguard + DryRun，Q3=A 并按表规模分档外键，Q4=B 加 30 秒宽限/20109，Q5=A，Q6=C 仅保留版本键 + 同步 INCR；outbox 重试交 M3。登录按用户名锁定、IP 只限流；bcrypt cost 12 保留至 M10。执行入口为 M1a/M1b，M1a 验收合并后打 `v0.M1a`。下文保留方案对比，已否决方案不作为实现依据；执行规范见 ADR-011/012/013 与 M1 共享卡。

## 1. 规格冲突与歧义

1. **权限码层级（已解决）。** 初稿的两段/三段语法混用，可能按资源命名，也可能按模块+资源命名；已选 Q1=B，统一 `module:resource:action` 且首段等于 module，PRD、模型、任务卡示例同步。
2. **菜单响应（已解决）。** 初稿可能被理解为拆两次请求或一次聚合；现 M1 §8 按 PRD REQ-IAM-011 返回 `{user, menus, permissions}`，同时保留 `/auth/me`。
3. **超管数据范围（已解决）。** 初稿与模型对是否绕过数据范围的说明相反；现 M1 §9.5 与模型一致：is_super 只绕 RBAC，super_admin 由 `data_scope=all` 获得全量。
4. **会话关联（已解决）。** 单 token 记录不足以稳定表示在线会话；已选独立 sid/family，在模型增加 session_id、replaced_by_token_id、revoked_reason，并在 JWT 带 sid。

## 2. 实现风险

1. **P0 数据越权。** 受限 repo 查询漏套 Scope 会泄漏他人数据；按 Q2 配置 depguard 和逐方法 DryRun 矩阵，覆盖详情/计数/join 等路径；不把 import 检查等同于 SQL 正确性证明。
2. **P1 授权陈旧。** 按 Q6 版本键 + 提交后同步 INCR 防止旧缓存写回；正常路径 5 秒内生效。M1 不做 outbox 重试，失效失败告警，Redis 不可用时拒绝服务，遗留缓存由 5 分钟 TTL 收敛；故障重试交 M3。
3. **P1 refresh 并发与重放。** 按 Q4 只对 rotated 旧 token 允许 30 秒 20109，不重新计时；超过 30 秒才撤 family。登出/强踢不走宽限，刷新与撤销按 sid 串行，防止撤销后产生可用新 token。
4. **P1 菜单/权限漂移。** 配置表复合外键与 service 事务校验防止悬空/跨租户引用；角色菜单授权与接口权限仍需独立检查，菜单显示不能直接授予 API 权限。
5. **独立交付依赖。** audit/cache/query 门面需在 M1a 补齐；最小 seed 和认证 OpenAPI 也归 M1a，不能依赖 M1b 才可启动。不新增第三方依赖，不追改已合并迁移。

## 3. 方案对比

### Q1 权限码用两级还是三级

**方案 A：两级 `resource:action`**

- 做法：只保留资源和动作两段，module 仅在分组字段中保存；要求资源名在租户内全局唯一。此方案已否决，不保留为有效权限码示例。
- 优点：前端展示短，字符串规则简单。
- 缺点：模块扩到 11 个本身不会自动撞名，但不同模块若复用同一资源名和动作就会冲突；需要维护全局资源名册。
- 对已有表与接口的影响：表数量、字段和记录数量不变；若采纳需改 M1 §7/§8 中权限码种子和接口权限声明，`/auth/menus` 的 `permissions` 字符串仍较短。
- 推荐：否。
- 工作量：约 0.5–1 人日。

**方案 B：三级 `module:resource:action`**

- 做法：固定三段，如 `system:user:list`、`shop:store:create`；module 字段必须分别为 system、shop。module 首段一致性由 service/种子/测试验证，实际入库和接口返回均为具体完整码，不保存通配符。
- 优点：模块命名空间隔离；模块数增长或不同模块出现同名资源时不易撞码；复杂系统模块的资源边界清楚。
- 缺点：前端展示和配置字符串更长；module 信息在 code 中重复，需保持一致。
- 对已有表与接口的影响：`sys_permission` 行数不因层级变化而增加，`code` 字符串会变；种子、RequirePerm 声明和 `/auth/menus` 的权限数组值需统一，PRD 与任务卡示例已按裁决同步。
- 推荐：是，已裁决采用。11 个模块不会自动增加权限行数；规模取决于实际资源/动作组合，按 module 分组即可保持前端可读性。
- 工作量：约 1–2 人日，含种子与契约示例统一。

### Q2 数据权限用显式 Scope 还是 GORM 全局注入

**方案 A：显式 Scope，加不可绕过的 repo 查询门**

- 做法：按 M1 §9.5 显式应用 Scope，bootstrap 注入不暴露原始 GORM handle 的 ScopedQuery 门面；使用现有 golangci-lint 的 depguard 禁止 repo 直连 GORM/kernel DB/SQL 驱动，不写自研 AST。逐 repo 方法建立 DryRun 矩阵，以 Go reflection 比较 repo 接口方法与用例登记集合，缺项失败；核验列表/详情/计数/更新删除/join 的 tenant 条件、五种 scope、别名及缺失身份拒绝路径，另做跨 owner 集成测试。认证/seed 使用受限专用查询能力，同样登记测试，禁止通用跳过 Scope 开关。
- 优点：查询范围在调用点可见；join、子查询的目标表明确；depguard 在 lint 阶段阻止禁用导入，方法登记检查在测试阶段暴露漏测。
- 缺点：depguard 只能约束导入；DryRun 矩阵须随新增 repo 方法更新，不能只测公共 Scope 函数。特殊查询需明确受保护表/列和受控例外。
- 对已有表与接口的影响：不改表和 HTTP 契约；复用 `sys_user.dept_id`、`sys_dept.path`、`sys_role_dept` 与业务表 owner 列。超管数据范围按本节规格冲突决策，推荐仍执行其角色 `data_scope`。
- 推荐：是，符合任务卡“不做隐式全局注入”的选择，并提供了可执行的防漏措施。
- 工作量：约 1.5–2.5 人日，含门面、depguard 配置与范围测试。

**方案 B：GORM 全局 callback 注入**

- 做法：注册查询 callback，根据 context 中的用户 scope 对受保护模型自动添加条件。
- 优点：简单查询不需要每个 repo 显式调用，遗漏表面上较少。
- 缺点：join/alias、子查询、Raw SQL、表名动态拼接和跨表查询的 scope 归属难判；可能重复或加错条件，问题不易从调用点发现。
- 对已有表与接口的影响：表和 API 不变；需维护全局受保护模型注册表及 join 规则，且与 M1 §9.5 已选的显式设计相反。
- 推荐：否。
- 工作量：约 3–5 人日，复杂 join 仍需逐条补规则和测试。

### Q3 sys_menu 与 sys_permission 是否合并

**方案 A：保持分表，强化引用完整性**

- 做法：菜单管理展示，权限点管理 API 能力；配置类小表及其关联表建物理外键，菜单以 `(tenant_id, permission_code)` 引用权限点并 RESTRICT 删除。无权限关联时用 NULL；service 事务校验租户/引用类型，受引用 code 不裸改；业务大表不建物理外键，应用校验及一致性检查兜底（ADR-011）。
- 优点：UI 路由和 API 能力可以独立存在；一个权限点可不对应侧边菜单，一个菜单也不必有按钮权限；职责与现有模型一致。
- 缺点：角色菜单与角色权限仍需分别授权。最易出现“菜单/按钮可见，但 API 返回 403”或“菜单隐藏，但已授权 API 仍可调用”；前者需配置校验和分配授权测试，后者是展示与 API 授权独立的结果，禁止把菜单隐藏视为撤权。
- 对已有表与接口的影响：保留 `sys_menu`、`sys_permission`、`sys_role_menu`、`sys_role_permission` 和现有 CRUD 路径；需增加复合外键/删除约束。无论存储是否合表，`/auth/menus` 按 PRD 应返回包含 `user`、`menus`、`permissions` 的聚合对象。
- 推荐：是，已裁决采用；只对配置小表及其关联建物理外键，业务大表不建。
- 工作量：约 1–1.5 人日，含迁移约束、service 校验和测试。

**方案 B：合并为带类型的权限/菜单对象表**

- 做法：用一张带 `kind` 的表存 route/menu/button/API permission，并以统一角色关联表授权。
- 优点：单一对象 ID，减少菜单字符串引用；统一对象管理与授权入口。
- 缺点：菜单路由字段对纯 API 权限为空、API 权限字段对目录为空；不同对象生命周期和授权语义被塞进同一行，形成新的类型约束；不是所有 API 权限都应有菜单。
- 对已有表与接口的影响：需重建 `sys_menu`、`sys_permission` 及两张角色关联表的数据与约束；菜单、权限 CRUD/授权接口需要兼容层或调整，风险高于单加引用约束。
- 推荐：否。
- 工作量：约 3–5 人日，含数据迁移和接口回归。

### Q4 refresh 轮换对多端登录的影响

在推荐的 family 方案下，PC 刷新只轮换 PC 自己的 `sid` 链，手机不会被踢下线；只有对手机对应的 `sid` 执行登出、强制下线，或该 family 发生 refresh 重放时，手机会失效。

**方案 A：只撤销旧 refresh token**

- 做法：每次刷新只把当前 `token_hash` 对应行置为 `revoked_at`，新建一条 refresh 记录；PC 和手机各自持有独立记录。
- 优点：严格按 M1 现有 token 轮换描述实现；设备之间自然互不影响，不需增加列。
- 缺点：无法可靠把同一次登录的多代 token 归为一个在线会话；重放旧 token 时不能只撤销对应设备的后续 token；强制下线接口只能按单 token 操作。
- 对已有表与接口的影响：表和 JWT claims 不变；`GET /admin/sessions`、按会话 `DELETE` 的语义无法由当前记录稳定实现。
- 推荐：否。
- 工作量：约 0.5–1 人日，但未满足会话链需求。

**方案 B：每次登录建立独立 session/family**

- 做法：每次登录生成 sid（ULID），模型增加 session_id、replaced_by_token_id、revoked_reason，JWT 带 sid。DB 事务原子轮换并记录首次撤销时间/rotated 原因；提交后写 Redis。family 有效时，rotated 旧 token ≤30 秒复用返回 409/20109，不撤 family、不签发、不延长宽限；超过 30 秒重放才撤整族。登出/强踢立即 20103。刷新与撤销按 sid 串行，PC/手机各自独立 sid。
- 优点：PC 刷新只轮换 PC 链，手机不会因此下线；支持会话列表、单设备强制下线和同 family 重放检测。
- 缺点：客户端需等待成功刷新请求并使用新 token；成功响应丢失且拿不到新 token 时要重新登录，不能靠 20109 再次签发。超过宽限的旧 token 重放会撤该设备 family。
- 对已有表与接口的影响：`sys_refresh_token` 增加 session 关联；JWT claims 增加 `sid`，更新 `JWTAuth` 检查和 session API；接口路径不变，session id 可作为 `/admin/sessions/{id}`。
- 推荐：是，才能同时满足多端隔离与 REQ-IAM-010。
- 工作量：约 2–3 人日，含迁移、原子轮换、30 秒边界/20109、重放与多端测试；与 Q5 的会话检查共用，不重复累计。

### Q5 登出、强制下线、refresh 轮换的 Redis 失效 key 与 TTL

**方案 A：按对象类型拆分 tombstone key**

- 做法：统一前缀和版本，类型与标识均在 key 中，值固定为 `1`：
  - access JTI：`auth:v1:revoked:access:jti:{jti}`，TTL 为 `max(access.exp-now, 0)`，最多 15 分钟。
  - refresh token：`auth:v1:revoked:refresh:sha256:{token_hash}`，TTL 为 `max(refresh.expires_at-now, 0)`，最多 7 天。
  - session/family：`auth:v1:revoked:session:{sid}`，TTL 为该 family 当前最晚 refresh `expires_at-now`，最多 7 天；鉴权中间件校验 JWT `sid` 是否被撤销。
  - refresh 轮换只写旧 refresh hash key，refresh endpoint 仍需查 DB 区分 20109 宽限；登出/强踢写 session key，并标记可确定的目标 JTI/hash。session 标记覆盖整族 access，不能把管理员 JTI 当作目标。过期对象不写 key，重复写不得缩短撤销覆盖期。
- 优点：JTI、refresh hash、session id 处于独立命名空间，不会互相覆盖；TTL 与对象有效期匹配；能按 session 整体踢下线。
- 缺点：需要 sid 与 family 查询；Redis 不可用时 fail closed（503/10002），已持久撤销的 session 在 Redis 恢复后仍须拒绝，不能只靠易失 key。
- 对已有表与接口的影响：不改 API 路径；依赖 refresh 表新增 session 关联和 JWT `sid`；key 仅含 JTI、token hash、随机 session id，不存原始 refresh token。
- 推荐：是。
- 工作量：约 1–1.5 人日，含 key 封装和 Redis 行为测试。

**方案 B：每个 session 使用一个 Redis Hash 存失效项**

- 做法：`auth:v1:session:{sid}:revocations` 下以 `access:{jti}`、`refresh:{token_hash}`、`session` 为 field，整 key TTL 取该 session 最晚 refresh 到期时间。
- 优点：按 session 查询/清理集中；强制下线时一次写入即可标记 session。
- 缺点：Hash field 无法按各 token 独立 TTL；需自行清除已过期 field，长期滑动刷新会延长整 key 并累积字段；读取权限时仍需检查 session 和 token field。
- 对已有表与接口的影响：同样依赖 `sid` 和 refresh family；HTTP 不变，Redis 数据格式与 key 管理更复杂。
- 推荐：否。
- 工作量：约 2–3 人日，含字段清理与并发测试。

### Q6 权限与菜单变更何时生效

**方案 A：只依赖 5 分钟 TTL**

- 做法：缓存到期后才重新加载权限与菜单。
- 优点：实现最简单，无失效 fan-out。
- 缺点：旧授权最长继续生效 5 分钟，达不到 M1 §9 的 5 秒验收；收回权限时存在明确窗口。
- 对已有表与接口的影响：不改表/API，但不能满足正常路径 5 秒生效要求；不采用无版本的权限缓存键。
- 推荐：否。
- 工作量：约 0.5 人日。

**方案 B：DB 提交后同步删除受影响用户缓存**

- 做法：角色权限/菜单授权修改提交后，找出该角色成员并删除 `perm` 与 `menu` 缓存；用户角色变更则删除该用户两类缓存；权限码或菜单引用变更清理对应角色用户缓存。
- 优点：直接，正常情况下立刻生效；无需改变响应契约。
- 缺点：角色成员数量大时 fan-out 较重；删除失败或并发请求在删除后写回旧缓存会造成陈旧数据；需要重试机制。
- 对已有表与接口的影响：表/API 不变；依赖 `sys_user_role`、`sys_role_permission`、`sys_role_menu` 找出受影响用户。
- 推荐：否，单独用 DEL 无法稳妥处理写回竞争和 Redis 故障。
- 工作量：约 1–2 人日。

**方案 C：版本化缓存 + 同步 INCR（已裁决；重试交 M3）**

- 做法：无 TTL 的 `authz:ver:{tenant}:{user}`；菜单/权限分别为 `authz:menu:{tenant}:{user}:v{n}`、`authz:perm:{tenant}:{user}:v{n}`，TTL 5 分钟。授权 DB 提交后对受影响用户同步 INCR；读取先取版本，回填沿用原版本。覆盖角色权限/菜单、用户角色、角色状态/数据范围、菜单配置。M1 不写失效 outbox，重试交 M3 T3.11。
- 优点：旧缓存并发回填也只写到旧版本；Redis 正常时后端鉴权和菜单查询 5 秒内生效，5 分钟 TTL 用于清理。
- 缺点：M1 失效失败只有 Warn/指标和 TTL 兜底；Redis 不可用时拒绝服务，恢复后遗留旧缓存可能等满 5 分钟。已打开页面菜单需前端重新拉取，后端缓存失效不会主动改页面。
- 对已有表与接口的影响：M1 不改业务表/API，Redis 增加版本 key；M3 再接入现有 outbox 设计和投递器。
- 推荐：是，已裁决仅采用版本键 + 同步 INCR 核心；故障路径明确不保证 5 秒。
- 工作量：M1 约 1–2 人日，含并发回填、正常/故障验收；M3 重试另计。各问题工作量有重叠，拆分后总估约 8–10 人日。

## 4. 未来 2 个里程碑的坑（只列不展开）

- M2：新增 repo 若绕过统一 scoped query gate，M1 的显式数据权限约束无法覆盖新查询。
- M3：接入 M1b 延后的 outbox 授权失效重试，覆盖同步 INCR 故障恢复与重复投递。

## 5. 决策清单（已确认）

1. Q1=B：固定三段，首段等于 module；有效文档与种子/接口声明统一，不留混用。
2. Q2=A 调整：显式 Scope + depguard + 逐 repo 方法 DryRun，不写自研 AST；超管仍受角色 data_scope 约束。
3. Q3=A：菜单/权限分表；只对配置类小表及其关联建物理外键，业务大表不建，固化为 ADR-011。
4. Q4=B 调整：独立 sid/family + 首次轮换后 30 秒宽限/20109，超时重放才撤 family；登出/强踢立即生效。
5. Q5=A：access JTI、refresh hash、sid 三套独立命名与各自剩余 TTL。
6. Q6=C 核心：版本键 + 提交后同步 INCR；M1 无 outbox 重试，M3 接入。
7. 登录：用户名锁定、IP 只限流；bcrypt cost 12 先保留，M10 压测裁定。
8. 交付：M1a 认证/会话/最小种子/菜单聚合独立验收后打 v0.M1a，再做 M1b 管理/授权/范围/缓存。

## 6. 任务卡与 PRD 的冲突

无。初稿的权限码、聚合响应、超管数据范围、session 关联及过期错误码不一致已同步到 PRD/模型/工程约定/ADR 和 M1 任务卡；M1a/M1b 的需求映射、Redis 故障边界及 M3/M10 交接均已记录。
