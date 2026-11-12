# BE-6 · 开发用订单造数命令

- 类型：开发工具（FE-3 前置）
- 预计：**0.5 人日**
- 分支：`feat/be-6-dev-orders`
- 依赖：无

---

## 1. 背景

订单表当前为空，而 FE-3（订单列表与详情）必须用真实数据联调。

订单**不能**用 SQL 直接插：

- 收货信息是 `SealAddress` 加密的独立信封（AAD 绑 tenant/order/revision）
- 金额、状态、时间线由 `Upserter` 计算
- 手工插入的数据无法通过 `GET /orders/{id}/address` 的重认证解密路径

同步是唯一的生产入口，但同步需要 mock 平台服务 + 店铺 + 凭证 + 任务，链路长且会污染开发库。

## 2. 目标

新增一个**仅开发用**的 CLI 命令，通过既有领域服务写入合成订单：

```
go run ./cmd/cli dev-seed-orders --tenant 1 --shop <id> --count 20
```

要求：

- 走 `order.Upserter` 或等价的服务入口，**不直接写表**
- 收货信息用 `SealAddress` + 真实 Dimens…（Sealer）加密
- 幂等：以 `external_order_id = dev-<n>` 为键，重复执行不产生重复订单
- 明确标记来源（`source = 'dev'` 或外部订单号前缀），便于清理
- 命令带 `--clean` 一键删除本命令产生的数据

## 3. 数据形态

每个订单：

| 字段 | 值 |
|---|---|
| external_order_id | `dev-0001` … |
| 状态 | 在 paid/shipped/completed/cancelled 间轮转 |
| 商品行 | 1–3 行，引用库内已有 SKU（若无 SKU 则跳过并提示） |
| 金额 | 单价 × 数量 + 运费；优惠部分为 0 |
| 下单时间 | 最近 60 天内随机 |
| 收货人 | 中文姓名、手机号、省市区、详细地址（合成假数据） |

## 4. 安全

- 命令仅在 `app.env != prod` 时可用；prod 下直接报错退出
- 输出不得打印完整手机号/地址（只打印订单号与状态）
- 不使用真实个人信息（姓名用「测试用户N」，手机号用 `1380000xxxx` 段）

## 5. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 生成 | `--count 20` 后 `GET /admin/orders` total=20 |
| 2 | 幂等 | 再执行一次仍为 20 |
| 3 | PII 可解密 | `GET /admin/orders/{id}/address`（带重认证）返回合成地址 |
| 4 | 金额一致 | 详情实付 = 商品行小计 + 运费 |
| 5 | 状态分布 | 四种状态都有 |
| 6 | 清理 | `--clean` 后 total=0，且不影响同步产生的订单 |
| 7 | 生产保护 | `app.env=prod` 时命令拒绝执行 |
| 8 | 无日志泄漏 | 输出与日志无完整手机号/地址 |
| 9 | 全量 | `verify` 通过 |

## 6. 不做

- 图形化工具
- 生成商品/店铺/凭证（已有 dev seed 与真实接口）
- 生产数据迁移

## 7. Codex prompt

```
实现 docs/tasks/BE-6-dev-orders.md。

必读：本卡、internal/modules/order/（Upserter、SealAddress、model）、
cmd/cli/（现有 seed 命令结构）、internal/modules/catalog/（SKU 查询）

范围：
1. 新增 cmd/cli 子命令 dev-seed-orders
2. 走领域服务写入，禁止直接 INSERT
3. 收货信息用 SealAddress 加密
4. 幂等 + --clean + prod 保护

约束：
- 不打印完整手机号/地址
- 使用 --tenant/--shop 参数，不硬编码
- 生成数据必须能被 GET /orders/{id}/address 解出

验收按卡片 §5 九条逐条贴证据。
完成后停下报告。
```
