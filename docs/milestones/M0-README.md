# M0 本地启动

范围：T0.1–T0.9。T0.6–T0.9 已实现；2026-10-30 使用 Docker Desktop
补验，通过 Go 1.23.12 容器内的 lint、单测、race 与 PostgreSQL 16 集成测试，
以及隔离 schema 中的迁移 up/down/up、1 秒就绪缓存和工作流语法检查。
API/PostgreSQL/Redis Compose 容器均健康；API 镜像为 34,471,232 字节（34.5 MB），
使用 app:app 非 root 用户。本机 6379 已占用，补验通过忽略的临时 Compose 覆盖
将 Redis 映射到 127.0.0.1:16379，仓库默认端口不变。MinIO 可选 profile 改为
从官方 GitHub 同版本发布二进制构建（amd64/arm64 分别固定 SHA-256），
以解决官方镜像仓库返回 UNAUTHORIZED；版本仍为 RELEASE.2024-12-18T13-15-44Z。
镜像以 minio:minio 运行，用 `/minio/health/ready` 检查健康，不需要额外的 mc 客户端。
远程 CI 结果以 [GitHub Actions](https://github.com/omwk/go-admin-api/actions)
中 `feat/M0-bootstrap` 分支对应提交的 push workflow 为准。
`identity/contract.go` 已按架构补齐包声明，未添加业务接口。
Redis 故障及恢复已有前轮实测记录；本轮停止 Redis
的重跑被自动审批以 blocked by policy 拒绝，未执行该重跑。
需要 Go 1.23、GNU Make、golangci-lint 1.64.8、Docker Compose v2。

1. 在仓库根目录准备配置与本次开发环境变量（以下为 Bash）：

   ```bash
   cp configs/config.example.yaml configs/config.local.yaml
   export CH_CONFIG_PATH=configs/config.local.yaml
   export CH_POSTGRES_PASSWORD=$(openssl rand -hex 32)
   export CH_CRYPTO_MASTER_KEY=$(openssl rand -base64 32)
   export CH_STORAGE_ACCESS_KEY=$(openssl rand -hex 12)
   export CH_STORAGE_SECRET_KEY=$(openssl rand -hex 32)
   export CH_DB_DSN="postgres://commercehub:${CH_POSTGRES_PASSWORD}@localhost:5432/commercehub?sslmode=disable"
   ```

   必填值留空以避免分发固定凭据。变量仅在本机保存，不要提交或打印。
   使用已有数据库卷时沿用原来的数据库密码，不要每次重建密码。
   Windows PowerShell 可用 `RandomNumberGenerator.GetBytes` 生成字节，
   `Convert.ToBase64String` 编码主密钥，并用 `$env:CH_...` 设置同名变量。

2. 启动数据库和 Redis；需要对象存储时加 `--profile storage`：

   ```bash
   docker compose -f deploy/docker-compose.yml up -d --wait
   # docker compose -f deploy/docker-compose.yml --profile storage up -d --build --wait
   ```

   MinIO 构建需要访问 GitHub 发布资产；拉取或下载超时可使用本机 Docker Desktop 代理。
   使用已有 MinIO 数据卷时沿用原来的存储凭据。

3. 建表并验证：

   ```bash
   make migrate-up
   make migrate-version
   TEST_DATABASE_URL="$CH_DB_DSN" make lint test test-integration
   ```

4. 启动 API：

   ```bash
   make run-api
   # 或 docker compose -f deploy/docker-compose.yml --profile app up -d --build
   ```

5. 在另一终端检查服务：

   ```bash
   curl -i localhost:8080/healthz
   curl -i localhost:8080/readyz
   curl -s localhost:8080/metrics | grep http_requests_total
   curl -s localhost:8080/api/v1/admin/system/info
   ```

`/readyz` 在顶层返回 `status=ok|degraded`，`data` 列出 `db` 和 `redis` 状态，
并保留 `code/message/trace_id`。成功与失败均缓存 1 秒，并发请求共享刷新。
停止 Redis 后最多等待缓存窗口加探测超时，再检查 503 与 `redis=unavailable`；
`/healthz` 仍为 200。运维端点只应通过内网访问。

`make build` 输出 API、worker、CLI 到 `bin/`；`VERSION`、`BUILD_TIME`、`COMMIT`
可由构建系统传入。`make docker` 构建非 root 的 API 镜像。
`make run-worker` 当前只执行已有配置校验；队列业务处理属于后续里程碑。
CI 在 push/PR 时执行 vet、lint、带真实 PostgreSQL 的 race 测试，并检查镜像小于 50 MB。
`make help` 列出目标；未设置 `TEST_DATABASE_URL` 时数据库集成测试会跳过。
