# BE-IDN-034 · 客户端元数据可更新

- ID: BE-IDN-034
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
- 预计: 0.4 人日
- 分支: `feat/be-idn-034-client-update`
- 依赖: BE-IDN-031/033

---

## 0. 事实核对

```
PUT /admin/api-clients/{id} → admin_http.go:212
  return h.lifecycle.SetStatus(ctx, a, id, in.Version, in.Status)
SetStatus (secret.go:143) 只写 status，并用 version 做乐观锁
adminInput 虽有 Name / IPWhitelist 字段，但该分支未使用 → 静默忽略
审计登记仅 gateway.client.create / client.status，无 client.update
```

结论：**名称与 IP 白名单可以提交，但不会生效，且返回 200**。

补充核对（白名单语义）：

```
secret.go:40  创建时校验 netip.ParseAddr(value) → 只接受单个 IP
auth.go       allowedIP() 去掉末尾 /32、/128 后 ParseAddr → 不支持 CIDR
```

即：**当前「带 CIDR 的白名单」即使写进库，也不会生效**（匹配必然失败）。
因此本卡必须同时改「校验」与「匹配」两处，只改其一都是错的。

## 1. 为什么必须修

| 字段 | 影响 |
|---|---|
| `name` | 改名只能删客户端重建 → app_id 与密钥全变，外部调用方全部中断 |
| `ip_whitelist` | **安全控制**：IP 变更后无法收紧，只能继续放行旧 IP 或重建客户端 |

静默忽略比报错更糟：调用方以为改成功了。

## 2. 改动

### 2.1 语义

```
PUT /admin/api-clients/{id}
{
  "name": "订单服务（新）",     // 可选；缺省=不变
  "ip_whitelist": ["10.0.0.0/8"], // 可选；缺省=不变；[] 表示清空
  "status": "active",            // 可选；缺省=不变
  "version": 3                   // 必填，乐观锁
}
→ 200 { id, app_id, name, owner_user_id, status, version, ip_whitelist }
```

- 字段用指针区分「未提供」与「清空」：`*string`、`*[]string`
- `name` 提供时：trim 后非空、≤200
- `ip_whitelist` 提供时：≤100 条，每条为 **IP 或 CIDR**（`10.0.0.0/8`、`::1`、`192.168.1.10`）
  - 存库前归一化：IP 用 `netip.Addr.String()`，CIDR 用 `netip.Prefix.String()`
  - 拒绝 `0.0.0.0/0` 与 `::/0`（等于关闭白名单）
  - **创建接口同步改为同一规则**（不能创建只收单 IP、更新收 CIDR）
  - `allowedIP` 改为：单 IP 用 `Addr.Compare`，CIDR 用 `Prefix.Contains(ip)`；两者混合
- `status` 提供时：`active` / `suspended`
- `version` 必须等于当前版本，否则 409「客户端已被修改，请刷新」
- 三者都未提供 → 400「没有需要更新的字段」

### 2.2 审计

新增 action `gateway.client.update`（**先登记到 `docs/design/audit-events.md`**）：

```json
{ "before": {"name":"旧名","status":"active","ip_whitelist":["1.1.1.1"]},
  "after":  {"name":"新名","status":"active","ip_whitelist":["10.0.0.0/8"]} }
```

只记录实际变化的字段；状态变更仍可沿用 `gateway.client.status` 或统一为 `update`（本卡统一为 `update`，`status` 保留但不再产生新记录）。

### 2.3 存储

`ManagementStore` 增加：

```go
UpdateClient(context.Context, Actor, int64, ClientUpdate, int) (ClientInfo, error)
```

- 单条 UPDATE，带 `tenant_id`、`owner_user_id`、`version` 条件
- 影响行数 0 → 区分「不存在/无权」404 与「版本冲突」409（先查再判）

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 改名 | PUT name → 200，GET 显示新名，app_id 与密钥不变 |
| 2 | 改 IP | PUT ip_whitelist（含 CIDR）→ 200，回显归一化后的列表 |
| 3 | 清空 IP | 传 `[]` → 200，回显为空数组 |
| 4 | 仅状态 | 只传 status + version → 200（兼容旧前端） |
| 5 | 未提供字段不变 | 只改 name，status/ip 保持原值 |
| 6 | 空请求 | 三个字段都不传 → 400 |
| 7 | 版本冲突 | 旧 version → 409 |
| 8 | 非法输入 | 空 name / 非法 IP / 非法 CIDR / 101 条 / `0.0.0.0/0` → 400 |
| 8.1 | 创建同规则 | 创建时传 CIDR 成功；非法 CIDR → 400 |
| 8.2 | CIDR 生效 | 白名单 `10.0.0.0/8` 时，`10.1.2.3` 通过鉴权、`11.0.0.1` 被拒 |
| 9 | 审计 | 一条 `gateway.client.update`，before/after 仅含变化字段 |
| 10 | 越权 | 跨租户、非 owner → 404 |
| 11 | 契约 | OpenAPI 更新（含指针语义说明），双向一致 |
| 12 | 全量 | `verify` 通过 |

## 4. 不做

- 修改 `app_id`（外部引用，永不变）
- 转移 owner

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-034-client-update.md。

分支：feat/be-idn-034-client-update（从最新 main cf589bf 开）

必读：本卡、internal/modules/gateway/admin_http.go（PUT 分支 212 行）、
secret.go（SetStatus 与 Create 的 IP 校验）、admin_types.go（ManagementStore）、
docs/design/audit-events.md

改动：
1. adminInput 的 name/ip_whitelist 改为指针，区分未提供与清空
2. ManagementStore 增加 UpdateClient（租户+owner+version 条件）
3. PUT /api-clients/{id} 支持 name / ip_whitelist / status 的组合更新
4. 新增审计 action gateway.client.update，先登记到 audit-events.md
5. OpenAPI 更新

约束：
- version 必填；冲突返回 409
- 三个字段都未提供 → 400
- 不改变 app_id 与密钥
- 兼容旧的「只传 status+version」调用

验收按卡片 §3 十二条逐条贴证据：
- 第 1 条贴改名前后 GET，证明 app_id 不变
- 第 7 条贴 409 响应
完成后停下报告，不要做前端。
```
