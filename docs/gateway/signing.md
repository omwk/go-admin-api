# 签名与可执行示例

生产契约：`gateway/signature.go`、`auth.go`、ADR-022。下面两个完整示例文件参与 S3 的真实签名验收，不在本文另维护一份容易漂移的签名实现。

## 六段待签串

```text
METHOD + "\n" +
ESCAPED_PATH + "\n" +
CANONICAL_QUERY + "\n" +
X-Timestamp + "\n" +
X-Nonce + "\n" +
lowercase_hex(SHA256(exact_body_bytes))
```

UTF-8 编码，分隔符是单个 LF（0x0a），共五个分隔 LF，最后一段后没有换行。无 query 时第三段仍存在，是空字符串；GET 空 body 的最后一段为 `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`。不要签名 JSON 对象的重新序列化结果，应该签名实际发送的字节。

1. METHOD 使用实际 HTTP 方法，当前业务能力只有 GET。
2. PATH 使用请求中原始转义路径，不含 origin/query，不解码、不更改大小写或尾斜杠。现行订单 dispatcher 拒绝含 `%` 或反斜杠的路径；协议能表达转义路径不意味着能力会接受该路径。
3. Query 按 Go `url.ParseQuery` 解码，再按**解码后的 key 的 UTF-8 字节序**排序。重复 key（包括不同转义表示解码成相同 key）一律拒绝。key 非空，不允许 `&`、`=`、CR、LF；key/value 必须为有效 UTF-8。
4. 输出 `decoded_key=QueryEscape(decoded_value)`，用 `&` 连接；key 本身不再次转义。值使用 Go `url.QueryEscape`：空格为 `+`，加号为 `%2B`，斜杠为 `%2F`，百分号为 `%25`，十六进制字母大写；空值保留 `k=`。不要把 JavaScript 的 encodeURIComponent 原样当作等价实现。
5. `X-Timestamp` 为无前导零的十进制 Unix **秒**（非毫秒）。服务器判断 `now.Unix()-300 <= ts <= now.Unix()+300`，两端均包含。
6. nonce 使用密码学随机生成的 16 字节 hex 文本（32 位）；协议允许 32 位 `[A-Za-z0-9_-]`。每次传输，包括失败后重试，都生成新 nonce、时间戳和签名。

| 原始 query | canonical query |
|---|---|
| `z=&a=hello%20world` | `a=hello+world&z=` |
| `x=%2b%2F%25&name=%E4%B8%AD%E6%96%87` | `name=%E4%B8%AD%E6%96%87&x=%2B%2F%25` |
| `a=1&a=2` 或 `a=1&%61=2` | 400/10001，拒绝歧义 |

canonical query 是签名规则；端点还会做自己的参数校验。订单状态不接受任何 query，usage 只接受一个有效的 `date`。

签名为 `standard_base64(HMAC-SHA256(key=UTF8(issued_secret_text), message=UTF8(canonical)))`，长度 44、带标准 padding。**secret 是签发的 base64 文本，不能先 base64 decode，也不能改用 fingerprint。** 四个认证头各出现一次：`X-App-Id`、`X-Timestamp`、`X-Nonce`、`X-Signature`。body 上限 1MiB。

校验顺序：格式和应用主库状态 → 时间窗 → Redis SETNX（应用共享 nonce，TTL 600s）→ 所有有效 key 的 HMAC → IP 白名单 → 实时授权门禁。nonce 在验签前占位，错误签名也保留占位。更换 key 不能复用 nonce；DB/Redis 无法核验返回 503，不能放行或假称签名错误。

## bash + curl

设置环境变量（secret 从受控秘密存储读取，不写 shell 历史或开启 tracing）：

```bash
# CH_OPEN_BASE_URL: HTTPS API origin
# CH_OPEN_APP_ID: issued app_id
# CH_OPEN_SECRET: issued base64 text, loaded by your secret manager
# CH_OPEN_ORDER_ID: public_id returned by the per-order grant
bash scripts/gateway/examples/order-status.sh
```

[完整可执行 curl 示例](../../scripts/gateway/examples/order-status.sh) 会生成时间戳/nonce、哈希空体、签名并发送请求。支持用 `CH_OPEN_PATH=/open/v1/usage` 查询当天用量；该简洁 bash 示例仅签名无 query 的 GET，有 query 时使用下方 Go 客户端。脚本退出非零代表调用失败；成功输出标准 JSON 信封。

## Go SDK 风格

[独立客户端](../../scripts/gateway/client/client.go) 仅依赖标准库；[完整调用命令](../../scripts/gateway/example/main.go)：

```bash
go run ./scripts/gateway/example
# Or query a UTC date using the same signing client:
CH_OPEN_PATH='/open/v1/usage?date=2026-11-10' go run ./scripts/gateway/example
```

SDK 调用形态（在仓库模块内）：

```go
req, err := client.NewRequest(ctx, http.MethodGet, endpoint, appID, secret, nil)
if err != nil { return err }
response, err := httpClient.Do(req) // bounded client; redirects disabled
```

外部项目可复制标准库示例并替换模块导入路径。SDK 不把 raw endpoint/secret 放到错误信息中；发送后的 response body 必须关闭。重试逻辑遵循[错误表](errors.md)，不能自动重放原签名。

## 验证示例

```powershell
go test ./scripts/gateway/client
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/smoke.ps1 -RedisAddr 127.0.0.1:16379
```

协议测试随机生成 key，将独立客户端签名与生产 canonicalizer 对照，覆盖空 query、UTF-8、空值、空格/加号/百分号、转义 path 和重复/非法 query。第 15 步分别执行 Go 示例、bash 脚本及独立签名 helper，均须 HTTP 200，并断言三次成功恰有三次真实计量。测试链路只在隔离 loopback 上使用 HTTP；正式集成使用 HTTPS。
