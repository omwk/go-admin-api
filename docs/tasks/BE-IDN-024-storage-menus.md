# BE-IDN-024 · 存储菜单与上传人信息

- ID: BE-IDN-024
- 类型: 修复
- 仓库: 后端
- 状态: 已实现，验收通过（2026-11-21）
- 预计: 0.3 人日
- 分支: `feat/be-idn-024-storage-menus`
- 依赖: BE-IDN-021/023、FE-IDN-016（均已合并）

---

## 1. 问题（前端接入时发现）

### ① 两个页面没有菜单

`/system/storage`（存储配置）与 `/system/files`（文件管理）在后端菜单种子中不存在，
侧边栏看不到，只能手动输地址。

### ② 上传人无法显示

`GET /admin/files` 未返回上传人名称，前端「上传人」列只能显示 `—`。

## 2. 改动

### 2.1 补菜单（系统目录下）

```
系统 (dir, /system)
├ …
├ 存储配置  /system/storage   /system/storage/index.vue   system:storage:manage  sort 12
└ 文件管理  /system/files     /system/files/index.vue     system:file:manage     sort 13
```

- 组件路径必须与前端 `component-registry` 的键**逐字一致**：
  `/system/storage/index.vue`、`/system/files/index.vue`（已确认存在）
- 权限码已存在（BE-IDN-021 已 seed）
- 写入 `seed_pages` 覆盖守卫清单，防止将来再次漏配

### 2.2 文件列表返回上传人

`GET /admin/files` 与详情增加：

```json
{ "uploaded_by": 12, "uploaded_by_name": "admin" }
```

- 名称取 `sys_user.username`（被删用户回退 `#id`）
- 不返回邮箱、手机等敏感字段

详情接口经确认新增为 `GET /admin/files/{id}/metadata`；原 `GET /admin/files/{id}`
继续返回下载文件流。列表、详情、上传返回同一安全文件投影。

| 方法 | 接口（统一前缀 `/api/v1`） | 结果 |
|---|---|---|
| GET | `/admin/files` | 分页元数据，含上传人 ID / 名称 |
| GET | `/admin/files/{id}/metadata` | 同租户文件详情 JSON，含上传人 ID / 名称 |
| GET | `/admin/files/{id}` | 保留文件流下载 |
| POST | `/admin/storage-providers/test` | 测试请求体配置，不保存配置 |
| POST | `/admin/storage-providers/{id}/test` | 保留按已保存配置测试 |

### 2.3 测试连接的体验修正（顺带）

当前 `POST /storage-providers/{id}/test` 只能用**已保存**的配置。

新增可选接口：

```
POST /admin/storage-providers/test
body: { endpoint, port, bucket, use_ssl, region, access_key, secret_key }
→ 与按 id 测试相同的响应
```

用途：新建配置时可以先测再保存。保留按 id 的接口不变。

## 3. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 菜单可见 | 超管登录后 `/auth/menus` 含「存储配置」「文件管理」 |
| 2 | 权限过滤 | 无 `system:storage:manage` 的账号看不到存储配置 |
| 3 | 路由可打开 | 前端从菜单进入两个页面，不再 404 |
| 4 | 覆盖守卫 | 从 `seed_pages` 删除任一条 → 测试失败 |
| 5 | 上传人 | 列表与详情返回 `uploaded_by_name`；用户删除后回退 `#id` |
| 6 | 隐私 | 响应不含邮箱/手机 |
| 7 | 未保存测试 | 新接口用请求体配置测试成功；错误密钥返回 access_denied |
| 8 | 契约 | OpenAPI 更新，双向一致 |
| 9 | 全量 | `verify` 通过 |

## 4. Codex prompt

```
实现 docs/tasks/BE-IDN-024-storage-menus.md。

分支：feat/be-idn-024-storage-menus（从最新 main 29db3ec 开）

必读：本卡、internal/modules/identity/seed_catalog.go、
internal/modules/identity/seed_pages_test.go（页面覆盖守卫清单）、
internal/modules/file/file.go（列表查询）、
internal/modules/system/storage_http.go（test 接口）、
前端 apps/admin/src/router/component-registry.ts（确认组件键）

改动：
1. 补两条菜单（存储配置、文件管理），写入 seed 覆盖清单
2. 文件列表/详情返回 uploaded_by 与 uploaded_by_name（被删用户回退 #id）
3. 新增 POST /admin/storage-providers/test（用请求体配置测试）
4. OpenAPI 同步

约束：
- 组件键必须与前端 component-registry 完全一致
- 响应不含邮箱/手机
- 保留按 id 测试的接口

验收按卡片 §3 九条逐条贴证据：
- 第 1、3 条要贴超管菜单 JSON 与页面可打开
- 第 7 条贴未保存测试的请求与响应
完成后停下报告，不要做前端。
```

## 5. 验收记录（2026-11-21）

基线：`29db3ecf0266b91e886e12913338d9c07ceaa829`；分支：
`feat/be-idn-024-storage-menus`。前端仓库仅用于读取 registry 和运行既有页面，工作区无改动。

| # | 结果与证据 |
|---|---|
| 1 | 超管 `admin`、`is_super=true` 的真实响应见 [superadmin-menus.json](evidence/BE-IDN-024/superadmin-menus.json)，系统目录含两个组件键、权限及排序 12/13。 |
| 2 | `TestSeedStorageMenusIntegrationVisibilityAndPermissions` 验证 operator 无 `system:storage:manage` 且菜单不含 `/system/storage`，见 [targeted.txt](evidence/BE-IDN-024/targeted.txt)。 |
| 3 | 超管从系统侧栏依次点击进入，页面正常渲染；[存储配置截图](evidence/BE-IDN-024/storage-page.jpg)、[文件管理截图](evidence/BE-IDN-024/files-page.jpg)、[浏览器记录](evidence/BE-IDN-024/browser.txt)。组件键已与前端 registry 逐字比对。 |
| 4 | `TestMenuSeedCoverageRejectsMissingPage` 分别移除 storage/files 种子并断言 `missing declared page`；两个子测试通过。另用 Go overlay 分别删除 `seed_pages` 的两个新声明，均实测测试失败、退出码 1，见 [menu-mutation.txt](evidence/BE-IDN-024/menu-mutation.txt)。同步移除了已被投影的两项权限豁免，完整源码通过记录见 [menu-guards.txt](evidence/BE-IDN-024/menu-guards.txt)。 |
| 5 | `TestFileMetadataIntegrationUploaderAndPrivacy` 验证列表与详情一致：`operator` → 删除后 `#1`；缺失用户 `#99999999`；无上传人 `null` / 空名称，见 [targeted.txt](evidence/BE-IDN-024/targeted.txt)。 |
| 6 | 文件响应仅九个安全字段，测试排除 email/phone/password_hash 等字段；跨租户查询返回 404、列表为空，见同一测试日志及 [checks.txt](evidence/BE-IDN-024/checks.txt)。 |
| 7 | 真实 MinIO：未保存配置返回 `ok`；错误密钥返回 `access_denied`；不存在的桶返回 `bucket_missing`。三组请求/响应（凭证脱敏）见 [unsaved-probe.json](evidence/BE-IDN-024/unsaved-probe.json)，每次 providers=0；旧按 ID 测试仍返回 200，默认配置未改变。 |
| 8 | `TestFileStorageOpenAPIBidirectionalContract` 通过；新接口的成功、参数/权限/租户错误响应经 OpenAPI 验证，见 [targeted.txt](evidence/BE-IDN-024/targeted.txt)。 |
| 9 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1` 通过：`all checks passed`，覆盖 vet、lint、全量单元/集成测试，见 [verify.txt](evidence/BE-IDN-024/verify.txt)。 |

### 未保存测试请求 / 响应摘录

以下为真实集成请求，凭证已脱敏；`endpoint` 的显式端口沿用现有优先级。

```http
POST /api/v1/admin/storage-providers/test
Content-Type: application/json

{"endpoint":"http://127.0.0.1:12087","port":9000,"bucket":"export-test-01m7xkzkdj65t3rchjyxe07jt9","use_ssl":false,"region":"us-east-1","access_key":"<redacted>","secret_key":"<redacted>"}
```

```json
{"code":0,"message":"ok","data":{"status":"ok"},"trace_id":"01M7XKZKDQPAZJ1BVA4WJC4H0K"}
```

同一请求换成错误密钥：

```json
{"code":0,"message":"ok","data":{"status":"access_denied"},"trace_id":"01M7XKZKEXDJVDSMG2K88J89NX"}
```

### 自检与清理

- `make lint` 通过，最后一行 `golangci-lint run`；[lint.txt](evidence/BE-IDN-024/lint.txt)。
- `make test` 退出码 0，47 个有测试的包全部通过；[test.txt](evidence/BE-IDN-024/test.txt)。
- 隔离 schema 中运行 `cli.exe migrate up` / `down` / `up`：`38 → 37 → 38`，均 `dirty=false`；[migration.txt](evidence/BE-IDN-024/migration.txt)。未新增或修改迁移；全量测试同时覆盖完整回滚、并发启动及结构不变量。
- `rg -n 'TODO|not implemented' internal` 无匹配；变更 Go 文件无调试打印；临时凭证未进入验收产物，见 [checks.txt](evidence/BE-IDN-024/checks.txt)。
- Go AST 检查全部变更文件：最大文件 348 行，最大函数 69 行；[sizes.txt](evidence/BE-IDN-024/sizes.txt)。
- CI 分片守卫：`Source 637 = shards 636 + excluded 1`；[test-groups.txt](evidence/BE-IDN-024/test-groups.txt)。
- 已清理：`DROP SCHEMA be_idn_024_c4da4d38eb5113a9 CASCADE` → 隔离验收 schema 及审计全部删除；测试库由 fixture 自动销毁。临时 API、Vite、MinIO、Redis 已停止，监听端口已释放；[cleanup.txt](evidence/BE-IDN-024/cleanup.txt)。本次未写开发 schema，无需删除开发库已有审计或业务数据。

偏差说明：详情 JSON 使用已确认的新增 `/files/{id}/metadata` 接口；其余无。未改前端代码。
