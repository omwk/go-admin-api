# 密钥与配额操作手册

管理操作要求当前 owner 的 JWT 和对应 `gateway:client:*` 权限；open HMAC 不能代替管理会话。以下路径相对于服务 origin，`id/sid/route_id` 为管理端内部标识，只给运维使用。

## 常规轮换

1. GET `/api/v1/admin/api-clients/{id}/secrets`，核对最多两把有效 key、旧 key 的 `expires_at/last_used_at`。列表只给元数据，不含 secret。保留旧 key 的 sid，不能靠“最后一项”猜旧 key。
2. POST 同一路径，JSON `{"overlap_seconds":86400}`；默认 86400s，允许 1–604800s。两把有效 key 已存在时返回 409，先确认哪把可安全吊销。轮换只能缩短旧 key 期限，不延长既有期限。
3. 新 secret 只在首次 201 返回一次，经秘密管理通道交付并更新调用方；不要打印整个响应、复制到日志或提交仓库。若交付丢失，幂等重放仅返回 delivered 回执，不能恢复明文；吊销未交付 key 后重新签发。
4. 在期限内用不同 nonce 分别验证两把 key 能查询获授权订单，并通过 usage/持久日志确认成功计量；再将所有调用方切到新 key。
5. 检查旧 key `last_used_at` 不再增长，必要时主动 DELETE `/api/v1/admin/api-clients/{id}/secrets/{sid}` 提前结束窗口。否则窗口到期后即拒绝，不依赖清理任务。
6. 用旧 key 的新签名确认 401/80102，再用新 key 确认 200。轮换不更换 app_id、不重置 nonce、QPS 或配额。

PowerShell 管理调用形态（凭证由进程环境/秘密管理器提供，响应只留内存）：

```powershell
$headers = @{ Authorization = 'Bearer ' + $env:CH_ADMIN_TOKEN; 'Idempotency-Key' = [Guid]::NewGuid().ToString('N') }
$issued = Invoke-RestMethod -Method Post -Uri ($env:CH_OPEN_BASE_URL + '/api/v1/admin/api-clients/' + $clientID + '/secrets') -Headers $headers -ContentType 'application/json' -Body '{"overlap_seconds":86400}'
# Transfer $issued.data.secret directly to the approved secret manager. Do not print it.
```

## 泄露应急

1. 确认泄露 key 的 sid；有健康替代 key 时立即吊销泄露 key。不能等待常规轮换窗口。
2. 若无法辨别泄露范围，PUT `/api/v1/admin/api-clients/{id}`，JSON `{"status":"suspended","version":当前版本}`；这是乐观锁操作，409 时重读应用版本，不盲目覆盖。暂停后拒绝所有 key，再逐把吊销和替换。
3. DELETE key 成功提交后，立即用该旧 key **发送第一次新的签名请求**，必须返回 401；不能等缓存 TTL，也不能用重放 nonce 的 80103 冒充吊销验证。主库或 Redis 故障应返回 503 拒绝，不能恢复旧权限放行。
4. 检查受影响时段的 `api_call/api_call_log/api_call_meter` 安全引用、状态、计量和 quota；只导出调用/应用/路由 ID、时间和结果码，不导出 secret、密文、签名或客户原文。
5. 健康新 key 与必要的应用状态恢复后，验证 200 和计量，再持续观察异常调用。新密钥不撤回已经下载的内容；已准入的在途请求与已返回结果有无法回收边界。

## 配额耗尽

POST `/api/v1/admin/api-clients/{id}/routes`，JSON 包含 `route_id/status=active/quota_daily/quota_monthly`；0 表示对应周期不限。额度针对应用累计，不是每条授权各自独立计数。先评估容量与授权，再调整，不以发新 key 绕过限额。

收到 429 时先区分 `limit_type`；日/月配额使用[错误表](errors.md)的 UTC 边界和 Retry-After，usage 仍可查询。确认被拒请求没有新增成功 meter，也没有推进 `api_quota.admitted_count`。不要删 quota 行“修复额度”。准入后失败仍占配额是既定语义。

## 三个可执行演练

依赖真实 PostgreSQL、Redis、Go、Windows PowerShell 5.1、Git for Windows 的 bash/OpenSSL/curl。DatabaseUrl 从 `TEST_DATABASE_URL` 或根 `.env` 读取；秘密只在内存/管道传递。演练创建随机 schema，独占空 Redis 逻辑 DB（排除开发 DB），运行真实 API/worker 后清理；不会操作已有开发应用。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/gateway/rotation.ps1 -RedisAddr 127.0.0.1:16379
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/gateway/revocation.ps1 -RedisAddr 127.0.0.1:16379
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/gateway/quota.ps1 -RedisAddr 127.0.0.1:16379
```

每个脚本输出 PASS/FAIL、HTTP/code 和脱敏的行为摘要，退出 0 为成功，非零为失败，失败仍清理。轮换用 5 秒真实窗口；泄露演练不轮询重试旧 key；配额演练分别验证日/月 N=2 与 N+1，输出 limit_type/UTC reset_at/Retry-After，并核对拒绝不消费配额、不计量。它们都会执行 S3 的完整 15 步及两个签名示例。

独立压测使用 `scripts/gateway/benchmark.ps1`，结果解释、显式开关与容量边界见[性能报告](../milestones/M8c-performance.md)。它不会改产品 QPS 默认值。
