# 目录结构对齐上游 + IPv6 剥离迁移设计

## 1. 背景与目标

从 `cleanup-old-code` 分支出发，创建一个新分支 `align-upstream`，做两件事：

1. **目录结构对齐** aurora_upstream（保留 new_aurora 有价值的改进）
2. **剥离所有 IPv6 逻辑**（从 cleanup-old-code 的优化中移除，但不动原分支）

## 2. 目录结构对齐方案

采用"保留上下游都有的内容"策略：aurora_upstream 的顶层目录全部保留，new_aurora 新增的内部包也保留。

### 2.1 顶层目录映射

| aurora_upstream 目录 | new_aurora 对齐后 | 说明 |
|---|---|---|
| `api/` | `api/` | 不变 |
| `conversion/` | `conversion/` | 不变 |
| `httpclient/` | `httpclient/` | 不变 |
| `middlewares/` | `middlewares/` | 不变 |
| `util/` | `util/` | 不变 |
| `typings/` | `typings/` | **从 `internal/types/` 迁移过来** |
| — | `internal/types/` | **删除**（已迁到 `typings/`） |
| `initialize/` | — | **不创建**（已被拆分为 `internal/handler/` + `internal/bootstrap/` + `middlewares/`） |
| — | `internal/accounts/` | 保留（new_aurora 新增） |
| — | `internal/bootstrap/` | 保留（new_aurora 新增，对齐了 upstream 的初始化职责） |
| — | `internal/config/` | 保留（new_aurora 新增） |
| — | `internal/handler/` | 保留（取代 upstream 的 `initialize/handlers.go`） |
| — | `internal/proxy/` | 保留（取代 upstream 的 `internal/proxys/`，移除 IPv6） |

### 2.2 包级别的映射关系

| aurora_upstream 文件 | new_aurora 对应文件 | 状态 |
|---|---|---|
| `initialize/handlers.go` | `internal/handler/*.go` | ✅ 拆分且增强 |
| `initialize/router.go` | `internal/handler/router.go` | ✅ 路由表相同 |
| `initialize/auth.go` | `middlewares/auth.go` + `bootstrap.go` | ✅ 认证放中间件，账号加载放 bootstrap |
| `initialize/proxy.go` | `internal/bootstrap/bootstrap.go` + `internal/proxy/` | ✅ 代理初始化放 bootstrap，池实现独立 |
| `initialize/session_manager.go` | `internal/handler/session_manager.go` | ✅ 代码基本相同 |
| `internal/proxys/proxys.go` | `internal/proxy/pool.go` | ✅ 增强版（清理 IPv6 后） |
| `internal/tokens/tokens.go` | `internal/accounts/*.go` | ✅ 完整账号管理体系取代简单 token 池 |
| `typings/*` | `internal/types/*` | 🔄 迁移回 `typings/` |

### 2.3 `typings/` 迁移计划

将 `internal/types/` 完整迁移到 `typings/`：

```
new_aurora 现状:
  internal/types/
  ├── chatgpt/request.go
  ├── chatgpt/request_test.go
  ├── chatgpt/response.go
  ├── official/request.go
  ├── official/response.go
  ├── official/response_test.go
  └── typings.go

迁移后:
  typings/
  ├── chatgpt/request.go
  ├── chatgpt/request_test.go
  ├── chatgpt/response.go
  ├── official/request.go
  ├── official/response.go
  ├── official/response_test.go
  └── typings.go

  internal/types/  →  删除
```

需要更新所有 `import "aurora/internal/types/..."` → `import "aurora/typings/..."`。

---

## 3. IPv6 剥离影响分析（调用栈追溯）

### 3.1 需要修改的文件清单

| # | 文件 | 改动 |
|---|------|------|
| 1 | `internal/config/config.go` | 删除 `IPv6CIDR`、`IPv6IFace` 字段 + Load() 中的读取 |
| 2 | `internal/proxy/pool.go` | 简化 Pool 为纯 IPv4，删除 CIDRFromInterface / IsIPv6 / nthIPv6 / subnetSize / IPv6Count / initIPv6 / allocateIPv6 / releaseIPv6 |
| 3 | `internal/proxy/pool_test.go` | 删除 IPv6 相关测试用例 |
| 4 | `internal/accounts/account.go` | `InitClient()` 删除 `WithLocalAddr`、`SetLocalAddr`，简化代理绑定的条件判断 |
| 5 | `internal/accounts/wss_actor.go` | 删除 `IsIPv6` 重复函数，简化 WebSocket 代理选择逻辑 |
| 6 | `httpclient/bogdanfinn/tls_client.go` | 删除 `localAddr` 字段、`SetLocalAddr()`、`proxyDesc()` 中的 IPv6 分支 |
| 7 | `internal/bootstrap/bootstrap.go` | 删除 IPv6 CIDR 接口检测、删除 IPv6CIDR 变量、`NewPool(proxies, "")` 改为 `NewPool(proxies)` |
| 8 | `internal/config/config_test.go` | 如果存在 IPv6 相关测试则删除 |

### 3.2 调用栈追踪

#### 影响链路 1: Config → Proxy → Account (最宽)

```
config.Load()                # 删除 IPv6CIDR, IPv6IFace
  ↓
bootstrap.Init()              # 删除 CIDRFromInterface 检测
  ↓
proxy.NewPool(proxies, "") → NewPool(proxies)  # 移除了 ipv6CIDR 参数
  ↓
proxyPool.Allocate()          # 只走 IPv4 round-robin 分支
  ↓
acct.Proxy = proxyPool.Allocate()
  ↓
acct.InitClient()             # 不再判断 IsIPv6，不再 WithLocalAddr
```

#### 影响链路 2: Account → WSS Actor

```
wss_actor.go connect()
  └── if a.account.Proxy != "" && !IsIPv6(a.account.Proxy) { dialer.Proxy = ... }
                                                    ↓
                                     改为 if a.account.Proxy != "" { dialer.Proxy = ... }
```

#### 影响链路 3: HTTP Client → Debug 日志

```
tls_client.go Request()
  └── proxyDesc() → 删除 src:xxx IPv6 分支，只保留 proxy:xxx 和 direct
     ↓
  SetLocalAddr() → 删除（IPv6 不再需要绑定源 IP）
```

### 3.3 风险点与回归预防

| 风险 | 影响 | 缓解 |
|------|------|------|
| `NewPool` 签名变更 | 所有调用者需更新 | 只有 `bootstrap.go` 一处调用 |
| `Pool` 结构体简化 | IPv6 字段全删，只保留 ipv4List + cursor | 编译检查即可 |
| `InitClient()` 代理判断逻辑 | 原 `if !proxy.IsIPv6(a.Proxy) && a.Proxy != ""` 变为 `if a.Proxy != ""` | 语义等价：非空即设代理 |
| `typings/` 迁移 | 大量 import 路径变更 | 全局替换 + 编译验证 |

---

## 4. 功能对应关系验证

### 4.1 `initialize/handlers.go` vs `internal/handler/`

| 功能 | upstream 函数 | new_aurora 位置 | 差异 |
|---|---|---|---|
| 聊天补全 | `nightmare()` | `chat_handler.go Nightmare()` | ✅ 逻辑一致 |
| 响应 API | `responses()` | `chat_handler.go Responses()` | ✅ |
| 图片生成 | `imageGenerations()` | `image_handler.go Generations()` | ✅ |
| 图片编辑 | `runImageEditFlow()` | `image_handler.go Edits/Variations()` | ✅ |
| 文件上传 | `files()` | `chat_handler.go Files()` | ✅ |
| 模型列表 | `engines()` | `models_handler.go ListModels()` | ✅ |
| TTS | `tts()` | `audio_handler.go TTS()` | ✅ |
| 语音转写 | `handleTranscription()` | `audio_handler.go handleTranscription()` | ✅ |
| SSE 工具 | `writeImageStreamHeader/Event/Done()` | `image_handler.go` 同 | ✅ |
| refresh/session | `session() refresh()` | `auth_handler.go` | ✅ |
| `initTurnStileWithRetry` | 有 | **移除** | 改用 `pool.ReportFailure` + 健康检查续期 |
| `InitBasicConfigForChatGPT` | 有 | `handler/router.go` 直接调用 `GetDpl` | ✅ |

### 4.2 新分支注意事项

1. **`internal/chatgpt/request.go`** 拆分确认：上游 117 个函数，我们在 6 个文件中全部分散覆盖，没有遗漏。
2. **`internal/chatgpt/` 的 `init()` 函数**：两个项目都有，功能一致。
3. **`endless` 优雅关闭** 在 `main.go` 中保持一致。
4. **`middlewares/auth.go`**：new_aurora 版本比上游增强（兼容无密钥模式、解析 team_id），保留增强版本。
5. **`internal/accounts/`** 和上游的 `internal/tokens/` 是"替换"关系：new_aurora 不创建 `internal/tokens/`，而是用 `accounts` 包管理账号生命周期、续期、故障标记等功能。

---

## 5. 迁移步骤

### Phase 1: 创建分支 + IPv6 剥离

1. 从 `cleanup-old-code` 创建 `align-upstream`
2. 改 `internal/proxy/pool.go` — 移除 IPv6，简化 `NewPool` 签名
3. 删 `internal/proxy/pool_test.go` 中的 IPv6 测试
4. 改 `internal/config/config.go` — 移除 IPv6 字段
5. 改 `internal/config/config_test.go` — 如有 IPv6 测试则删
6. 改 `internal/accounts/account.go` — `InitClient()` 简化
7. 改 `internal/accounts/wss_actor.go` — 删 `IsIPv6`，简化代理条件
8. 改 `httpclient/bogdanfinn/tls_client.go` — 删 `localAddr`/`SetLocalAddr`
9. 改 `internal/bootstrap/bootstrap.go` — 删 IPv6 检测逻辑

### Phase 2: `typings/` 迁移

10. 创建 `typings/` 目录（与 `internal/` 同级）
11. 复制 `internal/types/*` → `typings/`
12. 全局替换 import `"aurora/internal/types"` → `"aurora/typings"`
13. 验证编译通过
14. 删除 `internal/types/`

### Phase 3: 编译验证 + 测试

15. `go build ./...`
16. `go test ./...`
17. 手动检查关键包的 import 是否完整

---

## 6. 不变契约

- **不修改** `internal/chatgpt/` 中的任何算法逻辑（逆向 ChatGPT 的核心）
- **不修改** `conversion/` / `httpclient/` / `internal/fingerprint/` / `internal/prooftoken/` / `internal/turnstile/` / `internal/so/` / `internal/toolcall/` 等核心包
- **不修改** `cleanup-old-code` 分支的任何代码
- **保留** `internal/accounts/`（完整账号管理体系）、`internal/config/`（集中配置）、`internal/bootstrap/`（初始化入口）
- **保留** `internal/handler/`（不退回上游的 monolith `initialize/handlers.go`）

---

## 7. 已知潜在问题（待评估）

| # | 描述 | 来源 | 影响面 |
|---|------|------|--------|
| P1 | **401 后恢复不及时**：上游 `initTurnStileWithRetry` 在 paid token 401 时立即禁用并轮换下一个 paid token。我们改为 `ReportFailure` → 标记 `Expired` → 等 10 分钟健康检查续期。健康检查只用 refresh/session token 续期才能恢复，纯 access token 的账号 401 后永久不可用直到进程重启。 | 剥离 `initTurnStileWithRetry` | 若部署环境有超短会话窗口的服务 (如短 token 时效)，健康检查间隔可能过长。**待用户体验验证后决定是否缩短健康检查间隔或增加 401 后即时重试。** |
| P2 | **`StatusRateLimited` 永远无法触发**：`ReportFailure` 把 401、429、5xx 全部标记为 `StatusExpired`。429 限流不需要续期，只需等待冷却，但目前无任何代码设 `StatusRateLimited`，限流账号要等健康检查走一遍续期才能恢复，此时限流可能早已解除。 | `pool.ReportFailure` 实现 | 需增加 429 状态码判断分支设置为 `StatusRateLimited`，并在 `Acquire` 中跳过该状态。 |
| P3 | **`JSONStore` 生产代码未使用且序列化危险**：`Account` 包含 `Client`（接口类型）和 `WSSActor`（接口类型），JSON 序列化会 panic。`Store` 接口目前只在单元测试中使用，无人接入生产路径。若将来接入可能导致运行时崩溃。 | `store.go` 实现 | 如果有计划持久化账号状态，需要添加 `json:"-"` 标签或改用选择性序列化（只序列化 Token/Status/Proxy 等基础字段）。 |
| P4 | **`WSSActor.Subscribe` 阻塞无超时**：`commands` channel buffer 为 16，`Subscribe` 写入 channel 后阻塞等待结果。若 `Start()` 未调用或 goroutine 因重连正在退避，写入 17 条后永久阻塞。 | `wss_actor.go` Subscribe | 当前业务 handler 未调用 `Subscribe`（WSS 仅在 conversationClientOrder 中 dial 并用于流式），但未来若接入需添加超时机制（select + time.After）或预检查 `started` 状态。 |
| P5 | **`Pool.Acquire` 不检查 `ExpiresAt`**：只过滤 `Status == StatusActive`，不检查 `acct.ExpiresAt` 是否已到达。如果 Token 已到 TTL 但 Status 从未被标记过期，会继续使用直到碰到真实的 401。 | `pool.go` Acquire | `ExpiresAt` 是预留字段，不对应任何真实 ChatGPT token TTL，目前不影响功能。 |
| P6 | **`Pool.Release` 空实现有冗余参数**：`Release(acct *Account, result error) {}` 中 `result` 参数未被使用，调用者可能误以为传递 error 会影响池状态。 | `pool.go` Release | 纯代码异味，从共享池模式沿袭的空壳。测试中调用 `Release` 的位置很少，无实际影响。 |
