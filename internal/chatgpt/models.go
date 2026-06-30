package chatgpt

// PrepareState 表示 /f/conversation/prepare 的客户端状态机:
// none -> sent -> success -> conversation
// 真实浏览器严格按此顺序触发;漏掉任何一阶段都会被服务端识别为非标准客户端,
// 进而把请求路由到 mini 池。
type PrepareState string

const (
	PrepareStateNone    PrepareState = "none"
	PrepareStateSent    PrepareState = "sent"
	PrepareStateSuccess PrepareState = "success"
)
