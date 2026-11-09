# M8 开放 API 接入

当前发布以 ADR-022 与 `internal/modules/gateway/openapi.yaml` 为准。历史总规格中的 `/api/v1/open/**`、动态代理及计费规划不是当前可调用接口。

| 方法与路径 | 返回与边界 |
|---|---|
| GET `/open/v1/orders/{public_id}/status` | `id/order_no/status/updated_at`；必须有路由与逐订单授权，且 owner 当前权限/店铺范围仍有效 |
| GET `/open/v1/usage?date=YYYY-MM-DD` | 本应用 UTC 日用量，省略日期取当天；不占业务配额、不产生新的计量事件，仍认证并经过全局/应用桶 |
| GET `/open/v1/balance` | M8 固定 503/10002；不返回伪造余额 |

`public_id` 是逐应用订单授权产生的公开 ULID，不是内部订单 ID。订单状态读已同步规范数据；`updated_at` 是规范记录更新时间，不承诺平台实时状态。公开响应不会携带内部订单详情、PII、金额、平台/店铺身份。管理 API 继续使用人类 JWT/RBAC。

- [签名、编码与可执行示例](signing.md)
- [错误码与重试](errors.md)
- [轮换、泄露处置与配额演练](operations.md)
- [M8c 验收](../milestones/M8c-acceptance.md)
- [性能测量与瓶颈](../milestones/M8c-performance.md)

首次接入：管理员创建本人应用，安全交付一次性 secret，登记 `order.status` 路由，授予路由与所需订单，再将公开 ID 交付调用方。IP 白名单是精确地址匹配。服务器之间使用 HTTPS；长期 secret 留在服务端。

调用成功同时返回 `X-Call-Id`（服务端逻辑调用身份）和 `X-Request-Id`（服务端追踪身份）。可自带受限字符、最长 64 位的 `X-Request-Id`，它只作为响应 `request_id` 和内部观测字段，不是计量或去重身份。

M8 **只计量，不扣费**。业务成功且结果与 meter 已持久化后计 1 次；失败/超时不计。日/月配额统计准入次数，已准入后业务失败仍占配额。所有 key、所有路由共享应用日/月计数；轮换不清零。

成功持久化后丢失响应仍有计量。现行只读 v1 未提供客户端幂等结果重取协议；同一个 GET 或 `X-Request-Id` 重发会生成新 `call_id`，再次成功会再计一次。不能把观察 ID 或未纳入签名的 `Idempotency-Key` 当成公开去重承诺。
