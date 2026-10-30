# 06 · 分享与报价规格

- 关联：`docs/03-data-model.md` §8；`docs/ADR.md` ADR-006
- 适用里程碑：M6

---

## 1. 要解决的核心问题

同一个商品，对不同客户报不同的价；分享出去的页面，客户今天看和下周看必须**一致**（除非运营主动改）。上游改价、下架、改字段，都不能穿透到已经发出去的链接。

**结论：价格与内容双冻结。**

```
价格 ← price_list_item（报价单，客户维度）
内容 ← share_snapshot（快照，链接维度）
```

---

## 2. 数据流

```
① 建客户 customer
② 建报价单 price_list + price_list_item（协议价、MOQ）
③ 选商品（SPU/SKU 集合）→ 生成 share_link
      ├─ mode=snapshot：立即解析价格 + 冻结商品数据 → share_snapshot
      └─ mode=live：不生成快照，访问时实时读
④ 把 https://<host>/s/<token> 发给客户
⑤ 客户打开 → 校验 → 返回快照/实时数据 → 记 share_visit
⑥ 客户留资 → inquiry → 通知归属销售
```

---

## 3. 链接与鉴权

### 3.1 token 生成

```go
raw := cryptorand(32)                  // 32 字节
token := base64.RawURLEncoding.EncodeToString(raw)  // 43 字符
hash  := sha256.Sum256([]byte(token))
// 存 hash，展示 hash 前 8 位（token_prefix）
```

- 生成时**只返回一次**明文 token，之后无法再取回（只能重新生成）。
- 链接格式：`https://<host>/s/<token>`，token 在 path 中，不放 query（避免被日志记录）。

### 3.2 访问校验顺序

1. `token_hash` 查不到 → `70101`（统一返回「链接无效」，不区分不存在/已撤销，防枚举）。
2. `status != active` → `70102`。
3. `expires_at < now` → `70102`，同时把 `status` 置 `expired`。
4. `max_views > 0 && view_count >= max_views` → `70102`。
5. 有 `password_hash` 且未通过密码校验 → `70103`（前端先弹密码框）。
6. 通过 → `view_count += 1`（原子 SQL），写 `share_visit`，返回内容。

> 第 6 步的计数必须是 `update ... set view_count = view_count + 1 where id=? and (max_views=0 or view_count < max_views) returning view_count`，用返回行数判断是否超限，避免并发穿透。

### 3.3 密码校验

- 密码用 bcrypt（cost 10），存 `password_hash`。
- 校验通过后，签发一个短期（2 小时）的 HttpOnly Cookie：`share_access=<link_id>.<hmac>`，避免每次请求都带密码。
- 密码错误 5 次锁定该链接 15 分钟（Redis 计数）。

---

## 4. 价格解析

### 4.1 优先级（BR-007）

```
1. price_list_item 中 (spu_id, sku_id) 精确匹配
2. price_list_item 中 (spu_id, sku_id=0) 的 SPU 级报价
3. 商品原价 product_sku.price_cents（仅当租户配置 allow_fallback_price=true）
4. 无价格 → 展示「请联系客服」（show_price=false）
```

租户配置在 `tenant.settings`：

```json
{
  "share": {
    "allow_fallback_price": false,
    "show_stock": "range",        // none / range / exact
    "show_cost": false,
    "expired_price_list": "reject" // reject / use_last / fallback
  }
}
```

### 4.2 报价单有效期

- `price_list.valid_to` 过期后：
  - `reject`（默认）：该链接返回 `70202`，提示「报价已过期，请联系销售」。
  - `use_last`：继续用最后价格，但页面显示「报价已过期」角标。
- 报价单修改价格会 bump `version`；**已生成的快照不受影响**（这是设计意图）。

### 4.3 快照里的价格

快照 payload 中直接存**解析后的最终价**（`display_price_cents`），访问时不再重新计算。这样即使报价单后来被改，旧链接仍是旧价。

---

## 5. 快照 payload 结构

```json
{
  "schema_version": 1,
  "generated_at": "2026-10-30T07:00:00Z",
  "link": {
    "title": "2026 秋季新品报价",
    "customer_name": "杭州某某贸易",
    "valid_until": "2026-11-30T00:00:00Z",
    "currency": "CNY",
    "note": "价格含税，不含运费"
  },
  "items": [
    {
      "spu_id": 1001,
      "sku_id": 2001,
      "title": "纯棉圆领T恤",
      "sku_title": "白色 / L",
      "images": ["https://cdn/.../1.jpg"],
      "description": "…",
      "attributes": {"材质": "纯棉", "克重": "180g"},
      "display_price_cents": 4500,
      "original_price_cents": 5900,
      "moq": 50,
      "stock_display": "充足",
      "note": "50 件起订"
    }
  ]
}
```

**白名单原则**：快照只包含上表字段，**禁止**包含：`cost_cents`、精确库存数、内部备注、供应商信息、`ext` 中的原始平台字段。

### 5.1 库存展示三档

| 配置 | 展示 |
|---|---|
| `none` | 不显示 |
| `range` | `充足` (>100) / `紧张` (1–100) / `缺货` (0) |
| `exact` | 精确数字（仅对可信客户开启） |

`live` 模式下库存实时读，但仍然按这三档转换，不暴露精确值（除非 `exact`）。

---

## 6. 两种模式对比

| | snapshot（默认） | live |
|---|---|---|
| 生成时 | 冻结完整 payload | 只存商品 ID 列表（在 `share_snapshot.payload` 存 `item_refs`） |
| 访问时 | 直接读快照 | 实时查商品 + 实时解析价格 + 实时库存 |
| 上游改价 | 不影响 | 不影响（价格来自报价单） |
| 上游改标题/图 | 不影响 | **会变** |
| 商品被删 | 不影响 | 该条显示「已下架」 |
| 性能 | 一次查询 | N 次查询（需缓存） |
| 适用 | 正式报价单、合同附件 | 长期展示页、电子目录 |

`live` 模式必须在页面标注「实时数据，以下单时确认为准」。

---

## 7. 分享页接口（免登录）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/share/{token}` | 获取分享内容（含 link 元信息 + items） |
| POST | `/api/v1/share/{token}/verify` | 提交访问密码，换取 Cookie |
| POST | `/api/v1/share/{token}/inquiry` | 提交询价留言 |
| GET | `/api/v1/share/{token}/items/{spu_id}` | 单品详情（大列表懒加载用） |

响应示例：

```json
{
  "code": 0,
  "data": {
    "title": "2026 秋季新品报价",
    "customer_name": "杭州某某贸易",
    "valid_until": "2026-11-30T00:00:00Z",
    "currency": "CNY",
    "require_password": false,
    "allow_inquiry": true,
    "items": [ /* 同 §5 */ ]
  }
}
```

**限流**：按 token + IP 双维度，60 次/分钟；询价接口 3 次/分钟/IP。

---

## 8. 管理端接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/POST | `/api/v1/admin/customers` | 客户列表/创建 |
| PUT/DELETE | `/api/v1/admin/customers/{id}` | 更新/删除 |
| GET/POST | `/api/v1/admin/price-lists` | 报价单列表/创建 |
| PUT | `/api/v1/admin/price-lists/{id}` | 更新（bump version） |
| POST | `/api/v1/admin/price-lists/{id}/items:batch` | 批量设置报价条目 |
| GET/POST | `/api/v1/admin/share-links` | 分享链接列表/创建 |
| POST | `/api/v1/admin/share-links/{id}/regenerate` | 重新生成（快照刷新，写审计） |
| POST | `/api/v1/admin/share-links/{id}/revoke` | 撤销 |
| GET | `/api/v1/admin/share-links/{id}/visits` | 访问记录 |
| GET | `/api/v1/admin/inquiries` | 询价列表 |
| PUT | `/api/v1/admin/inquiries/{id}` | 更新状态/分配 |

创建分享链接请求：

```json
{
  "title": "2026 秋季新品报价",
  "customer_id": 12,
  "price_list_id": 34,
  "mode": "snapshot",
  "items": [{"spu_id": 1001, "sku_ids": [2001, 2002]}, {"spu_id": 1005}],
  "password": "optional",
  "expires_at": "2026-11-30T00:00:00Z",
  "max_views": 0,
  "allow_inquiry": true
}
```

响应中 `url` 字段含明文 token，**仅此一次**。

---

## 9. 边界情况

| 场景 | 行为 |
|---|---|
| 商品在生成后被删除 | snapshot 正常展示；live 显示「已下架」，不计入总数 |
| SKU 被删除 | 同上；快照保留原值 |
| 报价单被删除 | 链接仍可用但价格回落到规则 3/4（需审计记录） |
| 报价单过期 | 按 `expired_price_list` 配置 |
| 链接过期后访问 | 410 + `70102`，页面提示联系销售 |
| 同一 token 并发 100 次 | 计数原子更新，超限的返回 429/`70102`，不出现「超发」 |
| 客户转发链接给第三方 | 无法阻止；靠密码 + 有效期 + 访问日志降低风险 |
| 快照生成时商品无价格 | 该条 `show_price=false`，不阻塞整单 |
| 快照生成时全部商品无效 | 拒绝创建，返回 `70301` |
| 重新生成快照 | 覆盖旧快照，`checksum` 变化，写审计（`share.snapshot.regenerate`） |

---

## 10. 安全与合规

- token 只存哈希；日志中禁止出现完整 token（记录 `token_prefix`）。
- 访问日志存 IP 哈希（`sha256(ip + salt)`），不存明文 IP。
- 快照中禁止出现成本价、供应商、内部备注。
- 分享页响应加 `Cache-Control: private, no-store`，避免 CDN 缓存导致越权。
- 询价接口必须校验 token 有效 + 频率限制 + 内容长度上限（2000 字）。
- 导出/打印功能由前端实现，后端不提供批量下载（防止爬取）。
