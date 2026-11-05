# Go 服务 HTTP 协议

本文描述当前 Go 版服务，供 [NetLFS](https://github.com/CN-CARB/NetLFS) 或自定义 GMod 客户端使用。迁移前的文档另存为 [JS 协议历史记录](protocol-legacy-js.md)，其中的旧 Range 缺陷和 Cloudflare 部署说明不适用于当前实现。

## 定位与安全边界

- 服务临时存储原始字节，不负责序列化、压缩、消息通知或玩家认证。
- GMod 的 net 消息负责传递文件 ID；文件正文通过 HTTP 传输。
- API 前缀为 `/v1`，revision 为 `1`。建议使用 HTTPS。
- `/v1/register` 无需认证；返回的两个 token 权限相同，均可上传和下载。
- token 不绑定文件所有者、玩家、服务器或同次注册的另一枚 token。
- token 位于 URL 路径，避免将完整请求路径写入公开日志。
- 服务不计算哈希、不按内容去重，也不提供删除、列举、追加上传或撤销 token 的接口。

## 接口

| 方法 | 路径 | 成功响应 |
|---|---|---|
| GET | `/` | `302`，`Location: https://github.com/CN-CARB/NetLFS` |
| GET | `/v1/revision` | `200 {"revision":1}` |
| GET | `/v1/register` | `200 {"server":"…","client":"…","expiration":86400}` |
| POST | `/v1/write/{token}` | `200 {"id":"…"}` |
| GET / HEAD | `/v1/read/{token}/{id}` | `200` 完整文件，或有效 Range 的 `206` |
| GET | `/v1/size/{token}/{id}` | `200 {"size":"12345"}` |

`/` 不是健康检查接口；健康检查应使用 `/v1/revision`。

### 注册与过期

每次注册生成新的 server/client UUID，不续期或撤销已有 token。
`expiration` 表示 **token 有效期秒数**，固定为 86400，不是 Unix 时间戳或剩余时长。
数据默认也保存 86400 秒，但从各自上传时开始计时。
`GM_EXPRESS_EXPIRATION` 只覆盖数据 TTL，不改变 token TTL 或注册响应。

### 上传

请求体直接发送原始字节，建议 `Content-Type: application/octet-stream`，不要使用 multipart 表单或 Base64。
每次成功上传生成新 ID；重复上传相同内容也会创建不同文件。空文件合法。

最大文件为 **24 MiB（25165824 字节）**。应用先检查已知 Content-Length，随后用限制读取器检查实际正文；超过限制返回 `413`。反向代理可能提前拒绝请求，响应文本未必相同。

- 无效 token：`401 Invalid Request Parameters`。
- 超过大小限制：`413`。
- 存储失败（例如单个文件超过配置的整个缓存预算）：`500 Failed to store data`。
- 上传完成不代表接收者已经下载或处理；业务 ACK 应由客户端协议实现。

### 下载、Range 与 HEAD

完整下载返回原始字节及 `Content-Type: application/octet-stream`。无效 token 返回 `401`，文件丢失返回 `404`；认证先于文件查找。

使用 Go `http.ServeContent` 处理 Range。例如十字节文件：

| Range | 状态 | Content-Range | 正文字节数 |
|---|---|---|---:|
| `bytes=0-3` | 206 | `bytes 0-3/10` | 4 |
| `bytes=7-` | 206 | `bytes 7-9/10` | 3 |
| `bytes=-3` | 206 | `bytes 7-9/10` | 3 |
| `bytes=10-` | 416 | `bytes */10` | 错误正文 |

空文件按标准库行为返回空的 `200`，即使请求携带 Range。HEAD 不返回正文。
多范围请求遵循 Go 运行时策略；当前运行时配合模块的 Go 1.22 兼容设置默认忽略多范围，返回完整 `200`，不应假设一定返回 multipart。

Range 不会自动实现客户端断点续传。客户端必须保存分片、检查状态码和 Content-Range、合并并验证完整文件哈希；不能把返回的 `200` 全文件当作一个分片直接追加。

### 大小查询

`size` 是十进制字符串，表示实际存储的字节数。零字节文件返回 `{"size":"0"}`。
无效 token 返回 `401` 空正文；**不存在的文件仍返回 `200 {}`**，这是为兼容旧实现保留的行为。客户端必须验证字段存在，不能把缺失字段当成零。

大小由同一份文件内容计算，不存在旧 JS 版大小元数据与数据分离的问题。
查询成功不会预留文件；紧随其后的下载仍可能因过期或淘汰返回 `404`。

### 其他路径

未知 GET 返回 `406 Not Found - you may need to update the gm_express addon!`。其他未匹配方法返回 `404`。响应错误不保证为 JSON。

## LRU 与容量

- 文件和 token 均为进程内 LRU，重启或更换容器会清空。
- 默认最多 1024 个文件、512 MiB 保留正文；可通过 `GM_EXPRESS_MAX_ENTRIES`、`GM_EXPRESS_MAX_BYTES` 调整。
- token 缓存最多 4096 项，每次注册占两项。
- 成功读写提升最近使用顺序；读取不延长 TTL。
- 容量不足时先清理过期项，再逐个淘汰最久未使用的有效项。
- 超过整个字节预算的单次写入会失败，不为该失败写入淘汰旧文件。
- 字节预算不等于进程总内存：还需为请求缓冲、运行时等留空间。

## 推荐调用流程

1. 游戏服务端注册并保存 token，通过 net 将接收端 token 和服务配置通知玩家。
2. 发送方上传正文，获取文件 ID。
3. 通过 net 通知接收方消息类型、文件 ID 和客户端计算的哈希。
4. 接收方按需查询大小、下载或复用已校验的本地缓存。
5. 完整校验后交给业务回调；如有需要，通过 net 回传 ACK。
6. `401` 时重新获取可用 token；`404` 时按业务需要重新上传，重新注册不会恢复已丢失文件。

实现依据：[HTTP 路由](../goserver/main.go)、[LRU 存储](../goserver/store.go)、[Range/TTL 测试](../goserver/range_ttl_test.go)、[LRU 测试](../goserver/store_test.go)。
