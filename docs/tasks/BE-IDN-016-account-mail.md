# BE-IDN-016 · 账号邮件模板与链接

- ID: BE-IDN-016
- 类型: 修复
- 仓库: 后端
- 状态: 待实现
- 预计: 1.5 人日
- 分支: `feat/be-idn-016-account-mail`
- 依赖: 无

---

## 1. 现象

注册验证邮件实际内容是：

```
请在 24 小时内打开邮箱验证链接：/auth/register/verify?tenant_id=1#token=3iFWcc...
```

三个问题：

### ① 链接是相对路径

`AccountLink(base, ...)` 的 `base` 来自 `app.public_url`，而本地 `.env` 没配，
于是 `"" + path` 得到 `/auth/register/verify?...`。**用户点不了**。

### ② 用数字租户 id

`?tenant_id=1` 与「登录用租户 code」的既定规则不一致；
且租户 id 是内部实现细节。

### ③ 模板极其简陋

```
请在 24 小时内打开邮箱验证链接：{{.Link}}
```

缺：称呼、品牌、租户名、链接独占行、有效期表述、一次性说明、
非本人操作提示、客服联系方式。同类模板（重置密码、邀请）同样简陋。

## 2. 根因

| 项 | 现状 |
|---|---|
| `app.public_url` | 可选配置，未配时空字符串直接拼接 |
| `AccountLink` | `?tenant_id=<id>` 硬编码 |
| 模板变量 | 只有 `TokenID`、`Link` 两个 |
| 模板正文 | 单行，无结构 |

## 3. 改动

### 3.1 站点地址必须配置

```
app.public_url   http(s)://<前端域名>
```

| 环境 | 行为 |
|---|---|
| prod | **启动时必填**，缺失或非 http(s) 绝对地址 → 启动失败 |
| dev | 未配置时默认 `http://127.0.0.1:5555` 并打一条 warn 日志 |

`AccountLink` 增加防御：`base` 为空时返回错误而不是拼出相对路径。

### 3.2 链接用租户 code

```
<public_url>/auth/register/verify?tenant=<code>#token=<raw>
```

- 一并修改 `invite` 与 `reset` 链接
- 前端 `account-link.vue` 同步改为读 `tenant`（见 FE-IDN-012），
  过渡期后端**同时带 `tenant_id`**，避免旧前端失效
- token 仍在 fragment：不进入服务端日志与 Referer

### 3.3 模板变量扩展

`identity.*` 队列时提供：

| 变量 | 来源 | 示例 |
|---|---|---|
| `AppName` | `app.name`（默认 `CommerceHub`） | `CommerceHub` |
| `UserName` | 用户昵称，空则用户名 | `张三` |
| `TenantName` | 租户名称 | `Acme` |
| `Link` | 3.2 生成 | `https://...` |
| `ExpiresInHours` | 令牌有效期 | `24` / `7` |
| `ExpiresAt` | 过期时间（RFC3339 UTC） | `2026-11-19T12:00:00Z` |
| `SupportEmail` | 系统设置 `notification.support_email` | `support@acme.com`（可为空） |

模板使用 `{{if .SupportEmail}}…{{end}}` 处理客服邮箱缺失。
所有变量**无条件传入**（空字符串也传），避免 `missingkey=error` 导致投递失败。

### 3.4 模板重设计（内置 seed）

**identity.verify**

```
主题：验证你的 {{.AppName}} 邮箱

{{.UserName}} 你好：

你正在 {{.AppName}}（租户：{{.TenantName}}）注册账号。请打开下面的链接验证邮箱：

{{.Link}}

该链接 {{.ExpiresInHours}} 小时内有效，且只能使用一次。
如果这不是你本人的操作，可以忽略本邮件，你的邮箱不会被注册。

—— {{.AppName}}
{{if .SupportEmail}}如遇问题请联系：{{.SupportEmail}}{{end}}
```

**identity.reset**

```
主题：重置你的 {{.AppName}} 密码

{{.UserName}} 你好：

我们收到了重置 {{.AppName}}（租户：{{.TenantName}}）账号密码的请求。
请打开下面的链接设置新密码：

{{.Link}}

该链接 {{.ExpiresInHours}} 分钟内有效，且只能使用一次。
如果这不是你本人的操作，请忽略本邮件，并建议联系管理员检查账号安全。

—— {{.AppName}}
{{if .SupportEmail}}如遇问题请联系：{{.SupportEmail}}{{end}}
```

**identity.invite**

```
主题：{{.AppName}} 邀请你加入 {{.TenantName}}

你好：

管理员邀请你加入 {{.AppName}} 的租户「{{.TenantName}}」。
请打开下面的链接接受邀请并设置登录密码：

{{.Link}}

该链接 {{.ExpiresInHours}} 小时内有效，且只能使用一次。
接受邀请后，你可以用邮箱和刚设置的密码登录。

—— {{.AppName}}
{{if .SupportEmail}}如遇问题请联系：{{.SupportEmail}}{{end}}
```

`format` 保持 `text`（本期不做 HTML 邮件）。

### 3.5 种子更新语义

内置模板（`identity.*`、`notification.*`、`credential.*`）随 seed **强制更新**
subject/body/format；租户自定义模板（code 不在内置清单）不动。

### 3.6 变量齐全性守卫

新增测试 `TestSeededTemplatesRender`：

- 列出全部内置模板
- 用「后端实际会提供的变量集合」渲染每条模板的 subject 与 body
- 任一模板引用了未提供的变量 → 测试失败（`missingkey=error` 会直接报错）
- 任一模板渲染后为空 → 失败

这条守卫直接回答「变量是否齐全」，也防止下次改模板时漏传变量。

## 4. 验收

| # | 项 | 断言 |
|---|---|---|
| 1 | 绝对链接 | dev 未配 public_url 时链接为 `http://127.0.0.1:5555/...` |
| 2 | prod 强校验 | 缺 public_url 启动失败，错误信息指明配置项 |
| 3 | 租户参数 | 链接含 `?tenant=<code>`（同时保留 `tenant_id` 兼容期） |
| 4 | token 位置 | token 只在 fragment，服务端日志与投递记录中不出现 |
| 5 | 模板内容 | 三条模板渲染后含称呼、租户、链接独占行、有效期、一次性、客服（有则显示） |
| 6 | 变量齐全 | `TestSeededTemplatesRender` 通过；故意加一个未提供变量时失败 |
| 7 | 种子更新 | 重跑 seed 后内置模板为新文案；自定义模板不变 |
| 8 | 真实发送 | 注册一个账号，收到邮件且链接可点击打开验证页并验证成功 |
| 9 | 重置/邀请 | 两条链接同样为绝对地址且可用 |
| 10 | 契约 | OpenAPI 中邀请/重置响应里的 `link` 描述更新为绝对地址 |
| 11 | 全量 | `verify` 通过 |

## 5. 不做

- HTML 邮件与 multipart
- 模板多语言（仅 zh-CN）
- 模板可视化编辑器的变量提示（前端另卡）

## 6. Codex prompt

```
实现 docs/tasks/BE-IDN-016-account-mail.md。

分支：feat/be-idn-016-account-mail（从最新 main 89d218a 开）

必读：本卡、internal/modules/identity/account_invite.go（AccountLink）、
identity_account_notification.go（Queue 与变量）、
internal/kernel/notifier/template.go（Render/missingkey）、
internal/kernel/config/config.go（app.public_url 校验）、
内置模板种子所在文件、internal/kernel/setting/（notification.support_email）

改动：
1. app.public_url：prod 必填校验；dev 默认 http://127.0.0.1:5555；AccountLink 空 base 报错
2. 链接改为 ?tenant=<code>，过渡期同时带 tenant_id；token 仍在 fragment
3. identity.* 变量扩展到 7 个（见卡片 §3.3），全部无条件传入
4. 重写三条模板（文案见卡片 §3.4），format 保持 text
5. 内置模板 seed 强制更新，自定义模板不动
6. 新增 TestSeededTemplatesRender 变量齐全性守卫

约束：
- 不记录 token 明文（日志、审计、投递记录）
- 不改模板表结构
- 不改 /auth/register/verify 的校验语义

验收按卡片 §4 十一条逐条贴证据：
- 第 8 条要真实注册并收到邮件（贴渲染后的正文，token 打码）
- 第 6 条贴守卫通过 + 故意失败各一次
完成后停下报告，不要做前端。
```
