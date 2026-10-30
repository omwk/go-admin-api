# CommerceHub · 多平台电商中后台

一套面向「多平台 / 多店铺」的电商中后台系统。Go 单体模块化实现，供 Codex 分里程碑落地。

> 本仓库当前阶段是**规格仓库**：先有可执行的规格，再有代码。所有代码必须能追溯到 `docs/01-PRD.md` 中的需求 ID。

---

## 1. 文档地图

| 文件 | 作用 | 谁读 |
|---|---|---|
| `docs/01-PRD.md` | 需求清单（REQ-ID）、业务规则、验收标准、明确不做的范围 | 人 + Codex |
| `docs/02-architecture.md` | 分层、模块边界、目录结构、技术选型、部署 | 人 + Codex |
| `docs/03-data-model.md` | 全部数据表、字段、索引、唯一键、分区策略 | 人 + Codex |
| `docs/04-conventions.md` | 命名、API 协议、错误码、金额/时间、幂等、测试、Git | Codex 必须遵守 |
| `docs/05-platform-sync.md` | 连接器接口、映射 DSL、漂移检测、重放、限流 | Codex 实现 M3/M5 时读 |
| `docs/06-share-quote.md` | 分享链接、报价单、快照、访问控制 | Codex 实现 M6 时读 |
| `docs/07-gateway-billing.md` | 开放网关鉴权/限流/计量/计费/对账 | Codex 实现 M8/M9 时读 |
| `docs/ADR.md` | 已定架构决策（改动需新增 ADR） | 人 + Codex |
| `docs/PROMPTS.md` | 评审 / 实现 / 验收 / 修复 四套提示词模板 | 人 |
| `docs/milestones/M0.md … M10.md` | 每个里程碑的任务卡：范围、表、接口、验收、可粘贴 prompt | 人 + Codex |
| `AGENTS.md` | Codex 全局行为契约（禁止项、完成定义、Gate 流程） | Codex 每次会话必读 |

---

## 2. 怎么和 Codex 配合

一个里程碑的标准循环（**不要跳过 Gate A**）：

```
① Gate A 设计评审     → Codex 只读文档，输出 docs/proposals/Mx-review.md（禁止改代码）
② 人裁决 + 写 ADR      → 有分歧的结论落到 docs/ADR.md
③ 实现                 → 按 docs/milestones/Mx.md 的任务卡，一张卡一个 PR
④ Gate B 自检          → make lint test；迁移可重入；OpenAPI/文档同步
⑤ Gate C 验收          → 对照 Mx.md 的验收清单逐条跑命令，人工抽查 20%
```

**给 Codex 的开场指令**（每张任务卡都长这样）：

> 只实现 `docs/milestones/M3.md` 中 `任务 3.2`。先读 `AGENTS.md`、`docs/04-conventions.md`、
> `docs/03-data-model.md` 中 `raw_payload` 与 `mapping_version` 两节，以及本任务卡。
> 不要修改任务卡范围外的文件。完成后按 `AGENTS.md` 的完成定义自检，
> 并输出「偏差说明」一节：列出任何与规格不一致的地方。

---

## 3. 里程碑一览

| 里程碑 | 主题 | MVP |
|---|---|---|
| M0 | 工程底座（配置/日志/错误码/迁移/CI/健康检查） | ✅ |
| M1a | 认证、会话、最小种子与菜单聚合（入口 `docs/milestones/M1a.md`） | ✅ |
| M1b | 管理 CRUD、RBAC、数据权限与缓存失效（入口 `docs/milestones/M1b.md`） | ✅ |
| M2 | 店铺与凭据（加密、脱敏、审计） | ✅ |
| M3 | 连接器 + 同步框架（单平台纵向打通） | ✅ |
| M4 | 商品域（SPU/SKU/Listing/类目/库存/重放） | ✅ |
| M5 | 订单域（增量同步、状态机、查询导出） | ✅ |
| M6 | 分享与报价（快照、链接、询价） | ✅ |
| M7 | 凭据自动刷新（调度、退避、历史） | 商业化 |
| M8 | 开放网关（签名、限流、调用日志） | 商业化 |
| M9 | 计费（钱包、套餐、预扣、对账） | 商业化 |
| M10 | 加固交付（压测、安全、可观测、部署） | 交付 |

**MVP = M0–M6（M1 分 M1a/M1b）。** 每完成一个里程碑打一个 tag：`v0.Mx`；M1a 独立验收合并后先打 `v0.M1a`，再进入 M1b，完成后打 `v0.M1b`。

---

## 4. 已锁定的假设（来自需求澄清，按推荐方案）

| 决策 | 结论 |
|---|---|
| 租户模式 | 单租户运行，但**全表预留 `tenant_id`**，中间件强制注入 |
| 首批平台 | 淘宝/天猫（OAuth 会话）、Shopee（OAuth，短期 token）、Shopify（静态 token）三种鉴权形态各一个 |
| 数据规模假设 | 单店铺日订单 ≤ 5 万；同步延迟 ≤ 30 分钟；预留 2 倍余量设计 |
| 字段漂移策略 | 混合：必填缺失→隔离；可选缺失→留空+告警；未知字段→进 `ext`+提示 |
| 凭据形态 | 支持 token / key / cookie；浏览器自动化只留扩展点，MVP 不实现 |
| 凭据历史 | 密文保留 90 天，元数据永久 |
| 分享形态 | 双模式：默认快照，可选实时库存；价格一律取报价单 |
| 客户身份 | 匿名签名链接 + 可选访问密码 + 访问日志 |
| 成交闭环 | MVP 只到「询价留言」，不做下单支付 |
| 网关开放度 | 白名单 + 后台人工开通，不做自助注册 |
| 计费模式 | 按次 + 套餐包；支付后置，先人工充值 + 兑换码 |
| 技术栈 | Go 1.23 / Gin / GORM / PostgreSQL 16 / Redis 7 / asynq |
| 前端 | 本仓库只做后端，菜单树与 OpenAPI 对齐现成 admin 模板 |
| 评审机制 | Gate 提案制：里程碑前一次设计评审，实现中按需定向评审 |
| 权限模型 | 固定 `module:resource:action` 且首段等于 module；菜单/权限分表；显式 Scope + depguard/DryRun（ADR-011） |
| 外键策略 | 配置类小表及其关联表建物理外键，业务大表不建，不追改已合并迁移（ADR-011） |
| 会话与缓存 | 每次登录独立 sid；轮换 30 秒宽限 20109；三套 Redis 撤销 key；版本键 + 同步 INCR，outbox 重试 M3 接入（ADR-012/011） |
| 登录保护 | 按用户名锁定，IP 只限流；bcrypt cost 12 保留至 M10 压测裁定（ADR-013） |

> 任何一条要改，先改本表 → 在 `docs/ADR.md` 追加一条决策 → 同步 PRD 对应条目。**代码不能先于文档改。**

---

## 5. 本机快速开始（M0 完成后可用）

```bash
cp configs/config.example.yaml configs/config.local.yaml   # 填数据库/Redis
docker compose -f deploy/docker-compose.yml up -d          # pg + redis + minio
make migrate-up
make run-api        # :8080
make run-worker     # 消费同步/刷新任务
curl -s localhost:8080/healthz
```

### 本地测试库与前置工具

- 需要 Go 1.23、GNU Make、golangci-lint 1.64.8；Windows 运行 `go test -race` 还需要 GCC，并启用 `CGO_ENABLED=1`。
- Windows 可安装 WinLibs（含 GCC 和 GNU Make），将工具目录加入 PATH；若仅提供 `mingw32-make.exe`，在同目录复制为 `make.exe`。
- `TEST_DATABASE_URL` 使用独立测试库账号，该账号需要测试库的 CONNECT、CREATE 权限；集成测试创建独立 schema，结束后清理。
- `.env.example` 仅为模板，真实连接填入被 Git 忽略的 `.env`，不得提交密码。应用和 Make 不会自动加载 `.env`，需先导出到当前终端。

Git Bash：

```bash
cp .env.example .env  # 编辑 .env，填入本地连接；已有 .env 时保留原文件
set -a
source .env
set +a
make lint
make test
make test-integration
CGO_ENABLED=1 go test -race ./...
```

PowerShell 可在同一终端设置 `$env:TEST_DATABASE_URL` 后运行上述 make 目标。`make test-integration` 覆盖迁移回滚、认证/菜单及全新 schema 的 seed/API 链路；种子初始密码从 `CH_SEED_ADMIN_PASSWORD` 读取。

---

## 6. 约定速查

- 分支：`feat/M3-sync-framework`、`fix/...`、`docs/...`
- 提交：`feat(sync): 支持游标分页 [REQ-SYNC-007]`（**必须带需求 ID**）
- 一个 PR 只做一张任务卡；跨卡改动拆 PR
- 迁移文件只增不改；已合并的迁移永不编辑
