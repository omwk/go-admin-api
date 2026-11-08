# M6c 对外暴露面核对

本清单使用仓库内可运行的 `cmd/share-proxy`，实际读取 `deploy/share-public.json`。允许清单只有四个方法/路径：根 GET、verify POST、inquiry POST、成员 GET；其他路径、方法、查询串、编码路径在连接上游之前返回 404。反代不输出访问日志、原始 URI、Referer 或上游错误。管理入口独立发布。

## 1. 应用和可信代理

- API 只绑定私网或本机地址：`CH_APP_HTTP_ADDR=127.0.0.1:8080`；防火墙只允许管理入口和分享反代访问，禁止公网绕过反代访问 API 端口。
- `CH_APP_TRUSTED_PROXIES` 默认空，忽略客户端转发头。API 在反代同机时只配置 `127.0.0.1,::1`；跨机时只配置真实反代地址，禁止 `0.0.0.0/0` / `::/0`。
- 分享反代默认忽略转发头。如前置 TLS 终结器，`-trusted-proxies` 只填它的源地址；终结器必须追加实际客户端地址，禁止把客户端提供的 X-Forwarded-For 原样当可信来源。反代从右向左取最后一个不可信地址，重新生成单一 X-Forwarded-For，删除 X-Real-IP。
- TLS 终结器只将分享域转发到本代理，保留 Host；同源 HTTPS 页面提交 Origin 必须等于 Host。禁止缓存、请求/响应体捕获、原始 URI/Referer 访问日志；CDN/WAF/APM 也必须采用相同脱敏规则。错误页不得包含请求 URI、分享秘密或上游错误。
- 应用层四条系统 GET 全部使用实时 JWT；认证装配缺失时 403，匿名/撤销会话 401，禁止凭路径名假设 system/info 已鉴权。监控与探针改用受控管理认证入口。

```powershell
$env:CH_APP_HTTP_ADDR = '127.0.0.1:8080'
$env:CH_APP_TRUSTED_PROXIES = '127.0.0.1,::1'
go run ./cmd/api
# 另一个终端；如无前置代理，省略 -trusted-proxies。
go run ./cmd/share-proxy -config deploy/share-public.json -listen 127.0.0.1:8081 -upstream http://127.0.0.1:8080 -trusted-proxies 127.0.0.1,::1
```

## 2. 四条路径逐项验证

本地验收实际执行：

```powershell
go run ./cmd/share-proxy -config deploy/share-public.json -listen 127.0.0.1:18081 -upstream http://127.0.0.1:18080
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-share-exposure.ps1 -BaseUrl http://127.0.0.1:18081
```

```text
PUBLIC GET /healthz -> 404
PUBLIC GET /readyz -> 404
PUBLIC GET /metrics -> 404
PUBLIC GET /api/v1/admin/system/info -> 404
PASS: all four system routes reject anonymous access.
```

| 路径 | 单条验证命令 | 公开配置期望 |
|---|---|---|
| `/healthz` | `curl.exe --silent --output NUL --write-out "%{http_code}" http://127.0.0.1:18081/healthz` | 404/403 |
| `/readyz` | `curl.exe --silent --output NUL --write-out "%{http_code}" http://127.0.0.1:18081/readyz` | 404/403 |
| `/metrics` | `curl.exe --silent --output NUL --write-out "%{http_code}" http://127.0.0.1:18081/metrics` | 404/403 |
| `/api/v1/admin/system/info` | `curl.exe --silent --output NUL --write-out "%{http_code}" http://127.0.0.1:18081/api/v1/admin/system/info` | 404/403 |

部署后必须在实际外部网络上将脚本 `-BaseUrl` 替换为真实 HTTPS 分享域再次运行；`-ApplicationBaseUrl` 可同时核对私网 API。脚本禁止自动跟随重定向，以免把登录页 200 误当通过。本地结果证明仓库公网配置，不能代替真实 DNS/TLS/防火墙/CDN 配置验收。

## 3. 可重复回归命令

```powershell
go test ./cmd/share-proxy -run '^TestPublic' -count=1 -v
go test ./internal/kernel/httpx -run 'TestSystem|TestPublic|TestTrusted' -count=1 -v
go test ./internal/bootstrap -run '^TestShareExposure.*Integration' -count=1 -p 1 -v
go test ./internal/bootstrap -run '^TestShareInquiryIntegrationSubmissionRejectsForwardedHeaderSpoof$' -count=1 -p 1 -v
```

反代回归会拒绝四条路径及管理认证路径的 GET/POST/HEAD/OPTIONS、查询串、尾斜杠和编码变体，并断言上游零命中；四个允许的分享操作能够实际转发，验证不只覆盖全拒绝配置。应用回归逐条验证匿名 401、有效 JWT 200、注销后同 JWT 401；来源回归验证伪造转发头不能绕过第 4 次提交的 429。

## 4. 生命周期、日志与留存运维

- 客户停用/删除/移交、报价单禁用/删除，相关链接及引用该价目表的冻结来源链接全部永久撤销；重新启用父对象不会复活旧链接。主库门禁立即拒绝已有 Cookie 与热缓存，审计失败则父对象变更和撤销一起回滚。
- Worker 领导者启动及每日 UTC 日切执行清理；失败不写完成水位，下一次调度重试。`share_privacy_run` 保存每日完成和清理数量；每批安全审计仅含类别、数量、截止时间。
- 未转化询价自提交 180 天、转化后原始询价 PII 30 天、客户终止后联系 PII 180 天覆盖擦除；访问 90 天删除，幂等回执 24 小时删除。软删不替代擦除。
- 安全审计满三年后，仅明确列出的分享安全事件/资源组合允许按批删除，数据库 UTC 时钟为准。未到期、其他域或未知事件继续不可删除，全部 UPDATE/TRUNCATE 继续禁止；删除与清理审计同事务，失败重试。迁移回滚恢复原有全表只增约束，不恢复已依法清除的数据。
- 部署方需配置备份轮换和恢复后的到期清理；数据库擦除不声明即时清除离线备份。公开媒体、在途已准入响应及已下载内容不能被链接撤销收回。
- SQL 日志生产默认关闭；应用日志仅用路由模板。站内通知和 outbox 只含询价引用与固定提示，不向电话、邮箱或外部消息通道发送 PII。合成 canary 回归覆盖数据、错误、列表、CSV、通知、outbox、幂等回执与应用日志。
