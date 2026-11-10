# M9c 实施契约

依据 ADR-023 §5/10/11 和已批准 M9c 范围落地，不改变既有资金/配额裁决。第 21 号迁移是 PG 准入批量函数；下一号 `000022_billing_statement` 增加账单头、明细和调整凭据。

- 账单唯一 `(tenant,app,CNY,UTC自然月)`；头保存 cutoff、来源摘要、分/单位合计、版本、发行人/时点。明细独立保存 call/原流水、价格/路由版本/权益引用、服务日和入账时点；来源全局去重。列表分页，详情明细使用游标。
- draft 可按已结算现金调用、单位 consume、套餐购买及已执行调整重建；充值/冻结/释放不计消费。权益用量零现金，套餐购买单列。整数累加溢出拒绝。
- issued 头和明细在服务及数据库均不可变。发行必须月份已结束、来源摘要仍一致、服务日对账覆盖完整且不早于相关入账、无 open 差异/收费/调整；缺证据保留 draft。
- 迟到事实若服务月已发行且尚未入单，进入当前生成期的后续调整行，引用原账单。退款/补扣使用原现金 settle 流水；套餐退费规则未批准，不自动退剩余权益。锁原交易控制累计退款上限。
- 补扣必须关联同应用 open 差异；不足可用余额保留 open，独立 apply 入口重试，成功后同事务产生钱包流水、不可变调整凭据、差异处置凭据和审计。退款和处置都要求原因及凭据引用。处置不改原观测，保留验证 run。
- 调整记入执行时的开放月；若原服务月已发行，至少进入其下一月。后续开放月已发行时继续顺延。未执行的 open 不计账单金额。
- 财务数据至少保留 3 年，未决及其关联应用事实受保护。M9c 提供保留候选检查，沿用数据库只追加保护，不自动物理删除；超过 3 年的归档/清理和 RPO 留 M10 裁决。

| 方法 | 路径（前缀 /api/v1/admin） | 权限 | 主要输入 |
|---|---|---|---|
| GET | /billing-statements | billing:statement:list | app_id、page、page_size |
| GET | /billing-statements/{id} | billing:statement:list | after、page_size（明细游标） |
| POST | /billing-statements:generate | billing:statement:generate | app_id、month=YYYY-MM |
| POST | /billing-statements/{id}/issue | billing:statement:issue | version、reason |
| GET | /billing-adjustments | billing:statement:list | app_id、page、page_size |
| POST | /billing-adjustments | billing:wallet:adjust | original_txn_id、kind=refund/supplement、amount_cents、diff_id、reason、evidence |
| POST | /billing-adjustments/{id}/apply | billing:wallet:adjust | 无业务字段 |

全部写入要求 Idempotency-Key，同键异参 409/10006；JWT/RBAC 和 owner scope 同时生效。沿用 10001 参数、10004 终态冲突、10409 版本冲突及现有 90xxx 资金错误，不新增错误码。原差异处置 /reconcile-diffs/{id}/compensations 保留。
