# M1a · 认证、会话与菜单聚合

- 状态：T1.0–T1.4 实现与验收通过（2026-10-31）；[最终代码 CI](https://github.com/omwk/go-admin-api/actions/runs/41137020244) 含 race、构建和镜像检查成功，待验收合并后打 tag。
- 依赖：M0；共享规格：[M1.md](M1.md)，ADR-011/012/013。
- 分支：`codex/M1a-identity-auth`。
- 预计：约 4.5 人日；覆盖共享卡 T1.0–T1.4。
- 覆盖需求：REQ-IAM-001/002/011；REQ-IAM-006 的菜单读取、009 的登录审计，以及 010 的会话底座。管理授权与强踢 API 在 M1b。
- 完成标志：全新库能 seed、登录、轮换、登出并获取 `{user, menus, permissions}`；多端隔离，认证接口契约完整。
- 发布：本卡独立验收、合并后打 **`v0.M1a`**，再进入 M1b；本次文档裁决不创建标签。

## 1. 必读

1. `AGENTS.md`。
2. `docs/04-conventions.md` §2、§3.2、§4、§5。
3. 本卡、`docs/milestones/M1.md` §6–§11。
4. `docs/03-data-model.md` §2、`docs/01-PRD.md` EPIC-1、`docs/ADR.md` ADR-011/012/013。
5. `internal/modules/identity/contract.go` 及现有目录；`internal/kernel/{cache,db,errs,httpx,logger}`；现有 bootstrap、CLI/Makefile 的装配方式。

## 2. 范围与不做

- 完成 M1 的 T1.0–T1.4：cache/audit/idgen 所需能力、ScopedQuery 门面及 depguard 配置、10 张 identity 表迁移与 model、密码/token/session 工具、认证服务、菜单聚合、最小种子及认证 OpenAPI。
- 最小种子包含根部门、admin、`super_admin(data_scope=all)`、两组菜单授权验收角色、三段权限码与菜单；幂等执行，管理员初始密码按现有种子约定从 `CH_SEED_ADMIN_PASSWORD` 读取。
- 用户名连续失败 5 次锁定 15 分钟，跨 IP 仍有效；IP 只限流，限流命中不增加用户名失败次数。bcrypt cost 保留 12，M10 再压测裁定。
- 每次登录一个 sid；只对 `rotated` 旧 refresh 提供首次轮换起 30 秒宽限（409/20109），不签发、不重置计时；超过 30 秒重放才撤整族。登出/强踢不享受宽限。
- 沿用 M1 §9.2 的三套 Redis key 与 TTL；DB 事务内无 Redis IO；Redis 故障返回 503/10002，持久撤销在恢复后仍生效。
- 不做用户/角色/菜单管理 CRUD、管理端会话 API、数据权限完整落地、版本化授权缓存、outbox 投递器和前端；这些不能作为本卡启动/验收依赖。
- M1a 不注册 M1b 管理端点，不用无鉴权的占位 handler；M1b 的测试在 M1b 编写，不用 `t.Skip` 掩盖未实现行为。

## 3. 允许改动的实现文件

- `internal/modules/identity/`：在现有骨架上补认证相关 handler/service/repo/model、permission registry、工具及测试。
- `internal/kernel/{cache,audit,idgen,db,errs}/`：所需能力、20109 及测试；保留 M0 的事务和超时行为。
- `internal/bootstrap/`、`cmd/`、`Makefile`：路由/依赖装配、幂等 `seed` 命令。
- `migrations/000002_identity_init.up.sql`、`.down.sql`：只新增；不编辑已合并的 000001。
- `.golangci.yml`：启用现有 golangci-lint 的 depguard；不自研 AST，不新增第三方依赖。
- `api/openapi/identity.yaml`：本卡五个认证/读取接口契约；本卡及 `docs/progress.md`：验收事实。
- 不修改 M0 已验收的 httpx 中间件行为；不修改共享规格的已裁决结论。

## 4. 接口

| 方法 | 路径 | 要求 |
|---|---|---|
| POST | `/api/v1/auth/login` | 签发 access/refresh，用户名锁定与 IP 限流，登录审计 |
| POST | `/api/v1/auth/refresh` | 原子轮换、30 秒宽限、超时重放撤 family |
| POST | `/api/v1/auth/logout` | 撤当前 sid，其他 sid 不受影响 |
| GET | `/api/v1/auth/me` | 当前用户、角色、权限码 |
| GET | `/api/v1/auth/menus` | 按角色过滤，统一响应 `data={user, menus, permissions}` |

## 5. 验收与交接

| 项 | 自动化/命令证据 |
|---|---|
| 独立启动 | 全新库 `make migrate-up && make seed && make run-api`；重复 seed 不重置密码 |
| 迁移 | `make migrate-up`、`make migrate-down` 各通过；10 张表和配置类外键存在 |
| 登录 | 正确/错误凭据、用户名跨 IP 锁定；单 IP 限流不触发账号锁；未知用户执行 bcrypt 比对 |
| token | 验签、篡改、15 分钟 access 到期为 401/20102；refresh 不存明文 |
| family | PC/手机不同 sid，PC 刷新/登出不影响手机；同 sid 刷新与撤销并发不可绕过撤销 |
| 宽限 | 注入可控时钟，0/29/30 秒返回 409/20109；超过 30 秒返回 401/20103 并撤 family；重复调用不重置宽限 |
| 立即撤销 | 登出后 access/refresh 立即 20103；不能因 30 秒宽限而放行 |
| Redis | 三套 key 不覆盖，TTL 正确；故障不返回成功；恢复后仍拒绝已持久撤销 sid |
| 菜单 | 两组角色菜单不同，禁用/隐藏/按钮过滤与排序正确，聚合用户信息与 `/auth/me` 一致 |
| CI/契约 | `make lint`（含 depguard）、`make test`；已交付 repo 接口方法与 DryRun 登记集合匹配，认证/seed 专用查询边界明确；认证 OpenAPI 与响应一致，无占位测试 |

按 `AGENTS.md` 的 DoD 附实际输出；通过并合并后才创建 `v0.M1a`。将完成事实记入 `docs/progress.md`，交接 M1b 的身份 ctx、ScopedQuery、permission registry、会话撤销能力和 seed/OpenAPI 入口。

## 6. M1a 验收实现

- `Authenticate` 使用 tenant/user/sid 限定的 `SessionRevoked` 存在性查询；50 条历史记录下仍只执行 1 条 SQL。新增 `000003_refresh_session_revoked` 部分索引，不修改 000001/000002。
- `/auth/me` 与 `/auth/menus` 使用同一安全身份/权限投影；菜单表与角色菜单关联固定各 1 次查询，树在内存组装。
- `make seed` 从 `CH_SEED_ADMIN_PASSWORD` 取初始密码，重复执行保留已有账号哈希；初始化 super_admin、operator、sales 与菜单授权。
- `api/openapi/identity.yaml` 覆盖五个端点，并用实际 HTTP 响应做自动契约校验。
- `make test-integration` 覆盖迁移可逆性、认证/菜单数据库路径及全新 schema 的 make migrate-up → 两次 make seed → API 进程 → 登录 → menus 链路。
- 构建工具：已安装 GNU Make 4.4.1、GCC 与 golangci-lint；不再使用 make 不可用的替代验收。
- M1b 的管理 CRUD、数据权限、会话管理 API、版本化缓存未纳入本次实现。

验收证据（Windows 原生 GNU Make 4.4.1；测试连接只在进程内从本地配置加载）：

```text
$ make lint
golangci-lint run                         # exit 0
$ make test
17 个测试包通过                           # exit 0
$ make test-integration
ok github.com/omwk/go-admin-api/internal/kernel/db 2.721s
ok github.com/omwk/go-admin-api/internal/bootstrap 25.858s
ok github.com/omwk/go-admin-api/cmd/cli 10.353s
```

- 迁移覆盖：`make migrate-up`（全新 schema）、`DB.Migrate(ctx, "down")`（3→2→1→0）及重新 up 到 3 均通过；000003 部分索引存在性和回滚另有集成断言。
- CLI 链路：`make migrate-up` → `make seed` → `make seed` → 启动编译后的 API → 登录 → `/auth/menus`，通过。
- `rg -n 'TODO|not implemented|time\.Sleep|fmt\.Print|log\.Print' internal` 无匹配；审计只保存安全资源标识，HTTP/契约测试确认不返回 password_hash。
- PowerShell `Get-ChildItem internal,cmd -Recurse -Filter *.go` 行数扫描：最大文件 239 行、最大函数 61 行；OpenAPI 257 行。
- 偏差说明：撤销部分索引使用新增 000003；补齐密码比对的短事务和测试查询上下文生命周期，保留 bcrypt cost 12 与 DB 3s 超时，不放宽测试。
