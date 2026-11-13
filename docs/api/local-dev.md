# 本地开发环境

前端联调、冒烟、手工验证都在本地跑，本文记录**这台机器上实际可用的配置**。

---

## 1. 端口

| 服务 | 端口 | 说明 |
|---|---|---|
| PostgreSQL | `127.0.0.1:5432` | 容器 `deploy-postgres-1` |
| Redis | `127.0.0.1:16379` | 容器 `deploy-redis-1`（**不是 6379**） |
| API | `127.0.0.1:18080` | **不是 8080** |

> 8080 落在 Windows 保留端口段 `7998–8097` 内，绑定会失败并只报
> 「HTTP listener failed」。用 `netsh interface ipv4 show excludedportrange protocol=tcp`
> 可以确认。**前后端代理一律指向 18080。**

## 2. 启动顺序

```powershell
# 1. 起依赖
docker start deploy-postgres-1 deploy-redis-1

# 2. 迁移 + 种子
cd D:\code\go-admin-api
$env:CH_DB_DSN   = (从 .env 的 TEST_DATABASE_URL 取)
$env:CH_REDIS_ADDR = '127.0.0.1:16379'
$env:CH_JWT_SECRET = '<任意 32 字节以上>'      # 本机固定值即可
$env:CH_CRYPTO_MASTER_KEY = (从 .env 取)
$env:CH_SEED_ADMIN_PASSWORD = 'Admin@12345'
$env:CH_APP_HTTP_ADDR = ':18080'
go run ./cmd/cli migrate up
go run ./cmd/cli seed
go run ./cmd/api
```

## 3. 开发账号

| 账号 | 密码 | 权限 |
|---|---|---|
| `admin` | `Admin@12345`（由 `CH_SEED_ADMIN_PASSWORD` 决定） | 超管，全部权限 |
| `sales01` / `sales02` | `Test@12345` | 无角色（用于验证无权限表现） |

> 以上仅本地开发使用，**不得写入生产或提交到仓库**。

## 4. 验证后端可用

```powershell
$body = @{ username='admin'; password='Admin@12345' } | ConvertTo-Json
$r = Invoke-RestMethod -Uri 'http://127.0.0.1:18080/api/v1/auth/login' -Method POST `
       -Body $body -ContentType 'application/json'
$r.code            # 0
$r.data.expires_in # 900
```

## 5. 前端联调

F1 邀请/重置链接可用 `CH_APP_PUBLIC_URL` 指向前端公开基址。找回密码统一返回 202 和 `data.accepted=true`；`CH_APP_ENV=dev|test` 额外提供可复制的 `data.dev_reset_url`，不存在账号时为无效链接。生产环境仅通过邮件通道发送，响应不携带凭证；无通道时管理员可凭 `system:user:reset` 调用 `POST /api/v1/admin/users/{id}/reset-link` 获取一次性链接，操作记审计。链接包含租户参数，令牌放在 URL fragment，前端读取后通过 JSON 调用接受邀请或重置接口。管理员建号/重置后的首次登录需要先改密。

前端仓库 `D:\code\admin-web`：

```
# apps/admin/.env.development
VITE_NITRO_MOCK=false
VITE_GLOB_API_URL=/api

# apps/admin/vite.config.ts
server: { proxy: { '/api': { target: 'http://127.0.0.1:18080', changeOrigin: true } } }
```

## 6. 已知差异

| 项 | 现状 |
|---|---|
| `GET /admin/users` 的 `keyword` / `status` | **尚未实现**，传了也被忽略；见 `docs/tasks/BE-1-user-filters.md` |
| `/healthz`、`/readyz` | 本地绑定 18080 后可用；若返回失败先确认端口 |

## 6. 商品演示数据

前端 FE-2 需要商品数据，用脚本生成（仅开发库）：

``powershell
Get-Content scripts/dev-seed-catalog.sql -Raw |
  docker exec -i deploy-postgres-1 psql -U commercehub_test -d commercehub_test -v ON_ERROR_STOP=1
``

结果：15 个类目、120 个 SPU（含 draft/active）、240 个 SKU，价格区间与 `sku_count` 已回填。

### 商品详情响应结构

``json
{
  "data": {
    "reported_stock": 0,
    "product": { "id": 7, "title": "...", "price_min_cents": 1570, ... },
    "skus": [ { "id": 71, "code": "...", "price_cents": 1570, ... } ],
    "listings": [],
    "media": []
  }
}
``

注意：详情不是平铺结构，商品本体在 `data.product` 下，SKU 在 `data.skus`。

## 7. 部门演示数据

前端多部门场景验证需要部门数据：

``powershell
Get-Content scripts/dev-seed-depts.sql -Raw |
  docker exec -i deploy-postgres-1 psql -U commercehub_test -d commercehub_test -v ON_ERROR_STOP=1
``

结果：根部门下 3 个一级部门（电商事业部/客服中心/财务部）+ 4 个二级部门。

## 8. 类目后代筛选

``
GET /admin/products?category_id=1                        → 精确匹配
GET /admin/products?category_id=1&include_descendants=true → 含所有后代
``

实测：类目 1 精确 11 条，含后代 27 条。listings 与导出同样支持。
## 9. 升级后更新权限与菜单

正常升级只需重跑种子，管理员原密码保持不变：

```powershell
go run ./cmd/cli seed
```

seed 事务提交后，会自动对当前默认租户（`tenant_id=1`）的所有用户执行授权版本号 `INCR`。
`/api/v1/auth/menus` 和 `/api/v1/auth/me` 的下一次请求立即读取新菜单与权限，无需清缓存或重新登录。
其他租户的版本号不变；旧版本负载保留并按原有 TTL 过期，版本号键不设 TTL。
CLI 的 `CH_REDIS_ADDR`、`CH_REDIS_DB`、`CH_REDIS_PASSWORD` 必须与 API 使用同一 Redis 配置。

Redis 不可用或部分版本号递增失败时，已提交的 seed 仍成功，输出包含
`warning: seed committed but authorization cache invalidation failed` 和 `seed completed`。
先恢复 Redis 并重跑 seed；种子事务失败则不执行缓存失效。

仅当缓存失效失败、需要应急恢复时，才手动清理授权缓存。本机 Redis 容器的默认 DB 可执行：

```powershell
docker exec deploy-redis-1 sh -c "redis-cli --scan --pattern 'authz:*' | xargs redis-cli del"
```

该应急命令会清理所选 Redis DB 内所有租户的授权缓存。共享环境应分别扫描
`authz:ver:<tenant>:*`、`authz:menu:<tenant>:*`、`authz:perm:<tenant>:*`，
例如当前默认租户的版本键模式为 `authz:ver:1:*`，并选择与 API 相同的 Redis DB。
清理后再次请求 `/api/v1/auth/menus` 即会重建缓存。
