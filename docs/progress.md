## M0 · 工程底座
- 完成日期：2026-10-31
- Tag：v0.M0
- 状态：完成
- 验收：§9 的 10 条全部通过（第 7 条由 engine_test 覆盖，真实停机演练移至 M10）
- 偏差：MinIO 改用二进制构建（镜像拉取受限）；Redis 本地映射 16379，仓库默认 6379 不变
- 下一个里程碑：M1a，入口 docs/milestones/M1a.md；M1.md 为共享规格
- 新会话必读：AGENTS.md → docs/04-conventions.md → docs/milestones/M1a.md → 共享卡列出的必读文件

## M1 · Gate A 裁决与拆分
- 裁决日期：2026-10-31
- 状态：文档已裁决，M1a/M1b 均待实现；未创建发布标签
- 决策：ADR-011（权限/外键/数据范围/缓存）、ADR-012（family/宽限/失效）、ADR-013（登录保护/分批）
- M1a：认证、会话、最小种子、菜单聚合、认证契约；独立验收合并后打 v0.M1a
- M1b：管理 CRUD、RBAC、数据范围、会话管理、版本键 + 同步 INCR；完成后打 v0.M1b
- 延后：outbox 失效重试交 M3 T3.11；bcrypt cost 12 的压测裁决交 M10 T10.1
