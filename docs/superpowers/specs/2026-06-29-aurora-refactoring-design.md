# Aurora 重构设计文档

日期: 2026-06-29
状态: 草稿

## 1. 背景与目标

本项目基于 [aurora-develop/aurora](https://github.com/aurora-develop/aurora) 进行重构。
Aurora 是一个 ChatGPT Web → OpenAI API 协议转换代理，用 Go 编写。

### 现存问题

- **God 文件**：`initialize/handlers.go`（2464 行）和 `internal/chatgpt/request.go`（3416 行）职责混杂
- **全局状态**：`oaiDeviceID`、`BasicCookies`、`cachedDpl` 等全局变量导致代码不可测试
- **账号管理原始**：纯内存、无健康检查、无持久化、无指纹隔离
- **WSS 临时化**：每次请求创建 WebSocket，用完即关，无保活
- **配置散落**：`os.Getenv()` 散布在 8+ 个文件中
- **重复代码**：多个 handler 方法存在冗余（refresh / refresh_handler 等）
- **包命名不规范**：`proxys`、`typings` 等

### 重构目标

1. 代码结构清洁分层，可测试
2. 账户系统完整（三种类型 + 独立指纹 + WSS + 健康检查）
3. 消除全局状态
4. 配置集中化
5. 保持所有现有功能不变

---

## 2. 核心约束：internal 包必须谨慎处理

`internal/` 目录下的包（特别是 `internal/chatgpt/`、`internal/so/`、`internal/turnstile/`、`internal/prooftoken/`、`internal/fingerprint/`、`internal/toolcall/`）包含 **ChatGPT 逆向工程的关键逻辑**，包括：

- Sentinel 风控协议的请求/响应流程
- Proof of Work token 计算
- Turnstile（Cloudflare）求解
- SO (Sentinel Observer) token 生成
- 浏览器指纹模拟参数
- Tool call 协议解析与恢复

### 处理原则

1. **不删减**：这些包内的逻辑是逆向成果，重构时不得删减任何功能路径
2. **不重写**：核心算法（proof token 计算、turnstile 求解、so token 构建）保持原样
3. **只编排**：重构集中在代码组织——拆文件、明确接口、消除全局依赖
4. **测试覆盖**：这几个包必须有单元测试保障逆向逻辑不被破坏
5. **保留文件名关联**：如果当前包内的函数名有对应 ChatGPT 端点或协议名称，保持命名以便后续追踪

### 具体处理方式

`internal/chatgpt/request.go`（3416 行）目前混了三类职责：

| 职责 | 处理方式 |
|------|----------|
| **协议定义**（URL、Header、请求体构建、响应解析） | → 留在 `internal/chatgpt/`，拆分文件 |
| **HTTP 调用**（用 tls-client 真正发请求） | → 留在 `internal/chatgpt/`，它是 ChatGPT 协议的直接封装 |
| **业务编排**（sentinel→init→prepare→conversation 的调用顺序） | → 移到 `internal/service/` |

`internal/chatgpt/` 本身就是 `client` 层（ChatGPT 协议的客户端封装），不需要再套一层。

### 2.1 目录结构

```
new_aurora/
├── main.go
│
├── internal/
│   │
│   ├── config/
│   │   └── config.go                  ← NEW: Config 集中管理
│   │
│   ├── accounts/                      ← NEW: 账号管理系统
│   │   ├── account.go                     Account 结构体
│   │   ├── pool.go                        AccountPool (Acquire/Release)
│   │   ├── store.go                       JSON 持久化
│   │   ├── capabilities.go                账号能力映射
│   │   ├── fingerprint_profiles.go        8 个指纹画像
│   │   └── wss_actor.go                   WSS goroutine (连接/发送/保活/重连)
│   │
│   ├── proxy/
│   │   └── pool.go                      ← NEW: ProxyPool (IPv4 + IPv6)
│   │
│   ├── chatgpt/                         ← ★ 核心逆向包：拆文件，不改逻辑 ★
│   │   ├── sentinel.go                      sentinel prepare/finalize/ping 协议
│   │   ├── conversation.go                  conversation init/conduit/POST 协议
│   │   ├── websocket.go                     WSS URL 获取 + Dial + init + 流读取
│   │   ├── sse.go                           SSE 解析、handoff topic 提取
│   │   ├── files.go                         文件上传协议
│   │   ├── transcribe.go                    音频转写协议
│   │   ├── artifacts.go                     代码产物协议
│   │   ├── artifact_delivery.go             产物流式推送
│   │   ├── client_state.go                  ChatClientState
│   │   ├── cookie_bootstrap.go              Cookie 初始化
│   │   ├── init.go                          GetDpl/BasicCookies——消除全局变量
│   │   └── models.go                        内部类型定义
│   │
│   ├── prooftoken/                      ← 保留原样
│   │   └── prooftoken.go
│   │
│   ├── turnstile/                       ← 保留原样
│   │   └── turnstile.go
│   │
│   ├── so/                              ← 保留原样
│   │   └── so.go
│   │
│   ├── toolcall/                        ← 保留原样
│   │   ├── parser.go
│   │   ├── recover.go
│   │   ├── prompt.go
│   │   └── time.go
│   │
│   ├── fingerprint/                     ← 保留原样
│   │   └── fingerprint.go
│   │
│   ├── browserfp/                       ← 保留原样
│   │   ├── browserfp.go
│   │   └── data.go
│   │
│   ├── service/                         ← NEW: 业务编排
│   │   ├── chat_service.go                  ChatCompletion + continue 循环
│   │   ├── image_service.go                 图片生成/编辑/变体
│   │   ├── audio_service.go                 TTS / STT
│   │   ├── toolcall_service.go              工具调用模拟
│   │   └── file_service.go                  文件上传
│   │
│   ├── handler/                         ← NEW: 薄 HTTP 层 + 路由注册
│   │   ├── router.go                        路由注册（原 initialize/router.go）
│   │   ├── chat_handler.go
│   │   ├── image_handler.go
│   │   ├── audio_handler.go
│   │   ├── auth_handler.go
│   │   └── models_handler.go
│   │
│   ├── middleware/
│   │   ├── auth.go                       ← 强化：认证逻辑从 handler 移入
│   │   └── cors.go                       ← 保留
│   │
│   ├── conversion/                       ← 保留（格式转换）
│   │   ├── requests/chatgpt/convert.go
│   │   └── response/chatgpt/convert.go
│   │
│   └── types/                            ← typings → types，规范命名
│       ├── official/request.go
│       ├── official/response.go
│       ├── chatgpt/request.go
│       └── chatgpt/response.go
│
├── api/router.go                         ← 保留（Vercel 入口）
│
├── docs/superpowers/specs/
│
├── Dockerfile, docker-compose.yml, ...   ← 保留
└── README.md, API.md, ...                ← 保留
```

### 2.2 变更对照

| 原路径 | 新路径 | 处理方式 |
|--------|--------|----------|
| `internal/chatgpt/request.go` | `internal/chatgpt/{sentinel,conversation,websocket,sse,init,models}.go` | 拆文件 |
| `internal/chatgpt/artifacts.go` | `internal/chatgpt/artifacts.go` | 原样 |
| `internal/chatgpt/artifact_delivery.go` | `internal/chatgpt/artifact_delivery.go` | 原样 |
| `internal/chatgpt/files.go` | `internal/chatgpt/files.go` | 原样 |
| `internal/chatgpt/transcribe.go` | `internal/chatgpt/transcribe.go` | 原样 |
| `internal/chatgpt/client_state.go` | `internal/chatgpt/client_state.go` | 原样 |
| `internal/chatgpt/cookie_bootstrap.go` | `internal/chatgpt/cookie_bootstrap.go` | 原样 |
| `internal/prooftoken/prooftoken.go` | `internal/prooftoken/prooftoken.go` | 原样 |
| `internal/turnstile/turnstile.go` | `internal/turnstile/turnstile.go` | 原样 |
| `internal/so/so.go` | `internal/so/so.go` | 原样 |
| `internal/toolcall/*` | `internal/toolcall/*` | 原样 |
| `internal/fingerprint/*` | `internal/fingerprint/*` | 原样 |
| `internal/browserfp/*` | `internal/browserfp/*` | 原样 |
| `initialize/handlers.go` | `internal/handler/*.go` | 拆为多个 handler |
| `initialize/auth.go` | `internal/accounts/` | 重写 |
| `initialize/proxy.go` | `internal/proxy/pool.go` | 重写 |
| `initialize/session_manager.go` | `internal/chatgpt/` 或 `internal/service/` | 根据归属决定 |
| `initialize/router.go` | `internal/handler/router.go` | 移入 handler |
| `initialize/` | `internal/handler/` + `internal/accounts/` + `internal/proxy/` | 解散 |
| `internal/tokens/tokens.go` | `internal/accounts/` | 废弃，由 Account 替代 |
| `internal/proxys/proxys.go` | `internal/proxy/` | 废弃，由 ProxyPool 替代 |
| `typings/` | `internal/types/` | 改名 |

---

## 3. 整体架构

```
main.go → 加载 Config → 初始化各层 → 启动 HTTP 服务

┌─────────────────────────────────────────────────────────┐
│  handler 层（薄）                                         │
│  职责：请求/响应编解码，不涉及业务逻辑                       │
├─────────────────────────────────────────────────────────┤
│  service 层（核心业务）                                    │
│  职责：聊天循环、工具调用编排、图片生成等                    │
│  调用 internal/chatgpt/ 的函数，不包含协议细节              │
│  不依赖 gin.Context                                       │
├─────────────────────────────────────────────────────────┤
│  chatgpt 层（ChatGPT 协议封装）                            │
│  职责：逆向工程的协议细节——URL、Header、请求体、响应解析     │
│  接受 httpclient.AuroraHttpClient 作为参数                 │
├─────────────────────────────────────────────────────────┤
│  accounts 层                                              │
│  职责：账号全生命周期管理（创建、健康检查、续期、WSS）        │
├─────────────────────────────────────────────────────────┤
│  config / 基础设施                                         │
│  职责：配置管理、代理池、持久化                              │
└─────────────────────────────────────────────────────────┘
```

### 数据流示例（聊天请求）

```
HTTP POST /v1/chat/completions
  → middleware.Authorization (提取 Bearer Token)
  → handler.ChatHandler.Nightmare()
      → accountPool.Acquire(需要类型)
          → WSSActor goroutine 已在后台运行（连接/保活/重连自动管理）
      → chatService.ChatCompletion(req, account)
          → chatgpt.InitTurnStile(account.Client, account.Secret, account.Proxy)
          → chatgpt.POSTConversationInit(account.Client, account.Secret)
          → chatgpt.POSTConversation(translated, account.Secret, turnStile, account.Proxy)
          → chatgpt.HandlerDetailed(response, ...)  // SSE/WS 解析 + continue
      → accountPool.Release(account, err)
  → 序列化为 SSE/JSON 响应
```

---

## 4. 账号管理

### 4.1 账号类型

| 类型 | 凭证 | WSS | 能力限制 | 来源 |
|------|------|-----|----------|------|
| noauth | 设备 UUID | 无 | 仅聊天 | `free_tokens.txt` / `FREE_ACCOUNTS` |
| free | ChatGPT 免费账号 access_token | 有 | 聊天 + 部分功能 | `free_tokens.txt`（带登录态） |
| puid | ChatGPT 付费 access_token + PUID | 有 | 全部功能 | `access_tokens.txt` |

账号类型与能力的映射关系集中在 `internal/accounts/capabilities.go`，当 ChatGPT 策略变化时只改此文件。

### 4.2 Account 结构体

```go
type Account struct {
    ID            string
    Type          AccountType

    // 认证
    Token         string           // access_token 或 UUID
    RefreshToken  string           // 仅 free/puid 有

    // 身份
    PUID          string
    TeamUserID    string

    // 隔离单元（每个账号独立）
    Client        *bogdanfinn.TlsClient   // 专属 TLS client + cookie jar
    Proxy         string                  // 专属代理 IP
    Fingerprint   BrowserFingerprint      // 专属指纹 (DeviceID/SessionID/UA/视窗)

    // WSS Actor (free/puid 有, noauth 无)
    WSSActor      *WSSActor           // 一个 goroutine 管理连接/发送/保活/重连

    // 状态
    Status        AccountStatus   // active / expired / rate_limited / banned / pending
    ExpiresAt     time.Time

    // 统计
    TotalCalls    int64
    FailedCalls   int64
    LastUsed      time.Time
    LastChecked   time.Time        // 上次健康检查时间
    CreatedAt     time.Time
}
```

### 4.3 账号池 (AccountPool)

```go
type AccountPool struct {
    accounts []*Account
}

Acquire(type AccountType) → (*Account, error)
Release(acct *Account, result error)
```

**Acquire 策略：**
1. 从 accounts 中找 `Status == active` 且 `Type` 匹配的账号
2. round-robin 轮换
3. 检查 WSS 健康（free/puid）
4. 返回账号

**Release 策略（根据错误调整状态）：**
- 成功 → 更新统计
- 401 → 尝试 refresh_token 续期
  - 成功 → 更新 token，标记 active
  - 失败 → 标记 expired
- 403/429 → 标记 rate_limited，30 分钟后自动恢复
- WSS 断开 → 触发重连
- 连续 N 次失败 → 标记 disabled

### 4.4 健康检查 & 自动续期

后台协程（每 5-10 分钟执行一次）：

```
for each account:
  轻量探活 (HEAD /v1/models 或等效请求)
    ├─ 成功 → active
    ├─ 401 → 尝试 refresh_token 续期
    │   ├─ 成功 → 更新 token, active
    │   └─ 失败 → expired
    ├─ 403/429 → rate_limited
    └─ WSS 断 → 重连
rate_limited 冷却超时 → 重新探活
```

### 4.5 持久化

- 格式：JSON（初期），接口预留扩展（SQLite 等）
- 文件：`accounts.json`
- 触发时机：健康检查/状态变更 + 每 30s 定时保存

```go
type AccountStore interface {
    Load() ([]*Account, error)
    Save(accounts []*Account) error
}
```

---

## 5. 指纹画像 (Fingerprint Profiles)

硬编码 8 个自洽指纹画像，TLS 指纹、UA、视窗各维度绑定成一套：

| 画像名 | TLS Profile | UA | 视窗 | 核数 | 平台 |
|--------|-------------|-----|------|------|------|
| `chrome_win_high` | Chrome_146 | Win 10 Chrome 146 | 2560×1440 | 16 | Windows |
| `chrome_win_medium` | Chrome_146 | Win 10 Chrome 146 | 1920×1080 | 8 | Windows |
| `chrome_win_low` | Chrome_146 | Win 10 Chrome 146 | 1366×768 | 4 | Windows |
| `chrome_mac` | Chrome_146 | macOS Chrome 146 | 3024×1964 | 12 | macOS |
| `safari_mac` | Safari_16_0 | macOS Safari 16 | 3024×1964 | 10 | macOS |
| `safari_iphone_pro` | iOS_18_5 | iPhone 16 Pro iOS 18 | 393×852 | 6 | iOS |
| `safari_iphone` | iOS_17_0 | iPhone 15 iOS 17 | 390×844 | 6 | iOS |
| `safari_ipad` | iPad_15_6 | iPad Pro iPadOS 17 | 1024×1366 | 8 | iPadOS |

每个账号创建时随机分配一个画像，`oaiDeviceID` 和 `oaiSessionID` 随机生成，其他参数由画像决定。

---

## 6. WSS 管理

WSS 绑定在 `Account` 上（仅 free/puid），采用 **actor goroutine 模式**——一个 goroutine 统一负责连接的建立、发送、保活和重连。Service 层只需通过 channel 向这个 goroutine 发送指令。

### WSSActor 接口

```go
// wssCommand service 层向 WSS goroutine 发送的指令
type wssCommand struct {
    Type    string      // "subscribe", "close"
    Payload interface{}
    Result  chan<- wssResult
}

type wssResult struct {
    Data []byte
    Err  error
}

// WSSActor 每个 free/puid 账号持有一个
// 一个 goroutine 统管连接生命周期
type WSSActor struct {
    account  *Account
    commands chan wssCommand  // service 层通过这里发指令
    done     chan struct{}
}

func NewWSSActor(account *Account) *WSSActor
func (a *WSSActor) Start()                    // 启动 goroutine
func (a *WSSActor) Stop()                     // 停止 goroutine
func (a *WSSActor) Subscribe(topicID string) error  // 发指令到 channel
```

### Goroutine 内部循环

```
WSSActor.Start()
  │
  └→ goroutine: run()
       │
       │  建立连接（阻塞直到成功）:
       │  ├── GET /celsius/ws/user → 获取 ws_url
       │  ├── Dial(wss://...)
       │  └── 发送 4 条 init 消息（前置消息）:
       │        [{id:1, command:{type:"connect", presence:{...}}},
       │         {id:2, command:{type:"subscribe", topic_id:"calpico-chatgpt"}},
       │         {id:3, command:{type:"subscribe", topic_id:"conversations"}},
       │         {id:4, command:{type:"subscribe", topic_id:"app_notifications"}}]
       │
       ├→ 启动读 goroutine: 不断 ReadMessage，结果发回主循环
       │
       └→ select 主循环:
            ├── cmd := <-actor.commands
            │     ├── "subscribe" → conn.WriteJSON(subscribeMsg)
            │     └── "close"     → 退出
            │
            ├── data := <-readCh
            │     └── WSS 帧回调 / 数据分发
            │
            ├── <-ticker.C
            │     └── TODO: 浏览器端保活 ping 参数待抓包确认
            │
            ├── <-断开信号
            │     └── 重连:
            │           ├── 关闭旧 conn
            │           ├── 指数退避 (1s→2s→4s→...→30s max)
            │           ├── 用 account.Client 重新 GET /celsius/ws/user
            │           ├── 重新 Dial
            │           └── 重新发 4 条 init
            │
            └── <-actor.done → 清理退出
```

### 关键原则

- **一个 WSS 连接 = 一个 goroutine**，不分散为多个方法
- **Service 层只通过 channel 发指令**，不直接操作 conn
- **重连时用 account.Client 获取全新的 ws_url**（ChatGPT 每次返回可能不同）
- **noauth 账号没有 WSSActor**
- TODO: 浏览器端保活 ping 的具体频率和格式待补充，占位 ticker 已预留

---

## 7. Service / Handler 层分解

### Service 层（业务编排）

Service 层负责编排 `internal/chatgpt/` 的函数调用顺序，不包含协议细节：

```go
internal/service/
├── chat_service.go          // ChatCompletion + continue 循环
├── image_service.go         // GenerateImages / Edit / Variation
├── audio_service.go         // TTS / Transcribe
├── toolcall_service.go      // 工具调用模拟（注入 prompt → 解析 <tool_call> → 重试）
└── file_service.go          // 文件上传
```

Service 不依赖 `gin.Context`，接收 `*Account` 作为上下文，可单元测试。

### Handler 层

```go
internal/handler/
├── chat_handler.go          // BindJSON → service → SSE/JSON 响应
├── image_handler.go
├── audio_handler.go
├── auth_handler.go          // refresh / session
└── models_handler.go        // 模型列表
```

每个 Handler 方法不超过 50 行。

---

## 8. 配置管理

消除 `os.Getenv()` 散落，统一在 `internal/config/config.go` 加载：

```go
type Config struct {
    ServerHost        string
    ServerPort        string
    TLSCert           string
    TLSKey            string
    Authorization     string
    BaseURL           string
    APIReverseProxy   string
    FilesReverseProxy string
    StreamMode        bool
    MaxContinueCount  int
    EnableHistory     bool
    ToolCallingEnabled bool
    RefusalRetries    int
    DebugToolLog      string
    FreeAccounts      bool
    FreeAccountsNum   int
    ProxyURL          string
    HTTPProxy         string
    DebugSentinel     bool
}

func Load() Config  // 从环境变量读取，所有默认值集中在这里
```

各层通过函数参数或结构体字段接收 Config，不再直接读取环境变量。

---

## 9. 代理池 (ProxyPool)

独立于 AccountPool，职责单一：

```go
type ProxyPool struct {
    ipv4Proxies []string    // proxies.txt / PROXY_URL
    ipv6CIDR    string      // 可选 IPv6 子网
    assignments map[string]string // accountID → proxy
}

Allocate() → (string, error)  // 分配一个代理
Release(proxy string)         // 回收代理
Count() int                   // 可用代理数
```

**分配策略：**
- IPv6 模式：从 CIDR 自动生成独立 IP，通过自定义 Dialer 绑定源 IP
- IPv4 模式：从代理列表分配
- 优先 1:1，不够时降级共享

---

## 10. 实施建议

建议分 6 个阶段实施，每个阶段功能可独立验证：

| 阶段 | 内容 | 影响范围 |
|------|------|----------|
| 1 | Config 集中管理 + 目录结构调整 | 全局，但不改逻辑 |
| 2 | Account 模型 + 持久化 + ProxyPool | accounts/ + proxy/ |
| 3 | 指纹画像 + Account 初始化 | accounts/ |
| 4 | WSSActor（goroutine 模式） | accounts/ |
| 5 | chatgpt/request.go 拆文件 | chatgpt/ |
| 6 | Service 层 + Handler 层瘦身 | service/ + handler/ |
