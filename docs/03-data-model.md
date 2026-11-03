# 03 · 数据模型

- 版本：v1.0
- 数据库：PostgreSQL 16
- 关联：`docs/02-architecture.md`、`docs/05-platform-sync.md`

> **本文是表结构的唯一事实源。** Codex 不得自行增删字段；确需变更时，先在本文件提出修改，经确认后同步写迁移。
> 迁移文件命名：`migrations/000001_<domain>_init.up.sql` / `.down.sql`。

---

## 0. 全局约定

| 项 | 约定 |
|---|---|
| 主键 | `id bigint generated always as identity primary key` |
| 租户 | 所有业务表 `tenant_id bigint not null default 1`，唯一索引以它开头 |
| 时间 | `timestamptz`，默认 `now()`；业务时间字段显式命名（`paid_at`、`synced_at`） |
| 金额 | `bigint`，单位为**最小货币单位**（分）；字段名以 `_cents` 结尾；必须伴随 `currency char(3)` |
| 布尔 | `boolean not null default false`，命名 `is_*` / `has_*` / `allow_*` |
| JSON | `jsonb not null default '{}'::jsonb`，命名 `ext` / `meta` / `payload` / `spec` |
| 软删除 | `deleted_at timestamptz`，配 `where deleted_at is null` 的部分索引 |
| 乐观锁 | `version integer not null default 0`，更新时 `where version = ?` 并 `version = version + 1` |
| 审计列 | `created_at / updated_at / created_by / updated_by` |
| 索引命名 | `idx_<table>_<col>[_<col>]`、`uq_<table>_<col...>` |
| 枚举 | 用 `text` + `check` 约束，不用 PG enum（改起来要 DDL 锁） |
| 物理外键 | 仅配置类小表及其关联表建立；业务大表及业务表到配置表的引用不建，由应用校验 + 一致性检查保证（ADR-011） |

**通用索引原则**
- 列表页筛选组合建**复合索引**，顺序：`tenant_id` → 等值列 → 范围列 → 排序列。
- 大表（`raw_payload`、`api_call_log`）按 `created_at` / `fetched_at` **按月 RANGE 分区**，主键必须包含分区键。
- 模糊搜索用 `pg_trgm` GIN 索引，不用 `%like%` 全表扫。

---

## 1. 平台内核（kernel）

### tenant
| 列 | 类型 | 说明 |
|---|---|---|
| id | bigint PK | 默认 1 |
| code | text | 唯一，如 `default` |
| name | text | |
| status | text | `active` / `suspended` |
| settings | jsonb | 租户级配置（默认时区、币种、报价缺失策略） |
| created_at / updated_at | timestamptz | |

### sys_setting
| 列 | 类型 | 说明 |
|---|---|---|
| id | bigint PK | |
| tenant_id | bigint | |
| group_key | text | `sync` / `share` / `billing` |
| item_key | text | |
| value | jsonb | |
| updated_by | bigint | |
| updated_at | timestamptz | |
- `uq_sys_setting_tenant_group_item (tenant_id, group_key, item_key)`

### audit_log
| 列 | 类型 | 说明 |
|---|---|---|
| id | bigint PK | |
| tenant_id | bigint | |
| actor_type | text | `user` / `system` / `api_client` |
| actor_id | bigint | |
| action | text | `shop.credential.rotate` 这类点分动作 |
| resource_type | text | |
| resource_id | text | |
| before / after | jsonb | 脱敏后的差异 |
| ip | inet | |
| user_agent | text | |
| trace_id | text | |
| result | text | `success` / `failure` |
| created_at | timestamptz | |
- `idx_audit_log_tenant_created (tenant_id, created_at desc)`
- `idx_audit_log_resource (tenant_id, resource_type, resource_id)`
- 只追加，禁止 UPDATE/DELETE。

### outbox_event
| 列 | 类型 | 说明 |
|---|---|---|
| id | bigint PK | |
| tenant_id | bigint | |
| aggregate_type / aggregate_id | text / text | |
| event_type | text | `order.synced` / `product.updated` |
| payload | jsonb | |
| status | text | `pending` / `processing` / `sent` / `failed` |
| attempts | int | |
| available_at | timestamptz | 退避重试用 |
| created_at / sent_at | timestamptz | |
- `idx_outbox_pending (status, available_at) where status in ('pending','failed')`
- 投递器 `FOR UPDATE SKIP LOCKED` 批量取，消费方必须幂等。

### idempotency_key
| 列 | 类型 | 说明 |
|---|---|---|
| id | bigint PK | |
| tenant_id | bigint | |
| scope | text | 接口标识 |
| key | text | 客户端传入 |
| request_hash | text | 防同键不同体 |
| response_code | int | |
| response_body | jsonb | |
| status | text | `processing` / `completed` |
| expires_at | timestamptz | 24h |
| created_at | timestamptz | |
- `uq_idempotency_scope_key (tenant_id, scope, key)`

### notification
| 列 | 类型 | 说明 |
|---|---|---|
| id | bigint PK | |
| tenant_id | bigint | |
| channel | text | `inbox` / `email` / `webhook` |
| target | text | 邮箱/URL/用户 ID |
| template | text | |
| payload | jsonb | |
| status | text | `pending` / `sent` / `failed` |
| attempts / last_error | int / text | |
| dedup_key | text | 同状态 24h 去重 |
| created_at / sent_at | timestamptz | |
- `uq_notification_dedup (tenant_id, dedup_key) where dedup_key is not null`

### file_object
| 列 | 类型 | 说明 |
|---|---|---|
| id | bigint PK | |
| tenant_id | bigint | |
| bucket / object_key | text | |
| original_name / mime / size_bytes | text / text / bigint | |
| sha256 | text | |
| purpose | text | `product_image` / `export` / `snapshot` |
| uploaded_by | bigint | |
| created_at | timestamptz | |

---

## 2. 身份与权限（identity）

### sys_dept
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| parent_id | bigint | 0 表示根 |
| name | text | |
| path | text | 物化路径 `1.3.7`，用于「本部门及下级」 |
| sort | int | |
| status | text | |
- `idx_sys_dept_path (tenant_id, path)`

### sys_user
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| username | text | 登录名 |
| password_hash | text | bcrypt cost 12，M10 登录压测后再裁决（ADR-013） |
| nickname / email / phone / avatar | text | |
| dept_id | bigint | |
| status | text | `active` / `disabled` / `locked` |
| failed_attempts | int | 租户内同用户名连续登录失败次数；IP 限流命中不计入 |
| locked_until | timestamptz | 连续失败 5 次锁 15 分钟；跨 IP 仍有效，IP 本身只限流 |
| last_login_at / last_login_ip | timestamptz / inet | |
| is_super | boolean | 超管跳过权限校验（仍走数据权限） |
| created_at / updated_at / deleted_at | timestamptz | |
- `uq_sys_user_username (tenant_id, username) where deleted_at is null`
- `idx_sys_user_dept (tenant_id, dept_id)`

### sys_role
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| code | text | `admin` / `operator` / `sales` |
| name | text | |
| data_scope | text | `all` / `dept` / `dept_and_child` / `self` / `custom` |
| remark | text | |
| status | text | |
- `uq_sys_role_code (tenant_id, code)`

### sys_permission
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| code | text | 三段式 `module:resource:action`，如 `shop:store:create`、`trade:order:export`（ADR-011） |
| name | text | |
| module | text | `system` / `shop` / `product` / `trade` / `crm` / `share` / `sync` / `gateway` / `billing`；必须等于 `code` 首段 |
| type | text | `api` / `button` / `data` |
- `uq_sys_permission_code (tenant_id, code)`
- code 固定三个非空小写段，首段必须等于 module；service、种子与测试共同校验，不保存通配符或接口表的动作缩写。

### sys_menu
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| parent_id | bigint | 0 根 |
| name | text | 菜单名（i18n key 可选） |
| type | text | `dir` / `menu` / `button` / `link` |
| path | text | 路由 path，如 `/shop/list` |
| component | text | 前端组件路径，如 `shop/list/index` |
| redirect | text | |
| icon | text | |
| sort | int | |
| visible | boolean | 是否在侧边栏显示 |
| keep_alive | boolean | |
| is_external | boolean | 外链 |
| permission_code | text | 按钮/菜单对应权限码；无权限关联时为 SQL NULL，不能用空字符串代替 |
| meta | jsonb | 额外路由元信息（badge、affix 等） |
| status | text | `active` / `disabled` |
- `idx_sys_menu_parent (tenant_id, parent_id, sort)`
- 外键 `fk_sys_menu_permission (tenant_id, permission_code) → sys_permission(tenant_id, code) ON DELETE RESTRICT`（配置表允许物理外键，见 ADR-011）
- 树深度 ≤ 5，服务层校验；循环引用检测。
- **鉴权只认 `sys_permission.code`**；本列仅用于前端按钮显隐与授权关联。

### 关联表
- `sys_user_role (user_id, role_id)` PK 二者
- `sys_role_menu (role_id, menu_id)` PK 二者
- `sys_role_permission (role_id, permission_id)` PK 二者
- `sys_role_dept (role_id, dept_id)` — `data_scope = custom` 时使用
- 上述配置授权关联建物理外键，删除受引用的配置实体用 RESTRICT；清除授权关联在 service 事务内显式完成。所有关联须校验同一租户，不能只凭 ID 引用绕过租户检查。

### sys_refresh_token
| 列 | 类型 | 说明 |
|---|---|---|
| id | bigint PK | |
| tenant_id / user_id | bigint | |
| session_id | text | 登录会话族 ID（ULID）；同一 `sid` 的 refresh 构成一条轮换链（ADR-012） |
| token_hash | text | sha256，唯一 |
| replaced_by_token_id | bigint | 轮换后指向新 token，用于追溯；宽限按 rotated 旧行首次 revoked_at 计算，不因重试重置 |
| revoked_reason | text | `rotated` / `logout` / `kick` / `replay` / `expired` |
| expires_at / revoked_at | timestamptz | |
| ip / user_agent | inet / text | |
| created_at | timestamptz | |
- `uq_refresh_token_hash (token_hash)`
- `idx_refresh_user (tenant_id, user_id, expires_at desc)`
- `idx_refresh_session (tenant_id, session_id) where revoked_at is null`
- 仅 `revoked_reason=rotated`、family 仍有效且距首次轮换 ≤30 秒时返回 409/20109，不签发、不撤 family；超过 30 秒重放撤整族。logout/kick/replay 撤销不享受宽限。
- 整族撤销需在 DB 中标记该 sid 全部历史及当前行的撤销原因，保留会话撤销事实；刷新与撤销串行协调。token 链属于运行流水，不因配置表外键政策给本表轮换指针增加物理外键。

---

## 3. 店铺与凭据（shop）

### platform
| 列 | 类型 | 说明 |
|---|---|---|
| id | bigint PK | |
| code | text | `shopee` / `taobao` / `shopify`，与 adapter 注册码一致 |
| name | text | |
| auth_type | text | `oauth2` / `api_key` / `cookie` / `session` |
| capabilities | jsonb | `{"resources":["product","order","inventory"],"webhook":true,"token_refresh":true}` |
| config_schema | jsonb | 该平台凭据需要哪些字段（前端据此渲染表单） |
| status | text | |
- `uq_platform_code (code)`

### shop
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| platform_id | bigint | |
| name | text | 内部显示名 |
| external_shop_id | text | 平台侧店铺 ID |
| region / timezone | text | `CN` / `Asia/Shanghai` |
| currency | char(3) | |
| owner_user_id | bigint | 负责人（数据权限用） |
| dept_id | bigint | |
| status | text | `active` / `paused` / `disabled` |
| settings | jsonb | 同步开关、限流参数、报价缺失策略 |
| last_sync_at | timestamptz | 冗余，列表页用 |
| created_at / updated_at / deleted_at | timestamptz | |
- `uq_shop_external (tenant_id, platform_id, external_shop_id) where deleted_at is null`
- `idx_shop_owner (tenant_id, owner_user_id)`

### shop_credential（一个店铺可有多种凭据，如 access_token + refresh_token）
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / shop_id | bigint | |
| kind | text | `oauth2` / `api_key` / `cookie` / `session` |
| label | text | `主账号 token` |
| status | text | `active` / `expiring` / `expired` / `needs_reauth` / `disabled` |
| current_version_id | bigint | 指向 credential_version |
| expires_at | timestamptz | 可空（静态 key） |
| refresh_at | timestamptz | 计划刷新时间 = expires_at - lead_time |
| last_checked_at / last_refreshed_at | timestamptz | |
| refresh_strategy | text | `auto` / `manual` / `none` |
| consecutive_failures | int | 连续刷新失败次数 |
| extra | jsonb | 非密文元信息：scope、app_key、UA 指纹标识 |
| created_at / updated_at | timestamptz | |
- `idx_cred_shop (tenant_id, shop_id, kind)`
- `idx_cred_refresh_due (status, refresh_at) where status in ('active','expiring')`
- **注意**：`extra` 中不得放 secret；敏感项一律进 `credential_version`。

### credential_version
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / credential_id | bigint | |
| version_no | int | 从 1 递增 |
| cipher_text | bytea | AES-256-GCM 密文（含 nonce，或分列） |
| nonce | bytea | 12 字节 |
| key_version | int | 主密钥版本，支持轮换 |
| aad | text | 附加认证数据，建议 `tenant:shop:kind:vN` |
| fingerprint | text | 明文的 sha256 前 12 位，用于「是否变化」判断，不可逆 |
| issued_at / expires_at | timestamptz | |
| created_reason | text | `initial` / `refresh` / `manual` / `migration` |
| created_by | bigint | 用户 ID 或 0（系统） |
| raw_meta | jsonb | 刷新接口返回的非敏感元信息（scope、token_type） |
| retired_at | timestamptz | 被新版本替代的时间 |
| purge_after | timestamptz | 默认 `retired_at + 90d`，之后由清理任务删除密文 |
- `uq_cred_version (credential_id, version_no)`
- `idx_cred_version_purge (purge_after) where purge_after is not null`

### credential_audit
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / credential_id | bigint | |
| action | text | `create` / `read` / `check` / `refresh` / `rotate` / `revoke` / `purge` |
| actor_type / actor_id | text / bigint | |
| result | text | `success` / `failure` |
| error_code / error_msg | text | |
| detail | jsonb | 不含明文 |
| ip / trace_id | inet / text | |
| created_at | timestamptz | |
- `idx_cred_audit (tenant_id, credential_id, created_at desc)`

### credential_refresh_run
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / credential_id | bigint | |
| trigger | text | `schedule` / `manual` / `on_demand` |
| status | text | `running` / `success` / `failed` / `skipped` |
| attempt | int | |
| error_code / error_msg | text | |
| started_at / finished_at | timestamptz | |
| next_retry_at | timestamptz | |
| trace_id | text | |
- `idx_cred_refresh_run (credential_id, started_at desc)`

---

## 4. 采集与映射（ingestion）

### raw_payload（按月 RANGE 分区，分区键 `fetched_at`）
| 列 | 类型 | 说明 |
|---|---|---|
| id | bigint | 与 fetched_at 组成主键 |
| tenant_id / platform_id / shop_id | bigint | |
| resource_type | text | `product` / `order` / `inventory` / `category` |
| external_id | text | 平台侧 ID |
| api_name | text | 来源接口，便于排查 |
| request_meta | jsonb | 请求参数、页码、耗时 |
| payload | jsonb | 原始报文，**不做任何裁剪** |
| payload_sha256 | text | 内容指纹 |
| payload_size | int | 字节数，用于监控异常 |
| mapping_version_id | bigint | 解析时使用的映射版本（可空：尚未解析） |
| http_status | int | |
| fetched_at | timestamptz | 分区键 |
- 主键 `(id, fetched_at)`
- `uq_raw_dedup (tenant_id, platform_id, shop_id, resource_type, external_id, payload_sha256, fetched_at)` ← 同一批次内去重；跨月自然分区
- `idx_raw_lookup (tenant_id, resource_type, external_id, fetched_at desc)`
- `idx_raw_unmapped (fetched_at) where mapping_version_id is null`
- 保留 180 天，之后归档到对象存储（Parquet/JSONL），分区 drop。

### schema_baseline
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / platform_id | bigint | |
| resource_type | text | |
| version | int | |
| fingerprint | text | 字段路径+类型集合的 sha256 |
| fields | jsonb | `{"$.item_name":"string","$.price.amount":"number"}` |
| sample_raw_id | bigint | |
| status | text | `active` / `superseded` |
| created_at | timestamptz | |
- `uq_schema_baseline_active (tenant_id, platform_id, resource_type) where status = 'active'`

### schema_drift_event
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / platform_id / shop_id | bigint | |
| resource_type | text | |
| baseline_id | bigint | |
| kind | text | `field_added` / `field_removed` / `type_changed` |
| path | text | `$.price.amount` |
| old_type / new_type | text | |
| severity | text | `info` / `warning` / `critical`（critical = 必填字段丢失） |
| sample_raw_id | bigint | |
| status | text | `open` / `acked` / `resolved` / `ignored` |
| resolution | text | |
| detected_at / resolved_at | timestamptz | |
| resolved_by | bigint | |
- `idx_drift_open (tenant_id, status, detected_at desc)`
- 同一 `(baseline_id, kind, path)` 24h 内只记一条（应用层去重）。

### mapping_profile
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / platform_id | bigint | |
| resource_type | text | |
| name | text | |
| active_version_id | bigint | |
| status | text | `active` / `disabled` |
| created_at / updated_at | timestamptz | |
- `uq_mapping_profile (tenant_id, platform_id, resource_type)`

### mapping_version
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / profile_id | bigint | |
| version_no | int | |
| status | text | `draft` / `published` / `archived` |
| spec | jsonb | 完整映射规则（结构见 `docs/05-platform-sync.md` §4） |
| checksum | text | spec 的 sha256，用于校验 |
| note | text | 变更原因 |
| published_by / published_at | bigint / timestamptz | |
| created_at | timestamptz | |
- `uq_mapping_version (profile_id, version_no)`
- **已发布不可改**：service 层拒绝更新 `status='published'` 的行，只能新建。

### quarantine_record
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / shop_id | bigint | |
| resource_type / external_id | text | |
| raw_id | bigint | |
| reason_code | text | `missing_required` / `type_mismatch` / `invalid_value` / `mapping_error` |
| reason_detail | jsonb | 缺了哪个字段、期望什么 |
| status | text | `pending` / `replayed` / `discarded` |
| replayed_at | timestamptz | |
| created_at | timestamptz | |
- `idx_quarantine_open (tenant_id, status, created_at desc)`
- `uq_quarantine_raw (raw_id, reason_code)` 防重复入隔离

### replay_run
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / platform_id / shop_id | bigint | |
| resource_type | text | |
| mapping_version_id | bigint | 目标映射版本 |
| from_raw_id / to_raw_id | bigint | 范围 |
| status | text | `running` / `success` / `partial` / `failed` |
| stats | jsonb | `{"total":0,"updated":0,"quarantined":0,"failed":0}` |
| started_at / finished_at | timestamptz | |
| triggered_by | bigint | |

---

## 5. 商品（catalog）

> `listing` / `listing_sku` 两张表在 M3 的 `000005_ingestion_init` 中创建（作为同步链路的落库目标），其余表在 M4 的 `000007_catalog_init` 中创建。字段定义一次写清，后续不再变。

### category（内部类目）
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| parent_id | bigint | |
| name | text | |
| path | text | 物化路径 |
| level | int | |
| sort | int | |
| status | text | |
- `idx_category_path (tenant_id, path)`

### platform_category
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / platform_id | bigint | |
| external_id | text | |
| parent_external_id | text | |
| name | text | |
| path | text | |
| is_leaf | boolean | |
| synced_at | timestamptz | |
- `uq_platform_category (tenant_id, platform_id, external_id)`

### category_mapping
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| platform_id / platform_category_id | bigint | |
| category_id | bigint | 内部类目，可空（未匹配） |
| confidence | numeric(3,2) | 规则匹配置信度 |
| source | text | `manual` / `rule` / `default` |
| created_at / updated_at | timestamptz | |
- `uq_category_mapping (tenant_id, platform_category_id)`

### product_spu
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| code | text | 内部编码，可空 |
| title / subtitle | text | |
| brand | text | |
| category_id | bigint | |
| main_image | text | |
| description | text | 富文本，可能超大 |
| status | text | `draft` / `active` / `archived` |
| source | text | `platform` / `manual` |
| attributes | jsonb | 规范化属性 `{"颜色":["红","蓝"]}` |
| ext | jsonb | 平台差异字段（未知字段自动落这里） |
| price_min_cents / price_max_cents | bigint | 冗余：活跃 listing_sku 平台价区间（ADR-017） |
| sku_count | int | 冗余：distinct 已绑定 `product_sku` 数 |
| version | int | 乐观锁，默认 0 |
| created_at / updated_at / deleted_at | timestamptz | |
- `uq_product_spu_code (tenant_id, code) where code is not null and deleted_at is null`
- `idx_product_spu_list (tenant_id, status, updated_at desc) where deleted_at is null`
- `idx_product_spu_title_trgm USING gin (title gin_trgm_ops)`
- 冗余列只由 catalog service 在同一事务内重算，禁止触发器与异步补偿。

### product_sku
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / spu_id | bigint | |
| code | text | 商家编码 |
| barcode | text | |
| title | text | 规格名，如「红色 / L」 |
| price_cents | bigint | 参考售价 |
| cost_cents | bigint | 成本价（**绝不对外**） |
| currency | char(3) | |
| stock_cached | int | 冗余缓存，真实库存看 inventory_snapshot |
| attributes | jsonb | |
| status | text | |
| created_at / updated_at / deleted_at | timestamptz | |
- `uq_product_sku_code (tenant_id, spu_id, code) where code is not null`
- `idx_product_sku_spu (tenant_id, spu_id)`

### product_media
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / spu_id | bigint | |
| sku_id | bigint | 可空 |
| type | text | `image` / `video` / `detail` |
| url | text | |
| sort | int | |
| created_at | timestamptz | |
- `idx_media_spu (tenant_id, spu_id, sort)`

### listing（平台商品，一个 SPU 可有多条）
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / shop_id / platform_id | bigint | |
| spu_id | bigint | 可空（未归并） |
| external_product_id | text | |
| title | text | 平台标题（可能与 SPU 不同） |
| price_cents / currency | bigint / char(3) | 平台展示价 |
| status | text | `online` / `offline` / `deleted` |
| stock_total | int | 冗余汇总 |
| raw_id | bigint | 最近一次原始报文 |
| synced_at | timestamptz | |
| ext | jsonb | |
| created_at / updated_at | timestamptz | |
- `uq_listing_external (tenant_id, shop_id, external_product_id)`
- `idx_listing_spu (tenant_id, spu_id)`
- `idx_listing_shop_status (tenant_id, shop_id, status, synced_at desc)`

### listing_sku
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / listing_id | bigint | |
| sku_id | bigint | 可空 |
| external_sku_id | text | |
| title | text | |
| price_cents | bigint | |
| stock | int | |
| ext | jsonb | |
| synced_at | timestamptz | |
- `uq_listing_sku (listing_id, external_sku_id)`

### inventory_snapshot
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / shop_id / listing_sku_id | bigint | |
| available / reserved | int | |
| synced_at | timestamptz | |
- `idx_inv_snapshot (listing_sku_id, synced_at desc)`
- 只保留 30 天，定时清理。

---

### export_job（M4b）
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| kind | text | `products` / `orders`（后续扩展） |
| status | text | `pending` / `running` / `succeeded` / `failed` / `expired` |
| filters | jsonb | 创建时的筛选/排序/字段版本快照，worker 只按此执行 |
| requested_by | bigint | 请求人 |
| file_id | bigint | 完成后指向 `file_object` |
| row_count / error_msg | bigint / text | |
| object_key | text | `exports/{tenant}/{id}.csv`，服务端生成 |
| expires_at | timestamptz | 对象保留 24h |
| created_at / started_at / finished_at | timestamptz | |
- `idx_export_job (tenant_id, status, created_at desc)`
- 下载时校验 tenant 与 owner，签名 URL 有效期 1h，与对象 24h 保留是两件事。

---

## 6. 订单（order）

### sales_order
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / shop_id / platform_id | bigint | |
| external_order_id | text | |
| order_no | text | 内部单号（可等于外部） |
| status | text | 统一状态，见状态机 |
| platform_status | text | 原样保留 |
| payment_status | text | `unpaid` / `paid` / `partial_refund` / `refunded` |
| fulfillment_status | text | `unshipped` / `shipped` / `delivered` |
| buyer_nick | text | |
| buyer_id | text | 平台买家 ID |
| currency | char(3) | |
| goods_amount_cents / shipping_fee_cents / discount_cents / total_amount_cents / paid_amount_cents | bigint | |
| item_count | int | |
| placed_at / paid_at / shipped_at / delivered_at / completed_at / closed_at | timestamptz | |
| cancel_reason | text | |
| ext | jsonb | 平台差异字段 |
| raw_id | bigint | |
| synced_at | timestamptz | |
| created_at / updated_at | timestamptz | |
- `uq_order_external (tenant_id, shop_id, external_order_id)`
- `idx_order_list (tenant_id, shop_id, status, placed_at desc)`
- `idx_order_time (tenant_id, placed_at desc)`
- `idx_order_buyer (tenant_id, buyer_nick)`
- `idx_order_ext_gin USING gin (ext jsonb_path_ops)` — 平台特殊字段检索

### order_item
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / order_id | bigint | |
| external_item_id | text | |
| listing_id / listing_sku_id | bigint | 可空（商品已删仍保留快照） |
| sku_id | bigint | 可空 |
| title / sku_title | text | 下单时快照 |
| image_url | text | |
| price_cents / qty / discount_cents / amount_cents | bigint / int | |
| ext | jsonb | |
- `uq_order_item (order_id, external_item_id)`
- `idx_order_item_sku (tenant_id, sku_id)`

### order_address（PII 加密）
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / order_id | bigint | |
| receiver_name_cipher | bytea | |
| phone_cipher | bytea | |
| address_cipher | bytea | 省市区+详细地址整体加密 |
| region_hint | text | 仅省份明文，用于统计与筛选 |
| postal_code | text | |
| key_version | int | |
| created_at | timestamptz | |
- `uq_order_address (order_id)`
- 解密需 `trade:pii:read` 权限，且写审计。

### order_status_history
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / order_id | bigint | |
| from_status / to_status | text | 统一状态 |
| platform_status | text | |
| source | text | `sync` / `manual` / `system` |
| operator_id | bigint | |
| occurred_at | timestamptz | 平台事件时间，非入库时间 |
| raw_id | bigint | |
| created_at | timestamptz | |
- `idx_order_history (order_id, occurred_at)`

---

## 7. 同步编排（sync）

### sync_job
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / shop_id | bigint | |
| resource_type | text | |
| cron | text | 如 `*/15 * * * *` |
| enabled | boolean | |
| options | jsonb | `{"page_size":50,"max_pages_per_run":20,"full_refresh_cron":"0 3 * * *"}` |
| last_run_id | bigint | |
| created_at / updated_at | timestamptz | |
- `uq_sync_job (tenant_id, shop_id, resource_type)`

### sync_cursor
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / shop_id | bigint | |
| resource_type | text | |
| cursor_type | text | `timestamp` / `page_token` / `id_watermark` |
| cursor_value | text | 水位线或分页 token |
| cursor_meta | jsonb | 上次页号、总数等 |
| updated_at | timestamptz | |
- `uq_sync_cursor (tenant_id, shop_id, resource_type)`

### sync_run
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / shop_id / job_id | bigint | |
| resource_type | text | |
| trigger | text | `cron` / `manual` / `webhook` / `retry` |
| status | text | `running` / `success` / `partial` / `failed` / `cancelled` |
| stats | jsonb | `{"fetched":0,"created":0,"updated":0,"skipped":0,"quarantined":0,"failed":0,"pages":0}` |
| error_code / error_msg | text | |
| started_at / finished_at | timestamptz | |
| trace_id | text | |
| triggered_by | bigint | |
- `idx_sync_run (tenant_id, shop_id, resource_type, started_at desc)`
- `idx_sync_run_running (status) where status = 'running'`

### sync_error
| 列 | 类型 | 说明 |
|---|---|---|
| id / run_id | bigint | |
| external_id | text | |
| stage | text | `fetch` / `map` / `validate` / `persist` |
| error_code / message | text | |
| raw_id | bigint | |
| retryable | boolean | |
| created_at | timestamptz | |
- `idx_sync_error_run (run_id)`

### dead_letter
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / shop_id | bigint | |
| resource_type / external_id | text | |
| payload | jsonb | 失败上下文（不含明文凭据） |
| reason | text | |
| attempts | int | |
| next_retry_at | timestamptz | |
| status | text | `pending` / `replayed` / `discarded` |
| created_at / replayed_at | timestamptz | |
- `idx_dead_letter (tenant_id, status, next_retry_at)`

---

## 8. 分享与报价（share）

### customer
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| name | text | 公司/客户名 |
| contact_name / phone / email | text | |
| level | text | `A` / `B` / `C` |
| tags | jsonb | |
| owner_user_id | bigint | 归属销售 |
| status | text | `active` / `disabled` |
| ext | jsonb | |
| created_at / updated_at / deleted_at | timestamptz | |
- `idx_customer_owner (tenant_id, owner_user_id)`
- `idx_customer_name_trgm USING gin (name gin_trgm_ops)`

### price_list
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / customer_id | bigint | customer 可空 = 通用报价单 |
| name | text | |
| currency | char(3) | |
| status | text | `draft` / `active` / `expired` |
| valid_from / valid_to | timestamptz | |
| note | text | |
| version | int | 乐观锁；改价即改 version |
| created_by | bigint | |
| created_at / updated_at | timestamptz | |
- `idx_price_list (tenant_id, customer_id, status)`

### price_list_item
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / price_list_id | bigint | |
| spu_id / sku_id | bigint | sku 可空（按 SPU 定价） |
| price_cents | bigint | 协议价 |
| moq | int | 起订量 |
| note | text | |
| created_at / updated_at | timestamptz | |
- `uq_price_item (price_list_id, spu_id, sku_id)`（`sku_id` 用 0 代替 NULL 以参与唯一约束）

### share_link
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| token_hash | text | sha256(token)，唯一 |
| token_prefix | text | 前 8 位，列表展示用 |
| type | text | `products` / `price_list` |
| mode | text | `snapshot` / `live` |
| price_list_id / customer_id | bigint | 可空 |
| title | text | 页面标题 |
| password_hash | text | 可空，bcrypt |
| expires_at | timestamptz | 可空 |
| max_views / view_count | int | 0 表示不限 |
| allow_inquiry | boolean | |
| status | text | `active` / `revoked` / `expired` |
| created_by | bigint | |
| created_at / revoked_at | timestamptz | |
- `uq_share_token (token_hash)`
- `idx_share_link_creator (tenant_id, created_by, created_at desc)`

### share_snapshot
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / share_link_id | bigint | |
| payload | jsonb | 冻结后的完整展示数据（含已解析价格） |
| checksum | text | |
| item_count | int | |
| created_at | timestamptz | |
- `uq_share_snapshot (share_link_id)`（一个链接一份快照；重新生成会覆盖并 bump checksum，需审计）

### share_visit
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / share_link_id | bigint | |
| ip_hash | text | 不存明文 IP |
| user_agent | text | |
| referer | text | |
| country / city | text | 可空，按 IP 粗解析 |
| created_at | timestamptz | |
- `idx_share_visit (share_link_id, created_at desc)`
- 保留 90 天。

### inquiry
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / share_link_id / customer_id | bigint | |
| contact_name / contact_phone / contact_email | text | |
| message | text | |
| items | jsonb | `[{"spu_id":1,"sku_id":2,"qty":10,"note":""}]` |
| status | text | `new` / `contacted` / `quoted` / `won` / `lost` |
| owner_user_id | bigint | 分配给谁 |
| created_at / updated_at | timestamptz | |
- `idx_inquiry (tenant_id, status, created_at desc)`

---

## 9. 开放网关（gateway）

### api_route
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| code | text | 唯一短码，如 `weather.now` |
| name / description | text | |
| method | text | |
| path | text | 对外路径 `/api/v1/open/weather/now` |
| upstream_type | text | `http` / `handler` |
| upstream_url | text | `http` 专用，支持 `{param}` 占位 |
| handler_key | text | `handler` 专用，注册在代码里的 key |
| auth_mode | text | `hmac` / `apikey` / `none`（`none` 仅内部） |
| visibility | text | `private` / `public` |
| status | text | `active` / `disabled` |
| timeout_ms / retry_count | int | |
| cache_ttl_seconds | int | 0 不缓存 |
| rate_limit_qps / rate_limit_burst | int | 0 用全局默认 |
| billing_mode | text | `free` / `per_call` / `package` |
| price_cents | bigint | `per_call` 时单价 |
| units_per_call | int | `package` 时每次消耗单元数 |
| request_schema / response_schema | jsonb | 用于文档与校验 |
| version | int | 路由版本，变更时递增 |
| created_by / created_at / updated_at | bigint / timestamptz | |
- `uq_api_route_code (tenant_id, code)`
- `uq_api_route_path (tenant_id, method, path)`

### api_client
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| app_id | text | 公开标识，ULID |
| name | text | |
| owner_user_id | bigint | |
| status | text | `active` / `suspended` |
| ip_whitelist | inet[] | 空 = 不限制 |
| balance_alert_cents | bigint | |
| last_used_at | timestamptz | |
| created_at / updated_at | timestamptz | |
- `uq_api_client_app_id (app_id)`

### api_client_secret
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / client_id | bigint | |
| secret_hash | text | sha256(secret)，不可逆 |
| secret_prefix | text | 前 6 位展示 |
| status | text | `active` / `expired` / `revoked` |
| expires_at | timestamptz | 可空 |
| created_at / last_used_at | timestamptz | |
- `idx_client_secret (client_id, status)`
- 一个 client 最多 2 个 `active`（平滑轮换）。

### api_client_route
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / client_id / route_id | bigint | |
| status | text | `active` / `suspended` |
| quota_daily / quota_monthly | int | 0 = 不限 |
| granted_by / granted_at | bigint / timestamptz | |
| expires_at | timestamptz | |
- `uq_client_route (client_id, route_id)`

### api_call_log（按月分区）
| 列 | 类型 | 说明 |
|---|---|---|
| id | bigint | 与 called_at 组成主键 |
| tenant_id / client_id / route_id | bigint | |
| trace_id | text | |
| method / path | text | |
| status_code | int | |
| latency_ms | int | |
| req_bytes / resp_bytes | int | |
| ip_hash | text | |
| error_code | text | |
| billing_mode | text | |
| cost_units / cost_cents | int / bigint | |
| billed | boolean | |
| called_at | timestamptz | 分区键 |
- `idx_call_log_client (tenant_id, client_id, called_at desc)`
- `idx_call_log_route (tenant_id, route_id, called_at desc)`
- 保留 90 天。

### usage_daily
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / client_id / route_id | bigint | |
| stat_date | date | |
| calls / error_calls | int | |
| units / amount_cents | int / bigint | |
| updated_at | timestamptz | |
- `uq_usage_daily (tenant_id, client_id, route_id, stat_date)`

---

## 10. 计费（billing）

### wallet
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| owner_type | text | `api_client` |
| owner_id | bigint | |
| currency | char(3) | |
| balance_cents | bigint | 可用余额 |
| frozen_cents | bigint | 冻结额 |
| version | int | 乐观锁 |
| status | text | `active` / `frozen` |
| updated_at | timestamptz | |
- `uq_wallet_owner (tenant_id, owner_type, owner_id, currency)`
- **约束**：`check (balance_cents >= 0 and frozen_cents >= 0 and balance_cents >= frozen_cents)`

### wallet_txn（只追加）
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / wallet_id | bigint | |
| type | text | `recharge` / `freeze` / `commit` / `release` / `refund` / `grant` / `adjust` |
| amount_cents | bigint | 正负均可，含义随 type |
| balance_after_cents / frozen_after_cents | bigint | |
| biz_type / biz_id | text | `api_call` / `recharge_code` / `subscription` |
| idempotency_key | text | 唯一 |
| note | text | |
| created_at | timestamptz | |
- `uq_wallet_txn_idem (idempotency_key)`
- `idx_wallet_txn (wallet_id, created_at desc)`
- 表级权限：应用账号禁止 UPDATE/DELETE。

### pricing_plan
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| code / name | text | |
| mode | text | `per_call` / `package` / `subscription` |
| currency | char(3) | |
| price_cents | bigint | 套餐售价 |
| unit_count | int | 套餐包含调用次数（`package`/`subscription`） |
| valid_days | int | 有效期，0 = 永久 |
| route_scope | jsonb | `{"route_ids":[...]}` 或 `{"all":true}` |
| status | text | |
- `uq_pricing_plan_code (tenant_id, code)`

### subscription
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / client_id / plan_id | bigint | |
| status | text | `active` / `expired` / `exhausted` / `cancelled` |
| remaining_units | int | |
| starts_at / ends_at | timestamptz | |
| auto_renew | boolean | |
| created_at | timestamptz | |
- `idx_subscription_active (client_id, status, ends_at)`

### recharge_code
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id | bigint | |
| code_hash | text | 唯一 |
| code_prefix | text | |
| amount_cents | bigint | |
| status | text | `unused` / `used` / `void` |
| used_by_client_id | bigint | |
| used_at / expires_at | timestamptz | |
| created_by | bigint | |

### billing_statement
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / client_id | bigint | |
| period_start / period_end | date | 自然月 |
| calls / units | int | |
| amount_cents | bigint | |
| status | text | `draft` / `issued` / `settled` |
| issued_at | timestamptz | |
- `uq_statement (tenant_id, client_id, period_start)`

### billing_reconcile_diff
| 列 | 类型 | 说明 |
|---|---|---|
| id / tenant_id / client_id | bigint | |
| stat_date | date | 对账的业务日期 |
| expected_cents | bigint | 按 `usage_daily` 汇总应扣 |
| actual_cents | bigint | 按 `wallet_txn(commit)` 实际扣 |
| diff_cents | bigint | 差值（正=少扣，负=多扣） |
| detail | jsonb | 差异明细（按 route 拆分） |
| status | text | `open` / `resolved` / `ignored` |
| resolution | text | 处理说明 |
| resolved_by / resolved_at | bigint / timestamptz | |
| created_at | timestamptz | |
- `uq_reconcile_diff (tenant_id, client_id, stat_date)`
- `idx_reconcile_open (tenant_id, status, stat_date desc)`
- 只追加；`status` 由人工或补偿任务更新，金额字段不可改。

---

## 11. 表数量与迁移拆分

| 迁移 | 内容 | 里程碑 |
|---|---|---|
| `000001_kernel_init` | tenant, sys_setting, audit_log, outbox_event, idempotency_key, notification, file_object | M0 |
| `000002_identity_init` | sys_dept, sys_user, sys_role, sys_permission, sys_menu, 4 张关联表, sys_refresh_token（共 10 张） | M1a |
| `000003_refresh_session_revoked` | `idx_refresh_session_revoked` 部分索引；因 `CREATE INDEX CONCURRENTLY` 不能进事务，必须独占一个迁移 | M1a |
| `000004_shop_init` | platform, shop, shop_credential, credential_version, credential_audit | M2 |
| `000005_ingestion_init` | raw_payload(分区), schema_baseline, schema_drift_event, mapping_profile, mapping_version, quarantine_record, **listing, listing_sku** | M3 |
| `000006_sync_init` | sync_job, sync_cursor, sync_run, sync_error, dead_letter, replay_run | M3 |
| `000007_catalog_init` | category, platform_category, category_mapping, product_spu, product_sku, product_media, inventory_snapshot, **export_job** | M4 |
| `000008_order_init` | sales_order, order_item, order_address, order_status_history | M5 |
| `000009_share_init` | customer, price_list, price_list_item, share_link, share_snapshot, share_visit, inquiry | M6 |
| `000010_credential_refresh` | credential_refresh_run + 索引 | M7 |
| `000011_gateway_init` | api_route, api_client, api_client_secret, api_client_route, api_call_log(分区) | M8 |
| `000012_billing_init` | wallet, wallet_txn, pricing_plan, subscription, recharge_code, billing_statement, billing_reconcile_diff, usage_daily | M9 |

> **编号规则**：严格按里程碑落地顺序连续编号，**不允许留空号**。
> golang-migrate 按版本号递增应用；如果后续插入一个比当前版本小的编号，该迁移永远不会被执行。
> 新增迁移只能取「当前最大号 + 1」，并在本表登记。
>
> `listing` / `listing_sku` 提前到 M3 建，是因为 M3 要验证「raw → 映射 → 规范层」整条链路，需要一个真实的落库目标；M4 只在其上补全商品主数据，不重建。
