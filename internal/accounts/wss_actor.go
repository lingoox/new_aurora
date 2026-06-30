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
