# BE-IDN-026 · 菜单组件路径统一为简写

- ID: BE-IDN-026
- 类型: 重构
- 仓库: 后端
- 状态: 后端已完成并验收（前端联调未执行）
- 预计: 0.2 人日
- 分支: `feat/be-idn-026-menu-component`
- 依赖: BE-IDN-024

---

## 1. 问题

菜单 `component` 字段两种写法并存：

```
用户      system/user/index          简写
数据字典  system/dict/index          简写
存储配置  /system/storage/index.vue  全路径
文件管理  /system/files/index.vue    全路径
```

前端注册表为兼容两种写法，同一个页面注册了两个键：

```ts
'/system/dict/index.vue': …
'/system/dicts/index.vue': …
```

新增页面时很容易只写一种，导致路由解析失败。

## 2. 决定

**统一使用简写**：`<domain>/<page>/index`（不带前导斜杠、不带 `.vue`）。

| 菜单 | component |
|---|---|
| 用户 | `system/user/index` |
| 存储配置 | `system/storage/index` |
| 文件管理 | `system/files/index` |

前端注册表同步改为简写键（见 FE-IDN-018），并由 resolver 统一加前缀。

## 2.1 顺带：文件列表排除头像

上传头像会在 `file_object` 里留下一条 `purpose=avatar` 记录（实测 id=12、avatar.jpg），
于是**文件管理列表里会出现所有用户的头像**，与「文件库」语义不符。

要求：

- `GET /admin/files` 默认排除 `purpose=avatar`
- 提供 `include_avatar=true` 时才返回（供审计排查）
- 配额统计仍包含头像占用（避免绕过配额）

## 3. 改动

1. `seed_catalog.go` 中所有 `component` 改为简写
2. seed upsert 时更新已存在菜单的 component（BE-IDN-012 的 upsert 已支持）
3. 覆盖守卫测试：断言所有 seed 的 component 匹配 `^[a-z0-9/-]+$`（无前导斜杠、无 `.vue`）
4. 迁移旧值：seed 重跑即生效，无需 SQL 迁移

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 全部简写 | seed 中所有 component 匹配 `^[a-z0-9/-]+$` |
| 2 | 重跑生效 | 重跑 seed 后 `/auth/menus` 全部为简写 |
| 3 | 守卫 | 故意写一条 `/x/y.vue` → 测试失败 |
| 4 | 前端可用 | 配合 FE-IDN-018 后，所有页面可打开 |
| 4.1 | 头像不出现 | 文件列表默认不含 purpose=avatar；include_avatar=true 时出现 |
| 4.2 | 配额不绕过 | 头像占用计入 usage |
| 5 | 全量 | `verify` 通过 |

## 5. Codex prompt

```
实现 docs/tasks/BE-IDN-026-menu-component.md。

分支：feat/be-idn-026-menu-component（从最新 main c22c535 开）

必读：本卡、internal/modules/identity/seed_catalog.go、
seed_pages_test.go（覆盖守卫）、identity_seed.go（upsert）

改动：
1. 所有菜单 component 改为简写 <domain>/<page>/index
2. 增加格式守卫：^[a-z0-9/-]+$，禁止前导斜杠与 .vue 后缀
3. 重跑 seed 验证已存在菜单被更新

约束：
- 不改菜单 path 与权限码
- 与 FE-IDN-018 配套上线（前端不兼容会导致白屏，需同时合并）

验收按卡片 §4 五条逐条贴证据。
完成后停下报告，不要做前端。
```

## 6. 后端验收记录（2026-11-21）

本次执行以会话要求为准：从 `main 6e5449b` 创建 `feat/be-idn-026-menu-component`；前端解析器已兼容两种写法，后端可独立上线。本卡未修改前端。目录和按钮没有 component；所有非空 component 均由格式守卫检查。

### 接口补充

| 接口 | 参数 / 行为 |
|---|---|
| `GET /api/v1/admin/files` | `include_avatar`：boolean，默认 false；默认排除 `purpose=avatar`，true 时包含同租户头像；非法布尔值返回 400/10001。过滤在计数和分页之前执行。 |
| `GET /api/v1/admin/files/usage` | 保持统计当前用户所有文件，包含头像；上传配额检查继续使用同一统计。 |

### §4 逐条证据

| # | 结果 | 证据 |
|---|---|---|
| 1 | 通过 | `TestMenuSeedCoversDeclaredPages`：租户 1、23 各 30 个菜单、20 个声明页面；`TestMenuSeedComponentsUseShorthand` 检查所有非空 component，正则 `^[a-z0-9/-]+$` 并单独禁止前导 `/`。 |
| 2 | 通过 | `TestSeedMenuComponentIntegrationRerun` 先把已存在的存储配置、文件管理菜单写回旧全路径，并确认 HTTP 响应旧值；连续重跑真实 `SeedIdentity` 两次，`/auth/menus` 全部为简写，30 个菜单 ID、path、权限码及其余字段保持一致。 |
| 3 | 通过 | 临时把真实 seed 的 `system/storage/index` 改为 `/x/y.vue`，执行 `go test ./internal/modules/identity -run '^TestMenuSeedComponentsUseShorthand$' -count=1`，退出 1，报 `invalid seed component "/x/y.vue"`；finally 恢复后同命令退出 0。表驱动测试另覆盖独立前导 `/`、`.vue`、大写、下划线、空字符串。 |
| 4 | 未执行前端联调 | 本次明确仅做后端。前端解析器兼容两种格式是会话给定条件；页面逐项打开留给前端联调，未将后端测试等同于浏览器验收。 |
| 4.1 | 通过 | `TestFileLibraryIntegrationAvatarFilter`：默认 `total=4 count=4 avatars=0`；false 同默认；true 为 `total=5 count=5 avatars=1`；关键词/category 联合过滤、分页和外租户头像隔离均通过。非法布尔值返回 400。 |
| 4.2 | 通过 | `TestFileLibraryIntegrationAvatarUsageAndQuota`：头像记录占用 1048576 字节，HTTP usage 返回 `UsedBytes=1048576 QuotaBytes=1048576 FileCount=1`；后续上传返回 413。 |
| 5 | 通过 | `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1` → `all checks passed`，退出 0；vet、lint、`go test ./...` 全部通过，bootstrap 全量测试 `ok .../internal/bootstrap 420.299s`。 |

定向回归命令：

```powershell
go test ./internal/modules/identity -run 'TestMenuSeed' -count=1 -v
# PASS; ok github.com/omwk/go-admin-api/internal/modules/identity 6.150s
go test ./internal/bootstrap -run 'Test(FileLibraryIntegrationAvatar|SeedMenuComponentIntegrationRerun|SeedStorageMenusIntegration|FileModelListAndProviderContractFields)' -count=1 -v
# PASS; ok github.com/omwk/go-admin-api/internal/bootstrap 3.312s
```

### 其他自检

- `make lint`：最后一行 `golangci-lint run`，退出 0，无告警。
- `make test`：`go test ./...`，退出 0，47 个包输出 `ok`，其余包无测试；最后一行 `ok github.com/omwk/go-admin-api/scripts/smoke (cached)`。
- 迁移：`go test ./internal/kernel/db -run 'TestMigrateIntegration(ReversibleAndRepeatable|ConcurrentStartup)|TestMigrationIntegration(PartitionInvariants|TenantAndIdempotencyUniqueness|VersionDefaultsAndCompositeKeys)' -count=1 -v` → PASS，`ok .../internal/kernel/db 8.506s`。覆盖全链 up、完整 down 到底、再次 up、并发启动和关键结构不变量；本卡无 SQL 迁移。
- 分片：`powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-test-groups.ps1` → `Source 650 = shards 649 + excluded 1`，`PASS: every integration test has exactly one shard or an explicit exclusion.`；新增 3 个集成测试全部归入 core。
- 空实现：`rg -n 'TODO|not implemented' internal` 无匹配。
- 日志：人工检查完整 diff 及改动文件中的日志调用；未新增运行日志，新增测试日志仅含菜单数量、分页数量、字节数和状态码，无凭据或个人信息。
- 改动 Go 文件最大 225 行，最大函数 47 行；按 gofmt 后的顶层 `func` 与闭合 `}` 逐个计算，均满足 400/80 行限制。
- 已清理：上述定向测试使用 `testenv.Database` 隔离数据库，测试退出时关闭连接并 DROP DATABASE / schema，命令退出 0。未向开发业务库写入审计、临时实体或配置，因此无开发库 audit-prune、业务删除 SQL 或配置恢复项；保留的本卡测试数据：无。

偏差说明：验收 #4 的前端页面打开验证未执行，遵循本次仅后端的范围；其余后端范围无偏差。

文件 / 函数行数检查命令（PowerShell；检查本卡所有变更 Go 文件）：

```powershell
$goFiles = @(git diff --name-only -- '*.go') + @(git ls-files --others --exclude-standard -- '*.go')
foreach ($goFile in $goFiles) {
    $lines = [IO.File]::ReadAllLines((Join-Path (Get-Location) $goFile))
    $maxFunction = 0
    for ($i = 0; $i -lt $lines.Length; $i++) {
        if ($lines[$i] -match '^func ') {
            $start = $i
            if ($lines[$i] -notmatch '\}\s*$') {
                do { $i++ } while ($i -lt $lines.Length -and $lines[$i] -notmatch '^\}')
            }
            $maxFunction = [Math]::Max($maxFunction, $i - $start + 1)
        }
    }
    if ($lines.Length -gt 400 -or $maxFunction -gt 80) { throw "size limit exceeded: $goFile" }
    "$goFile lines=$($lines.Length) max_function_lines=$maxFunction"
}
```
