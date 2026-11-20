# BE-IDN-024 · 存储菜单与上传人信息

- ID: BE-IDN-024
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
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

分支：feat/be-idn-024-storage-menus（从最新 main 40ee3d7 开）

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
