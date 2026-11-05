> **历史文档（JS / Cloudflare 版本）**：下方保留此前文档正文；不描述当前 Go 服务。当前接口请参见 [Go 协议说明](protocol.md)。正文中的源码路径、线上状态及缺陷记录均属于迁移前版本。

# gm_express 服务 HTTP 协议说明

本文描述本仓库**当前实现**，用于编写自己的 GMod 发送端/接收端。除明确标注的建议外，接口行为均以源码为准；已知缺陷不会被当成标准协议保证。

## 1. 服务地址与定位

当前部署的 Base URL：

```text
https://gm-express.anon-nanning.party
```

- API 前缀：`/v1`；当前 revision：`1`。
- 直接使用 HTTPS。当前 VPS 将 HTTP 请求以 `308` 重定向至 HTTPS；这是反向代理配置，不是应用自带逻辑。
- 服务是**临时二进制存储中转站**：上传字节获得 ID，凭有效 token 和 ID 下载。
- 服务不提供消息推送、收件箱、WebSocket、玩家身份认证、序列化或自动压缩。
- 消息通知通常由 GMod 的 net 完成；真正的大数据通过 HTTP 传输。

## 2. 通用约定

| 项目 | 当前行为 |
| --- | --- |
| 认证位置 | token 位于 URL 路径中，不使用 `Authorization` 请求头 |
| token、数据 ID | 服务端生成的 UUID 字符串，客户端应视为不透明标识符 |
| 上传格式 | 原始请求体；建议 `Content-Type: application/octet-stream` |
| 下载格式 | 原始字节，`Content-Type: application/octet-stream` |
| JSON 响应 | 注册、上传结果、大小查询和 revision |
| 上传大小上限 | **24 MiB = 25,165,824 字节**，按实际请求体大小判断 |
| 数据默认 TTL | **24 小时 = 86400 秒** |
| token 默认 TTL | **24 小时 = 86400 秒** |
| 读取后是否删除 | 否，可以重复下载，也可以让多个接收者下载同一个 ID |
| 错误响应 | 不保证为 JSON；可能是文本、空响应体或反向代理错误页 |

上传不需要 multipart 表单，也不需要 Base64。服务不检查文件格式，不做压缩、解压或内容转换；空请求体在应用层也是允许的。

**token 并不是数据所有权凭证：**当前后端只检查 token 是否仍然存在。任意有效 token 配合某个数据 ID，都可以读取该数据，不要求与上传所用 token 相同，也不要求来自同一次注册。

## 3. 接口总览

以下路径均相对于 Base URL。正常成功状态均为 `200`，有问题的 Range 下载分支例外。

| 方法 | 路径 | 功能 |
| --- | --- | --- |
| GET | `/v1/register` | 注册两个 token |
| POST | `/v1/write/{token}` | 上传原始字节，返回数据 ID |
| GET | `/v1/read/{token}/{id}` | 下载数据 |
| GET | `/v1/size/{token}/{id}` | 查询上传字节数 |
| GET | `/v1/revision` | 查询服务修订号 |

### 3.1 注册：`GET /v1/register`

无请求体，不需要已有 token、账号、API Key 或 Steam 身份信息。

成功响应示例：

```json
{
  "server": "11111111-1111-4111-8111-111111111111",
  "client": "22222222-2222-4222-8222-222222222222",
  "expiration": 86400
}
```

以上 UUID 仅为示例，实际调用必须使用服务返回的值。

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `server` | string | 建议由游戏服务端使用的 token |
| `client` | string | 建议由游戏客户端使用的 token |
| `expiration` | number | 配置的有效期秒数，不是 Unix 时间戳，也不是动态剩余时长 |

`server` 与 `client` **权限相同、均可读写**。名称只是使用约定，后端没有角色绑定，也没有把两者组成可验证的会话关系。

默认配置下，数据和 token 都是 86400 秒，但各自从存入时开始计时，不是统一截止时间。自托管覆盖数据 TTL 时，`expiration` 只反映数据 TTL，详见第 6 节。

错误：`500`，文本 `Failed to register request`。没有刷新 token 的专用接口；失效后重新注册并通知相关接收者。

### 3.2 上传：`POST /v1/write/{token}`

请求示例：

```bash
curl --fail-with-body --silent --show-error --max-time 60 \
  -H 'Content-Type: application/octet-stream' \
  --data-binary @payload.bin \
  "https://gm-express.anon-nanning.party/v1/write/${TOKEN}"
```

`--data-binary` 会使用 POST，并保留文件中的二进制字节。不要改成表单编码，也不要把二进制放在 URL 参数里。

成功响应：

```json
{"id":"33333333-3333-4333-8333-333333333333"}
```

- 每次上传生成新 ID，不按内容去重。
- 没有幂等键、指定 ID、追加上传或覆盖数据接口。
- 上传成功仅表示存储操作已返回成功，**不代表接收方已下载或处理**。
- 重试 POST 可能创建额外的数据副本；需要客户端自行处理业务重复。

错误：

| 状态码 | 应用响应体 | 含义 |
| --- | --- | --- |
| 401 | `Invalid Request Parameters` | token 无效或失效 |
| 413 | `Data exceeds maximum size of 25165824` | 请求体超过 24 MiB |
| 500 | `Failed to store data` | 数据或大小元数据写入失败 |

应用会先读取整个请求体，再检查大小。当前 VPS 的 OpenResty 也设置了 `client_max_body_size 24m`，可能在请求进入应用前返回自己的 `413` 页面，不要依赖错误文本完全相同。

### 3.3 下载：`GET /v1/read/{token}/{id}`

```bash
curl --fail-with-body --silent --show-error --max-time 60 \
  "https://gm-express.anon-nanning.party/v1/read/${TOKEN}/${ID}" \
  -o received.bin
```

成功响应为上传时的字节，应用设置：

```text
HTTP 200
Content-Type: application/octet-stream
Content-Length: <响应体字节数>
```

不要把正常下载响应作为 JSON 解析。是否需要解压、反序列化，由发送端与接收端自行约定。

错误：

- `401`：token 无效，文本 `Invalid Request Parameters`。
- `404`：数据不存在、已过期或被缓存淘汰。

token 会先于数据 ID 被检查，因此“无效 token + 不存在的 ID”得到的是 `401`。

**建议不发送 `Range` 请求头，直接完整下载。** 当前分段下载实现存在缺陷，见第 7 节。

### 3.4 大小查询：`GET /v1/size/{token}/{id}`

```bash
curl --fail-with-body --silent --show-error --max-time 30 \
  "https://gm-express.anon-nanning.party/v1/size/${TOKEN}/${ID}"
```

成功响应示例：

```json
{"size":"12345"}
```

`size` 是**十进制字符串**，不是 JSON number；表示上传后的字节数。如果发送端先压缩，返回的是压缩后的长度，不是解压后的大小。客户端应检查字段存在且是合法非负整数，再做数值转换。

- 无效 token：`401`，空响应体。
- Cloudflare KV 上查不到大小：`404`，文本 `Size not found`。
- **当前自托管实现查不到大小：实测返回 `200 {}`。** 这是已知缺陷，不代表零字节；见第 7 节。

大小查询是可选步骤，不会预留数据，也不保证紧随其后的下载一定成功。大小元数据与数据分别存储，缓存淘汰或部分写入失败可能导致两者不一致。

### 3.5 修订号：`GET /v1/revision`

无需 token：

```bash
curl --fail --silent --show-error \
  https://gm-express.anon-nanning.party/v1/revision
```

```json
{"revision":1}
```

源码说明：revision 变化不一定意味着破坏性变更，但意味着调用方应检查是否需要更新。

### 3.6 其他路径

- `GET /`：`302` 跳转到原版 gm_express 的 GitHub 页面，不是健康检查接口。
- 未匹配的 GET 路径：`406`，文本 `Not Found - you may need to update the gm_express addon!`。
- 不要假设所有未知路径或不支持的方法都返回 `406`；这个兜底路由只显式注册为 GET。
- 当前应用未配置浏览器 CORS 中间件。浏览器跨域调用不能照搬 GMod 原生 HTTP 调用的假设。

## 4. 可直接运行的完整调用示例

要求 Bash、curl、jq、cmp。示例在当前目录写入 `payload.bin` 和 `received.bin`，运行前确认不存在需要保留的同名文件。

```bash
set -euo pipefail
BASE='https://gm-express.anon-nanning.party'

# 1. 注册；不要把含 token 的 JSON 输出到公开日志。
REGISTRATION=$(curl --fail-with-body --silent --show-error --max-time 30 \
  "$BASE/v1/register")
SERVER_TOKEN=$(jq -er '.server' <<< "$REGISTRATION")
CLIENT_TOKEN=$(jq -er '.client' <<< "$REGISTRATION")
jq -e '.expiration == 86400' <<< "$REGISTRATION" >/dev/null

# 2. 创建包含 NUL 和非 ASCII 字节的测试数据。
printf 'gm_express\0binary\377\n' > payload.bin

# 3. 上传并获取 ID。
UPLOAD=$(curl --fail-with-body --silent --show-error --max-time 60 \
  -H 'Content-Type: application/octet-stream' \
  --data-binary @payload.bin \
  "$BASE/v1/write/$SERVER_TOKEN")
ID=$(jq -er '.id' <<< "$UPLOAD")

# 4. 查询大小；拒绝缺字段的 200 {}。
SIZE_JSON=$(curl --fail-with-body --silent --show-error --max-time 30 \
  "$BASE/v1/size/$CLIENT_TOKEN/$ID")
SIZE=$(jq -er '.size | select(type == "string") | select(test("^[0-9]+$"))' \
  <<< "$SIZE_JSON")
printf 'Stored bytes: %s\n' "$SIZE"

# 5. 使用另一个 token 下载，验证字节完全一致。
curl --fail-with-body --silent --show-error --max-time 60 \
  "$BASE/v1/read/$CLIENT_TOKEN/$ID" -o received.bin
cmp payload.bin received.bin
printf 'Upload/download OK\n'
```

此示例验证的是 HTTP 服务，不包含 GMod 内部消息通知协议。

## 5. 自己实现 GMod 库时的推荐流程

以下是**调用方设计建议**，不是后端已经定义的 net 协议。

### 初始化

1. 游戏服务端调用 `/v1/register`，保存 `server` token。
2. 通过 GMod net 向玩家发送服务地址、`client` token 及自定义协议版本。
3. 客户端确认就绪后，再向其分发数据消息。
4. token 失效后重新注册、同步；不要无限重试旧 token。

### 服务端 → 玩家

```text
游戏服务端：序列化 → 可选压缩 → POST /v1/write/{serverToken}
HTTP 服务：返回数据 ID
游戏服务端：通过 net 发送消息名、传输 ID、数据 ID、编码/压缩信息
玩家客户端：GET /v1/read/{clientToken}/{id} → 校验 → 解压 → 反序列化
玩家客户端：调用业务回调；如需送达确认，通过 net 返回 ACK
```

多玩家接收相同内容时，可以只上传一次，再把同一个 ID 发给多个玩家。

### 玩家 → 服务端

```text
玩家客户端：序列化 → 可选压缩 → POST /v1/write/{clientToken}
HTTP 服务：返回数据 ID
玩家客户端：通过 net 把消息名和数据 ID 发给游戏服务端
游戏服务端：校验玩家权限、消息类型与频率
游戏服务端：GET /v1/read/{serverToken}/{id} → 校验 → 解压 → 反序列化
游戏服务端：调用业务回调；按需返回 ACK
```

自定义 net 控制消息可以携带：

| 字段 | 建议用途 |
| --- | --- |
| `messageName` | 业务消息路由 |
| `transferId` | 去重、ACK 和超时关联；区别于存储 ID |
| `dataId` | HTTP 上传返回的 `id` |
| `encodingVersion` | 自定义序列化协议版本 |
| `compression` | 是否压缩及所用算法 |
| `encodedSize` | 上传字节数；只能作为声明值，接收方仍需校验 |
| `decodedSize` / 校验和 | 可选；不能代替接收方自己的大小限制和真实性校验 |

必须自己实现的部分：消息路由、编解码、并发限制、超时、有限重试、业务去重、权限校验，以及需要时的 ACK。

GMod 端要检查实际 HTTP 状态码，不要仅凭“请求进入成功回调”判断业务成功。下载是二进制字符串，不能按文本处理 NUL 或进行隐式编码转换。解压时必须有独立的**解压后大小上限**，上传 24 MiB 上限不能防止压缩炸弹。

如果两端都使用自己的库，可以自行选择编码格式；若要与原版 `gm_express` 插件互通，还需核对原版的 net 消息和序列化格式，**实现本文 HTTP 接口不等于与原版客户端兼容**。

## 6. 有效期与存储语义

| 部署 | 默认数据 TTL | 默认 token TTL | 存储 |
| --- | --- | --- | --- |
| Cloudflare | 86400 秒 | 86400 秒 | KV |
| Docker 自托管 | 86400 秒 | 86400 秒 | 进程内 LRU |

自托管的 `GM_EXPRESS_EXPIRATION` 容器环境变量可以覆盖**数据 TTL**，单位秒；token TTL 仍固定为 86400 秒。此时 `/v1/register` 的 `expiration` 返回覆盖后的数据 TTL，不能把它理解为 token 的准确失效时间。设置时使用正整数秒值。

本仓库原始 `docker-compose.yml` 未声明传入此变量；仅在 Compose 的 `.env` 中写值不会自动注入容器，需要显式配置 `environment`。当前 VPS 的生产 Compose 已显式设置为 `86400`。

自托管存储还存在以下限制：

- **重启、重建容器或进程退出，会清空数据和 token。** 当前没有持久化存储。
- 数据缓存上限为 1024 个键，数据与大小元数据各占一个键；不是可容纳 1024 个完整文件的保证。
- token 缓存上限为 4096 个键，每次注册占两个键。
- 容量淘汰可能早于 TTL；读取成功不意味着后续一直可读。
- TTL 不是文件持久保存承诺，也不是接收方可以安全等待的时限。
- 没有续期数据、显式删除、列举数据或撤销 token 的专用接口。

客户端对 `401` 应重新获取可用 token；对 `404` 应视业务需要让发送方重新上传。重新注册 token **不会恢复已丢失的数据**。

## 7. 已知实现问题

### 7.1 Range 下载不可靠

`readRequest` 对 `Range` 会尝试返回 `206`，但实现不符合完整的 HTTP Range 语义：

- 只使用第一个范围，不支持真正的 multipart 多段响应。
- 数据切片使用 `slice(start, end + 1)`，响应头却使用 `end - 1` 作为结束位置。
- 例如足够长的数据请求 `bytes=0-1023`，正文为 1024 字节，`Content-Range` 却会写成 `bytes 0-1022/<总长>`。
- 后缀范围（如 `bytes=-500`）没有按“最后 500 字节”正确处理。
- 越界、非法范围没有规范的 `416` 处理。

**当前不要依赖 Range、并行分段下载或断点续传。** 如需这些能力，先修后端并补充边界测试。

### 7.2 自托管的缺失大小返回 `200 {}`

`readSizeRequest` 只用 `size === null` 判断缺失。Cloudflare KV 缺失值为 `null`，但自托管 LRU 返回 `undefined`，导致 JSON 序列化时省略 `size` 字段。

当前线上自托管实测：

```text
GET /v1/size/{有效token}/{不存在id}
HTTP 200
{}
```

客户端应拒绝该结果，不能默认转换成 `0`。真正零字节数据的成功大小响应是 `{"size":"0"}`。

下载接口使用 `data == null`，所以缺失数据在当前自托管服务仍返回 `404`。

## 8. 安全边界

- `/v1/register` 是公开接口；不要把“持有 token”当成某个玩家或服务器的身份认证。
- 数据未绑定所有者；ID 和 token 泄漏可能导致数据被读取。
- token 在 URL 路径中，可能出现在日志、监控、错误报告和终端历史里，避免公开打印。
- 当前 VPS 反向代理关闭了 access log，但应用自身的 Hono logger 仍可能记录请求路径；日志不是凭据安全边界。
- 默认没有按玩家或 token 的业务限流、配额或内容权限控制。
- 必须由游戏服务端根据发送者身份验证消息权限，不能只信任玩家提供的 ID、长度或压缩标记。
- 建议使用 HTTPS、限制并发与频率、限制解压后的大小、限制可接受的消息类型，并把存储服务视为非私密中转站。

## 9. 实现依据与验证范围

源码入口：

- [`index.js`](../index.js)：路由、token 校验、存取、大小上限、Range 解析、错误响应。
- [`setup_app.js`](../setup_app.js)：Cloudflare 默认 TTL。
- [`docker/selfhost.js`](../docker/selfhost.js)：自托管 LRU、数据 TTL 覆盖、固定 token TTL。

当前 VPS 配置（不等于仓库默认 Compose）：

- 服务目录：`/opt/gm_express_service`。
- 生产 Compose：`/opt/gm_express_service/compose.production.yml`。
- OpenResty 站点：`/usr/local/openresty/nginx/conf/sites-enabled/gm-express.conf`。
- HTTPS 反代至 `127.0.0.1:3001`，HTTP 返回 308；容器内服务端口为 `8000`。

已验证：可信 HTTPS、HTTP 308、revision、注册返回 86400、1048577 字节二进制上传/大小/下载完全一致、无效 token 的 401、缺失下载的 404、自托管缺失大小的 `200 {}`。TTL 为配置与代码核对结果，未通过等待 24 小时做过期实测；Range 缺陷依据源码确认，不应视为可用功能。
