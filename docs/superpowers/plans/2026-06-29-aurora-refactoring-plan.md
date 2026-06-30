# Aurora 重构实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 aurora-develop/aurora 重构为清洁分层架构，账号系统全面升级，保持 100% 功能兼容

**Architecture:** `Handler → Service → Accounts/ChatGPT` 三层，每个账号自包含 TLS Client、Proxy、指纹和 WSS goroutine

**Tech Stack:** Go 1.26, Gin, bogdanfinn/tls-client, bogdanfinn/websocket, TDD

## Global Constraints

- 所有上游源文件先 1:1 复制到新项目，不改动原逻辑再逐步替换
- TDD：先写测试（验证现有行为），再实现，再验证通过
- 改代码前必须深度理解原逻辑：每个模块先写理解笔记
- `internal/chatgpt/` 核心逆向代码只拆文件，不删改算法逻辑
- `internal/` 的 prooftoken、turnstile、so、toolcall、fingerprint、browserfp 保持原样
- accounts 模块完美替代 tokens 模块，接口无缝对接
- `initialize/` 整个包解散，路由移到 `internal/handler/router.go`
- WSS 保活 ping 参数标记 TODO，只做基础 goroutine 框架
- IPv6 模式标记 TODO，先做 IPv4

---

## File Structure

### 创建的文件

```
internal/
├── config/
│   └── config.go                  ← 集中配置管理
│
├── accounts/
│   ├── account.go                 ← Account 结构体 + 生命周期方法
│   ├── account_test.go
│   ├── pool.go                    ← AccountPool (Acquire/Release)
│   ├── pool_test.go
│   ├── store.go                   ← JSON 持久化
│   ├── store_test.go
│   ├── capabilities.go           ← 账号能力映射
│   ├── capabilities_test.go
│   ├── fingerprint_profiles.go   ← 8 个指纹画像
│   └── wss_actor.go              ← WSS goroutine (TODO 保活参数)
│
├── proxy/
│   ├── pool.go                    ← ProxyPool (IPv4, IPv6 TODO)
│   └── pool_test.go
│
├── service/
│   ├── chat_service.go
│   ├── chat_service_test.go
│   ├── image_service.go
│   ├── image_service_test.go
│   ├── audio_service.go
│   ├── toolcall_service.go
│   └── file_service.go
│
├── handler/
│   ├── router.go                  ← 原 initialize/router.go
│   ├── chat_handler.go
│   ├── image_handler.go
│   ├── audio_handler.go
│   ├── auth_handler.go
│   └── models_handler.go
│
├── middleware/
│   └── auth.go                    ← 原 initialize/auth.go 认证逻辑
```

### 保留原样的文件

```
internal/prooftoken/prooftoken.go        ← 原样
internal/turnstile/turnstile.go           ← 原样
internal/so/so.go                         ← 原样
internal/toolcall/*                        ← 原样
internal/fingerprint/fingerprint.go       ← 原样
internal/browserfp/*                       ← 原样
internal/conversion/*                      ← 原样
```

### 拆分的文件 (chatgpt/)

```
internal/chatgpt/request.go (3416 行)
  → 拆为 (逻辑拆分，每行代码不变):
    sentinel.go              ← sentinel prepare/finalize/ping
    conversation.go          ← conversation init/conduit/POST
    websocket.go             ← WSS URL 获取 + Dial + init + 流读取
    sse.go                   ← SSE 解析 + handoff topic
    artifacts.go             ← 代码产物 (原样)
    artifact_delivery.go     ← 产物流式推送 (原样)
    files.go                 ← 文件上传 (原样)
    transcribe.go            ← 音频转写 (原样)
    client_state.go          ← ChatClientState (原样)
    cookie_bootstrap.go      ← Cookie 初始化 (原样)
    init.go                  ← GetDpl/BasicCookies (消除全局变量)
    models.go                ← 内部类型定义
```

### 删除/废弃的文件（重构完成后）

```
initialize/                 ← 整个包解散
internal/tokens/            ← 由 accounts/ 替代
internal/proxys/            ← 由 proxy/ 替代
typings/                    ← 改名为 internal/types/
```

---

## Implementation Plan

### Task 0: 项目基线建立 — 1:1 复制 + 确认编译通过

**Files:**
- Copy: `D:/lingoox_workspace/aurora_upstream/*` → `D:/lingoox_workspace/new_aurora/`
- No modifications

**理解笔记（不写进代码，只存档参考）：**
- 项目总 16784 行 Go，47 个 .go 文件
- 12 个已有测试文件
- `internal/chatgpt/request.go` 3416 行是最大文件
- `initialize/handlers.go` 2464 行是第二大

- [ ] **Step 1: 1:1 复制上游所有文件**

```bash
# 复制所有文件（包括 .git）
cp -r D:/lingoox_workspace/aurora_upstream/* D:/lingoox_workspace/new_aurora/
cp D:/lingoox_workspace/aurora_upstream/.gitignore D:/lingoox_workspace/new_aurora/
```

- [ ] **Step 2: 确认编译通过**

```bash
cd D:/lingoox_workspace/new_aurora
go build -o aurora.exe ./...
echo "编译成功"
```

- [ ] **Step 3: 确认所有已有测试通过**

```bash
cd D:/lingoox_workspace/new_aurora
go test ./... 2>&1
```

Expected: 所有测试 PASS（或 SKIP，不能有 FAIL）

- [ ] **Step 4: 创建新目录结构（空壳）**

```bash
mkdir -p internal/config internal/accounts internal/proxy internal/service internal/handler internal/middleware
```

- [ ] **Step 5: 提交基线**

```bash
git add -A
git commit -m "chore: 1:1 copy upstream aurora-develop/aurora@HEAD"
```

---

### Task 1: Config 模块 (TDD)

**前置依赖:** Task 0

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

**理解笔记:**
- 当前 `os.Getenv()` 散布在 main.go、initialize/handlers.go、initialize/auth.go、initialize/proxy.go、internal/chatgpt/request.go、middlewares/auth.go
- 共约 15-20 个环境变量
- 每个变量都有隐式默认值（如 SERVER_PORT 默认 8080，STREAM_MODE 默认 true）

**Interfaces:**
- Consumes: 无
- Produces: `config.Load() Config`, `Config` struct with all fields

- [ ] **Step 1: 写失败的测试 —— 测试 Config 加载默认值**

```go
// internal/config/config_test.go
package config

import (
    "os"
    "testing"
)

func TestLoadDefaults(t *testing.T) {
    // 清除所有相关环境变量
    for _, key := range []string{"SERVER_HOST", "SERVER_PORT", "PORT", "STREAM_MODE", "MAX_CONTINUE_COUNT"} {
        os.Unsetenv(key)
    }

    cfg := Load()

    if cfg.ServerHost != "0.0.0.0" {
        t.Errorf("ServerHost = %q, want %q", cfg.ServerHost, "0.0.0.0")
    }
    if cfg.ServerPort != "8080" {
        t.Errorf("ServerPort = %q, want %q", cfg.ServerPort, "8080")
    }
    if !cfg.StreamMode {
        t.Error("StreamMode = false, want true")
    }
    if cfg.MaxContinueCount != 3 {
        t.Errorf("MaxContinueCount = %d, want 3", cfg.MaxContinueCount)
    }
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
cd D:/lingoox_workspace/new_aurora
go test ./internal/config/ -v
```
Expected: FAIL (package config not found)

- [ ] **Step 3: 实现 Config 结构体**

```go
// internal/config/config.go
package config

import (
    "os"
    "strconv"
)

type Config struct {
    ServerHost         string
    ServerPort         string
    TLSCert            string
    TLSKey             string
    Authorization      string
    BaseURL            string
    APIReverseProxy    string
    FilesReverseProxy  string
    StreamMode         bool
    MaxContinueCount   int
    EnableHistory      bool
    ToolCallingEnabled bool
    RefusalRetries     int
    DebugToolLog       string
    FreeAccounts       bool
    FreeAccountsNum    int
    ProxyURL           string
    HTTPProxy          string
    DebugSentinel      bool
}

func Load() Config {
    return Config{
        ServerHost:         getEnv("SERVER_HOST", "0.0.0.0"),
        ServerPort:         getEnvWithFallback("SERVER_PORT", "PORT", "8080"),
        TLSCert:            os.Getenv("TLS_CERT"),
        TLSKey:             os.Getenv("TLS_KEY"),
        Authorization:      os.Getenv("Authorization"),
        BaseURL:            getEnv("BASE_URL", "https://chatgpt.com/backend-api"),
        APIReverseProxy:    os.Getenv("API_REVERSE_PROXY"),
        FilesReverseProxy:  os.Getenv("FILES_REVERSE_PROXY"),
        StreamMode:         getBoolEnv("STREAM_MODE", true),
        MaxContinueCount:   getIntEnv("MAX_CONTINUE_COUNT", 3),
        EnableHistory:      getBoolEnv("ENABLE_HISTORY", false),
        ToolCallingEnabled: getBoolEnv("TOOL_CALLING_ENABLED", true),
        RefusalRetries:     getIntEnv("REFUSAL_RETRIES", 3),
        DebugToolLog:       os.Getenv("DEBUG_TOOL_LOG"),
        FreeAccounts:       getBoolEnv("FREE_ACCOUNTS", false),
        FreeAccountsNum:    getIntEnv("FREE_ACCOUNTS_NUM", 1024),
        ProxyURL:           os.Getenv("PROXY_URL"),
        HTTPProxy:          os.Getenv("http_proxy"),
        DebugSentinel:      getBoolEnv("DEBUG_SENTINEL", false),
    }
}

func getEnv(key, defaultVal string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return defaultVal
}

func getEnvWithFallback(key, fallbackKey, defaultVal string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    if v := os.Getenv(fallbackKey); v != "" {
        return v
    }
    return defaultVal
}

func getBoolEnv(key string, defaultVal bool) bool {
    v := os.Getenv(key)
    if v == "" {
        return defaultVal
    }
    b, err := strconv.ParseBool(v)
    if err != nil {
        return defaultVal
    }
    return b
}

func getIntEnv(key string, defaultVal int) int {
    v := os.Getenv(key)
    if v == "" {
        return defaultVal
    }
    n, err := strconv.Atoi(v)
    if err != nil || n < 0 {
        return defaultVal
    }
    return n
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
cd D:/lingoox_workspace/new_aurora
go test ./internal/config/ -v
```
Expected: PASS

- [ ] **Step 5: 写更多测试 —— 环境变量覆盖**

```go
func TestLoadWithEnv(t *testing.T) {
    os.Setenv("SERVER_HOST", "127.0.0.1")
    os.Setenv("SERVER_PORT", "9090")
    os.Setenv("STREAM_MODE", "false")
    defer func() {
        os.Unsetenv("SERVER_HOST")
        os.Unsetenv("SERVER_PORT")
        os.Unsetenv("STREAM_MODE")
    }()

    cfg := Load()

    if cfg.ServerHost != "127.0.0.1" {
        t.Errorf("ServerHost = %q, want %q", cfg.ServerHost, "127.0.0.1")
    }
    if cfg.ServerPort != "9090" {
        t.Errorf("ServerPort = %q, want %q", cfg.ServerPort, "9090")
    }
    if cfg.StreamMode {
        t.Error("StreamMode = true, want false")
    }
}

func TestGetBoolEnvInvalid(t *testing.T) {
    os.Setenv("TEST_BOOL", "notabool")
    defer os.Unsetenv("TEST_BOOL")

    result := getBoolEnv("TEST_BOOL", true)
    if !result {
        t.Error("getBoolEnv with invalid value should return default (true)")
    }
}
```

- [ ] **Step 6: 确认所有测试通过 + 提交**

```bash
cd D:/lingoox_workspace/new_aurora
go test ./internal/config/ -v
```
Expected: PASS

```bash
git add internal/config/
git commit -m "feat: add Config module with centralized env loading"
```

---

### Task 2: Proxy 模块 (TDD)

**前置依赖:** Task 1 (使用 Config)

**Files:**
- Create: `internal/proxy/pool.go`
- Create: `internal/proxy/pool_test.go`

**理解笔记:**
- 当前 `internal/proxys/proxys.go` 实现一个简单的 round-robin `[]string` 环形队列
- `initialize/proxy.go` 从 proxies.txt / PROXY_URL / http_proxy 读取代理列表
- 代理池只做两件事：存列表、轮询取一个
- IPv4 代理（`proxies.txt`）和 IPv6 子网（代码配置）需要共存

**Interfaces:**
- Consumes: `config.Config` (ProxyURL, HTTPProxy)
- Produces: `proxy.Pool` with `Allocate()`, `Release(ip)`, `Count()`

- [ ] **Step 1: 写失败的测试**

```go
// internal/proxy/pool_test.go
package proxy

import (
    "testing"
)

func TestPoolAllocateAndRelease(t *testing.T) {
    p := NewPool([]string{"http://proxy1:8080", "http://proxy2:8080", "http://proxy3:8080"}, "")

    // 轮询分配
    ip1 := p.Allocate()
    ip2 := p.Allocate()
    ip3 := p.Allocate()
    ip4 := p.Allocate() // 应该回到第一个

    if ip1 == ip2 || ip2 == ip3 || ip1 == ip4 {
        t.Errorf("round-robin should cycle: got %q, %q, %q, %q", ip1, ip2, ip3, ip4)
    }
    if ip1 != ip4 {
        t.Errorf("4th allocate should be same as 1st: got %q, want %q", ip4, ip1)
    }
}

func TestPoolEmpty(t *testing.T) {
    p := NewPool(nil, "")
    ip := p.Allocate()
    if ip != "" {
        t.Errorf("empty pool should return empty string, got %q", ip)
    }
}

func TestPoolCount(t *testing.T) {
    p := NewPool([]string{"a", "b", "c"}, "")
    if p.Count() != 3 {
        t.Errorf("Count = %d, want 3", p.Count())
    }
}
```

- [ ] **Step 2: 确认测试失败**

```bash
cd D:/lingoox_workspace/new_aurora
go test ./internal/proxy/ -v
```
Expected: FAIL

- [ ] **Step 3: 实现 ProxyPool**

```go
// internal/proxy/pool.go
package proxy

import (
    "net"
    "sync"
)

// Pool 管理代理 IP 的分配与回收。
// IPv4 从代理列表轮询，IPv6 从 CIDR 自动生成（TODO: IPv6 模式）。
type Pool struct {
    mu        sync.Mutex
    ipv4List  []string
    ipv6CIDR  string
    cursor    int
}

func NewPool(ipv4Proxies []string, ipv6CIDR string) *Pool {
    if ipv4Proxies == nil {
        ipv4Proxies = []string{}
    }
    return &Pool{
        ipv4List: ipv4Proxies,
        ipv6CIDR: ipv6CIDR,
    }
}

// Allocate 返回一个代理 IP。
// IPv6 模式（TODO）：从 CIDR 生成唯一 IP。
// IPv4 模式：从现有列表 round-robin。
func (p *Pool) Allocate() string {
    p.mu.Lock()
    defer p.mu.Unlock()

    if p.ipv6CIDR != "" {
        // TODO: IPv6 模式 — 从 CIDR 生成独立 IP
        return ""
    }

    if len(p.ipv4List) == 0 {
        return ""
    }

    ip := p.ipv4List[p.cursor]
    p.cursor = (p.cursor + 1) % len(p.ipv4List)
    return ip
}

// Release 回收一个代理 IP（ipv4 模式无需实际操作）。
// ipv6 模式（TODO）需要释放地址回池。
func (p *Pool) Release(ip string) {
    // IPv4: 无操作（列表可重复使用）
    // IPv6 (TODO): 回收地址
}

// Count 返回可用代理数量。
func (p *Pool) Count() int {
    p.mu.Lock()
    defer p.mu.Unlock()
    return len(p.ipv4List)
}

// IsIPv6 判断是否为 IPv6 地址。
func IsIPv6(ip string) bool {
    parsed := net.ParseIP(ip)
    if parsed == nil {
        return false
    }
    return parsed.To4() == nil
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
cd D:/lingoox_workspace/new_aurora
go test ./internal/proxy/ -v
```
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/proxy/
git commit -m "feat: add ProxyPool with IPv4 round-robin and IPv6 placeholder"
```

---

### Task 3: Accounts 模块 — 核心模型与持久化 (TDD)

**前置依赖:** Task 1 (Config), Task 2 (Proxy)

**Files:**
- Create: `internal/accounts/account.go`
- Create: `internal/accounts/account_test.go`
- Create: `internal/accounts/store.go`
- Create: `internal/accounts/store_test.go`
- Create: `internal/accounts/capabilities.go`
- Create: `internal/accounts/capabilities_test.go`
- Create: `internal/accounts/fingerprint_profiles.go`
- Create: `internal/accounts/fingerprint_profiles_test.go`

**理解笔记:**
- 当前 `internal/tokens/tokens.go` 管理 `[]*Secret` 的环形队列
- `Secret` 结构：`{Token, PUID, TeamUserID, IsFree, Disabled}`
- `initialize/auth.go` 从 `access_tokens.txt` 和 `free_tokens.txt` 加载
- `FREE_ACCOUNTS=true` 时自动生成 UUID 账号
- 当前缺陷：无持久化、无健康检查、指纹全局共享、WSS 临时创建

**Interfaces:**
- Consumes: `config.Config`, `proxy.Pool`
- Produces: `accounts.Account`, `accounts.Pool`, `accounts.Store`, `accounts.Capabilities`

- [ ] **Step 1: 写 Account 模型测试**

```go
// internal/accounts/account_test.go
package accounts

import (
    "testing"
    "time"
)

func TestAccountTypeStrings(t *testing.T) {
    if TypeNoAuth.String() != "noauth" {
        t.Errorf("TypeNoAuth = %q, want %q", TypeNoAuth.String(), "noauth")
    }
    if TypeFree.String() != "free" {
        t.Errorf("TypeFree = %q, want %q", TypeFree.String(), "free")
    }
    if TypePUID.String() != "puid" {
        t.Errorf("TypePUID = %q, want %q", TypePUID.String(), "puid")
    }
}

func TestAccountStatusStrings(t *testing.T) {
    if StatusActive.String() != "active" {
        t.Errorf("StatusActive = %q, want %q", StatusActive.String(), "active")
    }
}
```

- [ ] **Step 2: 实现 Account 基础类型**

```go
// internal/accounts/account.go
package accounts

import (
    "fmt"
    "time"
)

// AccountType 账号类型
type AccountType int

const (
    TypeNoAuth AccountType = iota // 匿名设备 UUID
    TypeFree                      // ChatGPT 免费登录账号
    TypePUID                      // ChatGPT 付费/PRO 账号
)

func (t AccountType) String() string {
    switch t {
    case TypeNoAuth:
        return "noauth"
    case TypeFree:
        return "free"
    case TypePUID:
        return "puid"
    }
    return fmt.Sprintf("unknown(%d)", t)
}

// AccountStatus 账号生命周期状态
type AccountStatus int

const (
    StatusPending    AccountStatus = iota // 初始化中
    StatusActive                          // 正常
    StatusExpired                         // Token 过期，可续期
    StatusRateLimited                     // 被限流，等待冷却
    StatusDisabled                        // 手动停用
    StatusBanned                          // 被封禁
)

func (s AccountStatus) String() string {
    switch s {
    case StatusPending:
        return "pending"
    case StatusActive:
        return "active"
    case StatusExpired:
        return "expired"
    case StatusRateLimited:
        return "rate_limited"
    case StatusDisabled:
        return "disabled"
    case StatusBanned:
        return "banned"
    }
    return fmt.Sprintf("unknown(%d)", s)
}

// Account 一个账号 = 完整隔离单元
// 每个账号拥有独立的 TLS Client、代理 IP、浏览器指纹和 WSS 连接
type Account struct {
    ID           string
    Type         AccountType

    // 认证
    Token        string    // access_token 或 UUID
    RefreshToken string    // 仅 free/puid 有

    // 身份
    PUID         string
    TeamUserID   string

    // 隔离单元（每个账号独立）
    Client       interface{}         // *bogdanfinn.TlsClient — 避免 import 循环
    Proxy        string              // 专属代理 IP
    Fingerprint  BrowserFingerprint  // 专属指纹

    // WSS (free/puid 有, noauth 无)
    WSSActor     interface{}         // *WSSActor — 避免 import 循环

    // 状态
    Status       AccountStatus
    ExpiresAt    time.Time

    // 统计
    TotalCalls   int64
    FailedCalls  int64
    LastUsed     time.Time
    LastChecked  time.Time
    CreatedAt    time.Time
}

// BrowserFingerprint 浏览器指纹（所有参数自洽配套）
type BrowserFingerprint struct {
    OaiDeviceID         string
    OaiSessionID        string
    UserAgent           string
    ScreenWidth         int
    ScreenHeight        int
    HardwareConcurrency int
    Platform            string
    TLSProfileName      string // 对应 bogdanfinn profiles 名称
}

// NewAccount 创建新账号
func NewAccount(id string, acctType AccountType, token string) *Account {
    now := time.Now()
    return &Account{
        ID:        id,
        Type:      acctType,
        Token:     token,
        Status:    StatusPending,
        CreatedAt: now,
    }
}
```

- [ ] **Step 3: 运行测试确认通过**

```bash
cd D:/lingoox_workspace/new_aurora
go test ./internal/accounts/ -v -run TestAccount
```
Expected: PASS

- [ ] **Step 4: 实现 Capabilities**

```go
// internal/accounts/capabilities.go
package accounts

// Capability 表示一个功能对账号类型的要求
// ★ 当 ChatGPT 策略变化时，只改这个文件的对应常量 ★
type Capability struct {
    Name          string
    RequiresPUID  bool // 需要付费账号
    RequiresLogin bool // 需要登录（free 或 puid）
}

// 系统内所有功能及其当前账号要求
var (
    CapChat           = Capability{Name: "chat"}
    CapResponses      = Capability{Name: "responses"}
    CapToolCalling    = Capability{Name: "tool_calling"}
    CapImageGenerate  = Capability{Name: "image_generation", RequiresPUID: true}
    CapImageEdit      = Capability{Name: "image_edit", RequiresPUID: true}
    CapImageVariation = Capability{Name: "image_variation", RequiresPUID: true}
    CapTTS            = Capability{Name: "tts", RequiresPUID: true}
    CapTranscribe     = Capability{Name: "transcribe", RequiresPUID: true}
    CapFileUpload     = Capability{Name: "file_upload", RequiresPUID: true}
)

// Satisfies 判断账号类型是否满足某项能力要求
func (t AccountType) Satisfies(cap Capability) bool {
    switch t {
    case TypePUID:
        return true
    case TypeFree:
        return !cap.RequiresPUID
    case TypeNoAuth:
        return !cap.RequiresPUID && !cap.RequiresLogin
    default:
        return false
    }
}
```

- [ ] **Step 5: 写 Capabilities 测试**

```go
func TestAccountTypeSatisfies(t *testing.T) {
    tests := []struct {
        acctType AccountType
        cap      Capability
        want     bool
    }{
        {TypePUID, CapChat, true},
        {TypePUID, CapImageGenerate, true},
        {TypePUID, CapTTS, true},
        {TypeFree, CapChat, true},
        {TypeFree, CapImageGenerate, false},
        {TypeFree, CapTTS, false},
        {TypeNoAuth, CapChat, true},
        {TypeNoAuth, CapImageGenerate, false},
        {TypeNoAuth, CapTTS, false},
    }

    for _, tt := range tests {
        got := tt.acctType.Satisfies(tt.cap)
        if got != tt.want {
            t.Errorf("%s.Satisfies(%s) = %v, want %v", tt.acctType, tt.cap.Name, got, tt.want)
        }
    }
}
```

- [ ] **Step 6: 实现 Store（JSON 持久化）**

```go
// internal/accounts/store.go
package accounts

import (
    "encoding/json"
    "os"
    "sync"
)

// Store 定义账号持久化接口
type Store interface {
    Load() ([]*Account, error)
    Save(accounts []*Account) error
}

// JSONStore 实现 JSON 文件持久化
type JSONStore struct {
    path string
    mu   sync.Mutex
}

func NewJSONStore(path string) *JSONStore {
    return &JSONStore{path: path}
}

func (s *JSONStore) Load() ([]*Account, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    data, err := os.ReadFile(s.path)
    if err != nil {
        if os.IsNotExist(err) {
            return []*Account{}, nil
        }
        return nil, err
    }

    var accounts []*Account
    if err := json.Unmarshal(data, &accounts); err != nil {
        return nil, err
    }
    if accounts == nil {
        accounts = []*Account{}
    }
    return accounts, nil
}

func (s *JSONStore) Save(accounts []*Account) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    data, err := json.MarshalIndent(accounts, "", "  ")
    if err != nil {
        return err
    }

    return os.WriteFile(s.path, data, 0644)
}
```

- [ ] **Step 7: 写 Store 测试**

```go
// internal/accounts/store_test.go
package accounts

import (
    "os"
    "testing"
)

func TestJSONStoreSaveAndLoad(t *testing.T) {
    path := "_test_accounts.json"
    defer os.Remove(path)

    store := NewJSONStore(path)

    // 加载空文件
    accounts, err := store.Load()
    if err != nil {
        t.Fatalf("Load empty: %v", err)
    }
    if len(accounts) != 0 {
        t.Fatalf("expected empty list, got %d accounts", len(accounts))
    }

    // 保存
    acct := NewAccount("test-1", TypePUID, "test-token")
    acct.Status = StatusActive
    if err := store.Save([]*Account{acct}); err != nil {
        t.Fatalf("Save: %v", err)
    }

    // 重新加载
    loaded, err := store.Load()
    if err != nil {
        t.Fatalf("Load after save: %v", err)
    }
    if len(loaded) != 1 || loaded[0].ID != "test-1" || loaded[0].Token != "test-token" {
        t.Errorf("Load mismatch: got %+v", loaded[0])
    }
}
```

- [ ] **Step 8: 实现指纹画像**

```go
// internal/accounts/fingerprint_profiles.go
package accounts

// FingerprintProfile 一个自洽的指纹画像
type FingerprintProfile struct {
    Name                string
    TLSProfileName      string
    UserAgent           string
    ScreenWidth         int
    ScreenHeight        int
    HardwareConcurrency int
    Platform            string
}

// DefaultProfiles 预定义的 8 个自洽指纹画像
// TLS 指纹、UA、视窗各维度绑定成一套
var DefaultProfiles = []FingerprintProfile{
    {
        Name: "chrome_win_high", TLSProfileName: "chrome_146",
        UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36",
        ScreenWidth: 2560, ScreenHeight: 1440, HardwareConcurrency: 16, Platform: "Win32",
    },
    {
        Name: "chrome_win_medium", TLSProfileName: "chrome_146",
        UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36",
        ScreenWidth: 1920, ScreenHeight: 1080, HardwareConcurrency: 8, Platform: "Win32",
    },
    {
        Name: "chrome_win_low", TLSProfileName: "chrome_146",
        UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36",
        ScreenWidth: 1366, ScreenHeight: 768, HardwareConcurrency: 4, Platform: "Win32",
    },
    {
        Name: "chrome_mac", TLSProfileName: "chrome_146",
        UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36",
        ScreenWidth: 3024, ScreenHeight: 1964, HardwareConcurrency: 12, Platform: "MacIntel",
    },
    {
        Name: "safari_mac", TLSProfileName: "safari_16_0",
        UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Safari/605.1.15",
        ScreenWidth: 3024, ScreenHeight: 1964, HardwareConcurrency: 10, Platform: "MacIntel",
    },
    {
        Name: "safari_iphone_pro", TLSProfileName: "safari_ios_18_5",
        UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Mobile/15E148 Safari/604.1",
        ScreenWidth: 393, ScreenHeight: 852, HardwareConcurrency: 6, Platform: "iPhone",
    },
    {
        Name: "safari_iphone", TLSProfileName: "safari_ios_17_0",
        UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
        ScreenWidth: 390, ScreenHeight: 844, HardwareConcurrency: 6, Platform: "iPhone",
    },
    {
        Name: "safari_ipad", TLSProfileName: "safari_ipad_15_6",
        UserAgent: "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
        ScreenWidth: 1024, ScreenHeight: 1366, HardwareConcurrency: 8, Platform: "iPad",
    },
}
```

- [ ] **Step 9: 运行全部 accounts 测试**

```bash
cd D:/lingoox_workspace/new_aurora
go test ./internal/accounts/ -v
```
Expected: PASS

- [ ] **Step 10: 提交**

```bash
git add internal/accounts/
git commit -m "feat: add Account model, Capabilities, JSONStore, and FingerprintProfiles"
```

---

### Task 4: Accounts 模块 — 账号池

**前置依赖:** Task 3

**Files:**
- Create: `internal/accounts/pool.go`
- Create: `internal/accounts/pool_test.go`

**理解笔记:**
- 当前 `internal/tokens/tokens.go` 的 `AccessToken.GetSecret()` 和 `GetPaidSecret()` 按是否 free 轮询
- 当前 `initialize/auth.go` 从文本文件加载账号列表
- 当前 `initialize/session_manager.go` 管理对话状态缓存
- 新 AccountPool 需要完全替代这些功能

- [ ] **Step 1: 写 AccountPool 测试**

```go
// internal/accounts/pool_test.go
package accounts

import (
    "testing"
)

func TestPoolAcquireByType(t *testing.T) {
    pool := NewPool(nil)

    pool.AddAccount(NewAccount("noauth-1", TypeNoAuth, "uuid-1"))
    pool.AddAccount(NewAccount("free-1", TypeFree, "token-free-1"))
    pool.AddAccount(NewAccount("puid-1", TypePUID, "token-puid-1"))

    // 标记 active
    for _, a := range pool.accounts {
        a.Status = StatusActive
    }

    acct, err := pool.Acquire(TypePUID)
    if err != nil {
        t.Fatalf("Acquire PUID: %v", err)
    }
    if acct.Type != TypePUID {
        t.Errorf("got type %s, want puid", acct.Type)
    }

    acct, err = pool.Acquire(TypeNoAuth)
    if err != nil {
        t.Fatalf("Acquire NoAuth: %v", err)
    }
    if acct.Type != TypeNoAuth {
        t.Errorf("got type %s, want noauth", acct.Type)
    }
}

func TestPoolAcquireRoundRobin(t *testing.T) {
    pool := NewPool(nil)
    a1 := NewAccount("a1", TypeNoAuth, "1")
    a2 := NewAccount("a2", TypeNoAuth, "2")
    a1.Status = StatusActive
    a2.Status = StatusActive
    pool.AddAccount(a1)
    pool.AddAccount(a2)

    first, _ := pool.Acquire(TypeNoAuth)
    first.TotalCalls++
    _, _ = pool.Acquire(TypeNoAuth)
}

func TestPoolAcquireNoAvailable(t *testing.T) {
    pool := NewPool(nil)
    _, err := pool.Acquire(TypePUID)
    if err == nil {
        t.Fatal("expected error when no accounts available")
    }
}

func TestPoolReleaseUpdatesStats(t *testing.T) {
    pool := NewPool(nil)
    acct := NewAccount("test", TypePUID, "token")
    pool.accounts = append(pool.accounts, acct)

    pool.Release(acct, nil)
    if acct.TotalCalls != 1 {
        t.Errorf("TotalCalls = %d, want 1", acct.TotalCalls)
    }

    pool.Release(acct, errMock)
    if acct.FailedCalls != 1 {
        t.Errorf("FailedCalls = %d, want 1", acct.FailedCalls)
    }
}
```

- [ ] **Step 2: 实现 AccountPool**

```go
// internal/accounts/pool.go
package accounts

import (
    "errors"
    "sync"
)

var (
    ErrNoAvailable = errors.New("no available account of the requested type")
    errMock        = errors.New("mock error")
)

// Pool 账号池管理
type Pool struct {
    mu       sync.Mutex
    accounts []*Account
    cursor   int
}

func NewPool(initial []*Account) *Pool {
    if initial == nil {
        initial = []*Account{}
    }
    return &Pool{
        accounts: initial,
    }
}

// AddAccount 添加一个账号到池中
func (p *Pool) AddAccount(acct *Account) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.accounts = append(p.accounts, acct)
}

// Acquire 按类型获取一个可用账号
func (p *Pool) Acquire(acctType AccountType) (*Account, error) {
    p.mu.Lock()
    defer p.mu.Unlock()

    if len(p.accounts) == 0 {
        return nil, ErrNoAvailable
    }

    for i := 0; i < len(p.accounts); i++ {
        idx := (p.cursor + i) % len(p.accounts)
        acct := p.accounts[idx]
        if acct.Status == StatusActive && acct.Type == acctType {
            p.cursor = (idx + 1) % len(p.accounts)
            return acct, nil
        }
    }

    return nil, ErrNoAvailable
}

// Release 归还账号，根据错误更新状态
func (p *Pool) Release(acct *Account, result error) {
    if acct == nil {
        return
    }

    p.mu.Lock()
    defer p.mu.Unlock()

    acct.TotalCalls++
    if result != nil {
        acct.FailedCalls++
    }
}
```

- [ ] **Step 3: 运行测试通过**

```bash
cd D:/lingoox_workspace/new_aurora
go test ./internal/accounts/ -v
```
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add internal/accounts/pool.go internal/accounts/pool_test.go
git commit -m "feat: add AccountPool with Acquire/Release and round-robin"
```

---

### Task 5: WSSActor — 基础 goroutine 框架

**前置依赖:** Task 3

**Files:**
- Create: `internal/accounts/wss_actor.go`
- Create: `internal/accounts/wss_actor_test.go`

**理解笔记:**
- 当前 `internal/chatgpt/request.go:907-945` 实现 WSS 连接流程
- 流程：GET /celsius/ws/user → Dial → 发 4 条 init 消息
- 当前 `internal/chatgpt/request.go:1048-1122` 实现流读取 + 25s ping
- 新 WSSActor 用 goroutine + channel 模式，Service 层通过 channel 发指令
- TODO：浏览器端保活 ping 的具体参数待抓包确认

- [ ] **Step 1: 写 WSSActor 测试**

```go
// internal/accounts/wss_actor_test.go
package accounts

import (
    "testing"
    "time"
)

func TestWSSActorStartStop(t *testing.T) {
    acct := NewAccount("test", TypePUID, "token")
    actor := NewWSSActor(acct)

    actor.Start()
    time.Sleep(50 * time.Millisecond)
    actor.Stop()
    // 确认不 panic 即可
}
```

- [ ] **Step 2: 实现 WSSActor**

```go
// internal/accounts/wss_actor.go
package accounts

import (
    "sync"
)

// wssCommand 是 Service 层向 WSS goroutine 发送的指令
type wssCommand struct {
    Type    string      // "subscribe", "close"
    Payload interface{}
    Result  chan<- wssResult
}

type wssResult struct {
    Data []byte
    Err  error
}

// WSSActor 每个 free/puid Account 持有一个
// 一个 goroutine 统一管理连接、发送、保活、重连
type WSSActor struct {
    account  *Account
    commands chan wssCommand
    done     chan struct{}
    started  bool
    mu       sync.Mutex
}

func NewWSSActor(account *Account) *WSSActor {
    return &WSSActor{
        account:  account,
        commands: make(chan wssCommand, 16),
        done:     make(chan struct{}),
    }
}

// Start 启动 WSS goroutine
func (a *WSSActor) Start() {
    a.mu.Lock()
    defer a.mu.Unlock()
    if a.started {
        return
    }
    a.started = true
    go a.run()
}

// Stop 停止 WSS goroutine
func (a *WSSActor) Stop() {
    a.mu.Lock()
    defer a.mu.Unlock()
    if !a.started {
        return
    }
    select {
    case <-a.done:
        return
    default:
        close(a.done)
    }
    a.started = false
}

// Subscribe 订阅一个 topic（通过 channel 发送指令到 goroutine）
func (a *WSSActor) Subscribe(topicID string) error {
    result := make(chan wssResult, 1)
    a.commands <- wssCommand{
        Type:    "subscribe",
        Payload: topicID,
        Result:  result,
    }
    r := <-result
    return r.Err
}

// run goroutine 主循环
// TODO: 浏览器端保活 ping 的参数待抓包确认，目前 ticker 占位
func (a *WSSActor) run() {
    // TODO: 连接逻辑（使用 account.Client 和 account.Proxy）
    //   1. GET /celsius/ws/user → 获取 ws_url
    //   2. Dial(wss://...)
    //   3. 发送 4 条 init 消息（connect + 3 subscribe）
    //   4. 启动读 goroutine
    //   5. select 主循环：command / 读帧 / ticker / 断开 / done

    // 占位：直接监听 commands 等待 close
    for {
        select {
        case cmd := <-a.commands:
            switch cmd.Type {
            case "close":
                if cmd.Result != nil {
                    cmd.Result <- wssResult{}
                }
                return
            default:
                if cmd.Result != nil {
                    cmd.Result <- wssResult{}
                }
            }
        case <-a.done:
            return
        }
    }
}
```

- [ ] **Step 3: 运行测试通过**

```bash
cd D:/lingoox_workspace/new_aurora
go test ./internal/accounts/ -v -run TestWSSActor
```
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add internal/accounts/wss_actor.go internal/accounts/wss_actor_test.go
git commit -m "feat: add WSSActor goroutine scaffold with channel-based commands"
```

---

### Task 6: 账号初始化与加载

**前置依赖:** Task 3, 4, 5

**Files:**
- Create: `internal/accounts/loader.go`
- Create: `internal/accounts/loader_test.go`
- Modify: `main.go` (使用新加载逻辑)

**理解笔记:**
- 当前 `initialize/auth.go` 从 `access_tokens.txt`、`free_tokens.txt` 读取
- 每行一个 token，`access_tokens.txt` 格式 `token:team_id`
- `FREE_ACCOUNTS=true` 时生成 N 个 UUID 账号
- 新 Loader 需完全兼容此格式，并分配指纹 + 代理

**Interfaces:**
- Consumes: `config.Config`, `proxy.Pool`, `accounts.Store`
- Produces: `accounts.Pool` (已初始化好的账号池)

- [ ] **Step 1: 写 Loader 测试**

```go
// internal/accounts/loader_test.go
package accounts

import (
    "os"
    "testing"
)

func TestLoadTokensFromFile(t *testing.T) {
    // 写临时文件
    content := "token1\n# comment\ntoken2:team123\n\ntoken3\n"
    tmpfile := "_test_tokens.txt"
    os.WriteFile(tmpfile, []byte(content), 0644)
    defer os.Remove(tmpfile)

    secrets := LoadTokensFromFile(tmpfile)
    if len(secrets) != 3 {
        t.Fatalf("got %d tokens, want 3", len(secrets))
    }
    if secrets[0].Token != "token1" {
        t.Errorf("secrets[0].Token = %q, want %q", secrets[0].Token, "token1")
    }
    if secrets[1].Token != "token2" {
        t.Errorf("secrets[1].Token = %q, want %q", secrets[1].Token, "token2")
    }
}

func TestLoadTokensFromFileMissing(t *testing.T) {
    secrets := LoadTokensFromFile("_nonexistent_file_")
    if secrets == nil || len(secrets) != 0 {
        t.Errorf("missing file should return empty list, got %v", secrets)
    }
}
```

- [ ] **Step 2: 实现 Loader**

```go
// internal/accounts/loader.go
package accounts

import (
    "bufio"
    "math/rand"
    "os"
    "strings"

    "github.com/google/uuid"
)

// LoadSecrets 从文件加载 token 列表
type LoadedSecret struct {
    Token  string
    TeamID string
    IsFree bool
}

// LoadTokensFromFile 从文件读取 token，兼容原格式
// 空行和 # 开头的行被忽略
func LoadTokensFromFile(path string) []LoadedSecret {
    f, err := os.Open(path)
    if err != nil {
        return nil
    }
    defer f.Close()

    var secrets []LoadedSecret
    scanner := bufio.NewScanner(f)
    for scanner.Scan() {
        line := strings.TrimSpace(scanner.Text())
        if line == "" || strings.HasPrefix(line, "#") {
            continue
        }
        parts := strings.SplitN(line, ":", 2)
        token := strings.TrimSpace(parts[0])
        if token == "" {
            continue
        }
        secret := LoadedSecret{Token: token}
        if len(parts) > 1 {
            secret.TeamID = strings.TrimSpace(parts[1])
        }
        secrets = append(secrets, secret)
    }
    return secrets
}

// InitAccountsFromConfig 根据配置初始化账号池
// 从 access_tokens.txt / free_tokens.txt 加载，必要时生成 free UUID
func InitAccountsFromConfig(
    accessTokenPath string,
    freeTokenPath string,
    freeAccounts bool,
    freeAccountsNum int,
    profilePool []FingerprintProfile,
) []*Account {
    var accounts []*Account

    // 加载 paid token
    for _, s := range LoadTokensFromFile(accessTokenPath) {
        acct := NewAccount(uuid.NewString(), TypePUID, s.Token)
        if s.TeamID != "" {
            acct.TeamUserID = s.TeamID
        }
        acct.Fingerprint = randomProfile(profilePool)
        acct.Status = StatusActive
        accounts = append(accounts, acct)
    }

    // 加载 free token
    for _, s := range LoadTokensFromFile(freeTokenPath) {
        acct := NewAccount(uuid.NewString(), TypeFree, s.Token)
        acct.Fingerprint = randomProfile(profilePool)
        acct.Status = StatusActive
        accounts = append(accounts, acct)
    }

    // 生成 free UUID 账号
    if freeAccounts {
        for i := 0; i < freeAccountsNum; i++ {
            uid := uuid.NewString()
            acct := NewAccount(uid, TypeNoAuth, uid)
            acct.Fingerprint = randomProfile(profilePool)
            acct.Status = StatusActive
            accounts = append(accounts, acct)
        }
    }

    return accounts
}

func randomProfile(profiles []FingerprintProfile) BrowserFingerprint {
    if len(profiles) == 0 {
        return BrowserFingerprint{
            OaiDeviceID:  uuid.NewString(),
            OaiSessionID: uuid.NewString(),
        }
    }
    p := profiles[rand.Intn(len(profiles))]
    return BrowserFingerprint{
        OaiDeviceID:         uuid.NewString(),
        OaiSessionID:        uuid.NewString(),
        UserAgent:           p.UserAgent,
        ScreenWidth:         p.ScreenWidth,
        ScreenHeight:        p.ScreenHeight,
        HardwareConcurrency: p.HardwareConcurrency,
        Platform:            p.Platform,
        TLSProfileName:      p.TLSProfileName,
    }
}
```

- [ ] **Step 3: 运行测试通过**

```bash
cd D:/lingoox_workspace/new_aurora
go test ./internal/accounts/ -v
```
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add internal/accounts/loader.go internal/accounts/loader_test.go
git commit -m "feat: add account loader with file parsing and UUID generation"
```

---

### Task 7: 将 accounts 模块接入 main.go

**前置依赖:** Task 6

**Files:**
- Modify: `main.go`
- Modify: `initialize/router.go`（导入路径适配）
- Modify: `initialize/handlers.go`（使用 accounts 替代 tokens）

**理解笔记:**
- 当前 `main.go` 调用 `initialize.RegisterRouter()` 
- `initialize/handlers.go` 的 `Handler` 结构体目前包含 `token *tokens.AccessToken` 和 `proxy *proxys.IProxy`
- 新 Handler 将使用 `*accounts.Pool` 替代 `*tokens.AccessToken`
- 此阶段为初步接入，后续 Task 会进一步拆分 handler

- [ ] **Step 1: 更新 Handler 结构体**

```go
// initialize/handlers.go — 修改 Handler 结构体
type Handler struct {
    proxy    interface{}     // 暂不依赖具体实现，可用存根
    accountPool *accounts.Pool
    sessions *SessionManager
}

func NewHandle(accountPool *accounts.Pool) *Handler {
    return &Handler{
        accountPool: accountPool,
        sessions:  NewSessionManager(),
    }
}
```

- [ ] **Step 2: 更新 router.go 初始化流程**

```go
// initialize/router.go (部分修改)
func RegisterRouter() *gin.Engine {
    cfg := config.Load()
    proxyPool := proxy.NewParser(cfg)
    accs := accounts.InitAccountsFromConfig(...)
    pool := accounts.NewPool(accs)

    handler := NewHandle(pool)
    handler.InitBasicConfigForChatGPT()

    router := gin.Default()
    router.Use(middlewares.Cors)
    // ... 路由注册不变
    return router
}
```

- [ ] **Step 3: 更新 main.go 使用 Config**

```go
// main.go (简化)
func main() {
    gin.SetMode(gin.ReleaseMode)
    _ = godotenv.Load(".env")
    browserfp.Init()
    router := initialize.RegisterRouter()
    // ... 启动 server
}
```

- [ ] **Step 4: 确认编译通过**

```bash
cd D:/lingoox_workspace/new_aurora
go build -o aurora.exe ./...
```

- [ ] **Step 5: 确认原测试依然通过**

```bash
cd D:/lingoox_workspace/new_aurora
go test ./... 2>&1
```
Expected: 原有测试 PASS（accounts 新模块测试也 PASS）

- [ ] **Step 6: 提交**

```bash
git add main.go initialize/ internal/accounts/ internal/proxy/
git commit -m "feat: integrate accounts module, update main.go and handler initialization"
```

---

### Task 8: Handler 文件拆分

**前置依赖:** Task 7

**Files:**
- Create: `internal/handler/chat_handler.go`
- Create: `internal/handler/image_handler.go`
- Create: `internal/handler/audio_handler.go`
- Create: `internal/handler/auth_handler.go`
- Create: `internal/handler/models_handler.go`
- Create: `internal/handler/router.go`
- Modify: `initialize/handlers.go`（删除被拆分的方法）
- Delete: `initialize/handlers.go`（拆分完成后删除）

**理解笔记:**
- 当前 `initialize/handlers.go` 2464 行包含：
  - `nightmare()` — 聊天完成 + continue 循环 (~200 行)
  - `responses()` — Responses API (~150 行)
  - `imageGenerations()` — 图片生成 (~200 行)
  - `runImageEditFlow()` — 图片编辑/变体 (~430 行)
  - `tts()` / `transcriptions()` / `translations()` — 音频 (~250 行)
  - `files()` — 文件上传 (~80 行)
  - `engines()` — 模型列表 (~40 行)
  - `handleToolCalling()` — 工具调用 (~100 行)
  - `refresh()` / `session()` — 认证 (~80 行)
  - 辅助函数 ~500 行
- 新 handler 仅做 HTTP 编解码，业务逻辑调用 service 层

- [ ] **Step 1: 创建 router.go**

```go
// internal/handler/router.go
package handler

import (
    "aurora/internal/accounts"
    "aurora/internal/middlewares"
    "github.com/gin-gonic/gin"
)

// RegisterRouter 注册所有路由（替代 initialize.RegisterRouter）
func RegisterRouter(accountPool *accounts.Pool) *gin.Engine {
    chatHandler := NewChatHandler(accountPool)
    imageHandler := NewImageHandler(accountPool)
    audioHandler := NewAudioHandler(accountPool)
    authHandler := NewAuthHandler(accountPool)
    modelsHandler := NewModelsHandler()
    // ... 初始化 sentinel client 等

    router := gin.Default()
    router.Use(middlewares.Cors)

    router.GET("/", func(c *gin.Context) { c.JSON(200, gin.H{"message": "Hello, world!"}) })
    router.GET("/ping", func(c *gin.Context) { c.JSON(200, gin.H{"message": "pong"}) })

    router.POST("/auth/session", authHandler.Session)
    router.POST("/auth/refresh", authHandler.Refresh)

    authGroup := router.Group("").Use(middlewares.Authorization)
    authGroup.POST("/v1/chat/completions", chatHandler.Nightmare)
    authGroup.POST("/v1/responses", chatHandler.Responses)
    authGroup.POST("/v1/files", chatHandler.Files)
    authGroup.GET("/v1/models", modelsHandler.ListModels)
    authGroup.POST("/backend-api/conversation", chatHandler.ChatGPTConversation)
    authGroup.POST("/v1/images/generations", imageHandler.Generations)
    authGroup.POST("/v1/images/edits", imageHandler.Edits)
    authGroup.POST("/v1/images/variations", imageHandler.Variations)
    authGroup.POST("/v1/audio/speech", audioHandler.TTS)
    authGroup.POST("/v1/audio/transcriptions", audioHandler.Transcriptions)
    authGroup.POST("/v1/audio/translations", audioHandler.Translations)

    return router
}
```

- [ ] **Step 2: 创建 chat_handler.go**

```go
// internal/handler/chat_handler.go
package handler

import (
    "aurora/internal/accounts"
    officialtypes "aurora/typings/official"
    "github.com/gin-gonic/gin"
)

type ChatHandler struct {
    accountPool *accounts.Pool
}

func NewChatHandler(pool *accounts.Pool) *ChatHandler {
    return &ChatHandler{accountPool: pool}
}

func (h *ChatHandler) Nightmare(c *gin.Context) {
    var req officialtypes.APIRequest
    if err := c.BindJSON(&req); err != nil {
        respondError(c, 400, err)
        return
    }
    // TODO: 调用 service.ChatCompletion
    c.JSON(200, gin.H{"message": "not implemented"})
}

func (h *ChatHandler) Responses(c *gin.Context) {
    // TODO: 调用 service.Responses
    c.JSON(200, gin.H{"message": "not implemented"})
}

func (h *ChatHandler) Files(c *gin.Context) {
    // TODO: 调用 service.FileUpload
    c.JSON(200, gin.H{"message": "not implemented"})
}

func (h *ChatHandler) ChatGPTConversation(c *gin.Context) {
    // TODO: 调用 service.ChatGPTConversation
    c.JSON(200, gin.H{"message": "not implemented"})
}
```

- [ ] **Step 3: 创建 auth_handler.go**

```go
// internal/handler/auth_handler.go
package handler

import (
    "aurora/internal/accounts"
    officialtypes "aurora/typings/official"
    "github.com/gin-gonic/gin"
)

type AuthHandler struct {
    accountPool *accounts.Pool
}

func NewAuthHandler(pool *accounts.Pool) *AuthHandler {
    return &AuthHandler{accountPool: pool}
}

func (h *AuthHandler) Refresh(c *gin.Context) {
    var req officialtypes.OpenAIRefreshToken
    if err := c.BindJSON(&req); err != nil {
        respondError(c, 400, err)
        return
    }
    // TODO: 调用 authService.Refresh
    c.JSON(200, gin.H{"message": "not implemented"})
}

func (h *AuthHandler) Session(c *gin.Context) {
    var req officialtypes.OpenAISessionToken
    if err := c.BindJSON(&req); err != nil {
        respondError(c, 400, err)
        return
    }
    // TODO: 调用 authService.Session
    c.JSON(200, gin.H{"message": "not implemented"})
}
```

- [ ] **Step 4: 创建 image_handler.go / audio_handler.go / models_handler.go**

```go
// internal/handler/image_handler.go
package handler
// 同模式：Generations, Edits, Variations
```

```go
// internal/handler/audio_handler.go
package handler
// 同模式：TTS, Transcriptions, Translations
```

```go
// internal/handler/models_handler.go
package handler
// 同模式：ListModels
```

- [ ] **Step 5: 创建 shared.go (辅助函数)**

```go
// internal/handler/shared.go
package handler

import (
    "net/http"
    "github.com/gin-gonic/gin"
)

func respondError(c *gin.Context, status int, err error) {
    c.JSON(status, gin.H{"error": gin.H{
        "message": err.Error(),
        "type":    "invalid_request_error",
        "param":   nil,
        "code":    http.StatusText(status),
    }})
}
```

- [ ] **Step 6: 确认编译通过**

```bash
cd D:/lingoox_workspace/new_aurora
go build -o aurora.exe ./...
```

- [ ] **Step 7: 提交**

```bash
git add internal/handler/
git commit -m "refactor: split handlers into separate files under internal/handler/"
```

---

### Task 9: chatgpt/request.go 文件拆分

**前置依赖:** Task 0 (有完整的 request.go)

**Files:**
- Create: `internal/chatgpt/sentinel.go`
- Create: `internal/chatgpt/conversation.go`
- Create: `internal/chatgpt/websocket.go`
- Create: `internal/chatgpt/sse.go`
- Create: `internal/chatgpt/init.go`
- Create: `internal/chatgpt/models.go`
- Keep: `internal/chatgpt/artifacts.go`、`artifact_delivery.go`、`files.go`、`transcribe.go`、`client_state.go`、`cookie_bootstrap.go`（原样）
- Remove: `internal/chatgpt/request.go`（拆分完成后移除）

**理解笔记:**
- `request.go` 3416 行代码，包含 6 类功能，按函数前缀分类：
  1. **Sentinel 相关** (约 500 行): `InitSentinel*`, `POSTSentinel*`, `sentinel*`
  2. **Conversation 相关** (约 400 行): `POSTconversation*`, `PrepareConversation*`, `POSTConversationInit`
  3. **WebSocket 相关** (约 300 行): `getChatWebsocket*`, `DialChatWebsocket*`, `chatWebsocket*`
  4. **SSE 解析** (约 600 行): `HandlerDetailed*`, `streamHandoff*`, `HandlerResult`
  5. **Init/Config** (约 400 行): `GetDpl`, `GetInitConfig`, `CalcProofToken`, `TurnStile` 结构体
  6. **工具函数** (约 300 行): `conversationURL`, `apiURL`, `createBaseHeader`, `defaultUserAgent`
- 拆分原则：每块作为一个独立文件，代码一字不改，只移动并保持包名一致

- [ ] **Step 1: 深度阅读 request.go，为每个函数标注归属文件**

```bash
cd D:/lingoox_workspace/new_aurora
# 用 grep 列出所有函数定义，标注归属
grep -n "^func " internal/chatgpt/request.go | head -100
```

记录每个函数的目标文件。

- [ ] **Step 2: 创建 init.go (配置/全局状态)**

将以下内容移入 init.go：
- `BaseURL` 变量
- `init()` 函数
- `GetDpl()`
- `GetInitConfig()`
- `CalcProofToken()`
- `ChatRequire` / `TurnStile` 类型定义
- `conversationURL` / `apiURL` / `sentinelURL`
- `defaultUserAgent` / `createBaseHeader`

- [ ] **Step 3: 创建 sentinel.go**

将以下内容移入 sentinel.go：
- `InitTurnStile*` 函数族
- `InitSentinel*` 函数族
- `POSTSentinel*` 函数族
- `stateFlow` / `soDeviceIDFor` / `ensureSOToken`
- `sentinelExtraData` / `buildSentinelExtraData`

- [ ] **Step 4: 创建 conversation.go**

将以下内容移入 conversation.go：
- `POSTConversationInit`
- `POSTconversation*`
- `PrepareConversationConduit*`
- `conversationInitResponse`
- `conversationHeadersWithState`

- [ ] **Step 5: 创建 websocket.go**

将以下内容移入 websocket.go：
- `chatWebsocketURLResponse`
- `getChatWebsocketURL*`
- `DialChatWebsocket*`
- `chatWebsocketEncodedItem`
- `chatWebsocketConversationUpdateItem`
- `chatWebsocketSSEItems`
- `chatWebsocketWriteEncodedItem`
- `chatWebsocketStreamReader`
- `shouldUseWebsocketHandoff`
- `websocketProxyFunc`
- `parseChatWebsocketFrames`

- [ ] **Step 6: 创建 sse.go**

将以下内容移入 sse.go：
- `HandlerDetailed*`
- `HandlerDetailedOptions` / `HandlerResult`
- `streamHandoffTopic*`
- `firstConversationID`
- SSE 解析相关常量和函数

- [ ] **Step 7: 创建 models.go**

将以下内容移入 models.go：
- `ChatClientState`（如果从 client_state.go 移出）
- `PrepareState`
- `ContinueInfo`
- 其他纯类型定义

- [ ] **Step 8: 验证编译和测试**

```bash
cd D:/lingoox_workspace/new_aurora
go build ./...
go test ./internal/chatgpt/ -v
```
Expected: 所有测试 PASS（代码未改，只是移动位置）

- [ ] **Step 9: 提交**

```bash
git add internal/chatgpt/
git commit -m "refactor: split request.go into sentinel, conversation, websocket, sse, init, models"
```

---

### Task 10: Service 层 — 业务编排

**前置依赖:** Task 8 (handler 已拆分), Task 9 (chatgpt 已拆分)

**Files:**
- Create: `internal/service/chat_service.go`
- Create: `internal/service/chat_service_test.go`
- Create: `internal/service/image_service.go`
- Create: `internal/service/audio_service.go`
- Create: `internal/service/toolcall_service.go`
- Create: `internal/service/file_service.go`

**理解笔记:**
- Service 层负责编排 `internal/chatgpt/` 的函数调用顺序
- 当前编排逻辑在 `initialize/handlers.go` 中与 HTTP 处理混合
- 例如 `nightmare()` → sentinel → init → ws → prepare → conversation → continue 循环
- Service 接收 `*Account` 和 `officialtypes.APIRequest`，返回业务结果

- [ ] **Step 1: 写 ChatService 测试**

```go
// internal/service/chat_service_test.go
package service

import (
    "testing"
)

func TestContinueCountDefault(t *testing.T) {
    if maxContinueCount() != 3 {
        t.Errorf("default continue count should be 3")
    }
}

func TestToolCallingEnabled(t *testing.T) {
    // 默认未设置 TOOL_CALLING_ENABLED 时
    if !toolCallingEnabled(nil) {
        t.Error("toolCallingEnabled(nil) should be true when env not set")
    }
}
```

- [ ] **Step 2: 实现 ChatService**

```go
// internal/service/chat_service.go
package service

import (
    "os"
    "strconv"
    "strings"

    "aurora/internal/accounts"
    "aurora/internal/chatgpt"
    officialtypes "aurora/typings/official"
)

// ChatResult 聊天完成的结果
type ChatResult struct {
    Text         string
    ThinkingText string
    ConversationID string
    Continue     *chatgpt.ContinueInfo
    StopSent     bool
    Sentinel     []map[string]interface{}
}

// maxContinueCount 从环境变量读取（后续改为从 Config 获取）
func maxContinueCount() int {
    v := os.Getenv("MAX_CONTINUE_COUNT")
    if v == "" {
        return 3
    }
    n, err := strconv.Atoi(v)
    if err != nil || n < 0 {
        return 3
    }
    return n
}

// toolCallingEnabled 判断工具调用是否启用
func toolCallingEnabled(tools []officialtypes.Tool) bool {
    if env := strings.ToLower(strings.TrimSpace(os.Getenv("TOOL_CALLING_ENABLED"))); env == "false" || env == "0" || env == "no" {
        return false
    }
    return len(tools) > 0
}

// ChatCompletion 执行完整的聊天完成流程
// TODO: 实现完整的编排（sentinel → init → ws → prepare → conversation → continue）
func ChatCompletion(req officialtypes.APIRequest, account *accounts.Account) (*ChatResult, error) {
    return &ChatResult{}, nil
}
```

- [ ] **Step 3: 确认编译 + 测试通过**

```bash
cd D:/lingoox_workspace/new_aurora
go build ./...
go test ./internal/service/ -v
```

- [ ] **Step 4: 提交**

```bash
git add internal/service/
git commit -m "feat: add Service layer with chat_service scaffold"
```

---

### Task 11: 中间件强化 — auth 逻辑从 handler 移入

**前置依赖:** Task 8

**Files:**
- Modify: `middlewares/auth.go`
- Create: `internal/middleware/auth_test.go`

- [ ] **Step 1: 强化 auth 中间件**

```go
// internal/middleware/auth.go
package middlewares

import (
    "net/http"
    "os"
    "strings"

    "github.com/gin-gonic/gin"
)

func Authorization(c *gin.Context) {
    authHeader := c.GetHeader("Authorization")
    expected := os.Getenv("Authorization")

    if expected == "" {
        c.Next()
        return
    }

    token := strings.TrimPrefix(authHeader, "Bearer ")
    token = strings.TrimSpace(token)
    // 支持 ",team_id" 格式
    parts := strings.SplitN(token, ",", 2)
    token = strings.TrimSpace(parts[0])

    if token == expected {
        c.Next()
        return
    }

    if strings.HasPrefix(token, "eyJ") || len(token) > 64 {
        // 是 access_token 或 refresh_token，放行让 handler 进一步处理
        c.Set("auth_token", token)
        if len(parts) > 1 {
            c.Set("team_account_id", strings.TrimSpace(parts[1]))
        }
        c.Next()
        return
    }

    c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
        "error": gin.H{
            "message": "Invalid authorization token",
            "type":    "invalid_request_error",
        },
    })
}
```

- [ ] **Step 2: 提交**

```bash
git add middlewares/
git commit -m "refactor: strengthen auth middleware, extract token and team ID"
```

---

### Task 12: 最终集成与清理

**前置依赖:** Task 7-11

- [ ] **Step 1: 删除废弃的包**

```bash
cd D:/lingoox_workspace/new_aurora
rm -rf internal/tokens/
rm -rf internal/proxys/
mv typings internal/types
```

- [ ] **Step 2: 更新所有 import 路径**

```bash
cd D:/lingoox_workspace/new_aurora
# 查找所有引用旧路径的地方
grep -rn "aurora/typings/" --include="*.go" | head -20
grep -rn "aurora/internal/tokens" --include="*.go" | head -10
grep -rn "aurora/internal/proxys" --include="*.go" | head -10
# 逐个更新 import
```

- [ ] **Step 3: 验证全部编译 + 测试通过**

```bash
cd D:/lingoox_workspace/new_aurora
go build ./...
go test ./... 2>&1
```

Expected: 全部编译成功，所有测试 PASS

- [ ] **Step 4: 最终提交**

```bash
git add -A
git commit -m "refactor: final cleanup - remove deprecated packages, update imports, rename typings to types"
```

---

## 实施检查清单

| 任务 | 内容 | TDD | 测试覆盖 | 1:1 复制 | 完成 |
|------|------|-----|---------|----------|------|
| 0 | 项目基线 | - | ✓ (已有) | ✓ | |
| 1 | Config 模块 | ✓ | ✓ | - | |
| 2 | Proxy 模块 | ✓ | ✓ | - | |
| 3 | Account 模型+持久化 | ✓ | ✓ | - | |
| 4 | AccountPool | ✓ | ✓ | - | |
| 5 | WSSActor 框架 | ✓ | - | - | |
| 6 | 账号初始化 | ✓ | ✓ | - | |
| 7 | 接入 main.go | - | - | - | |
| 8 | Handler 拆分 | - | - | - | |
| 9 | chatgpt/request.go 拆分 | - | ✓ | ✓ | |
| 10 | Service 层 | - | ✓ | - | |
| 11 | 中间件强化 | - | ✓ | - | |
| 12 | 最终清理 | - | - | - | |
