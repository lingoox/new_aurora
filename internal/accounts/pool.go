package accounts

import (
	"errors"
	"log"
	"sync"
	"time"
)

var ErrNoAvailable = errors.New("no available account of the requested type")

// Pool 账号池管理
type Pool struct {
	mu       sync.Mutex
	entries []*Account
	cursor   int
}

func NewPool(initial []*Account) *Pool {
	if initial == nil {
		initial = []*Account{}
	}
	return &Pool{
		entries: initial,
	}
}

// AddAccount 添加一个账号到池中
func (p *Pool) AddAccount(acct *Account) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries = append(p.entries, acct)
}

// Acquire 按类型获取一个可用账号
func (p *Pool) Acquire(acctType AccountType) (*Account, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.entries) == 0 {
		return nil, ErrNoAvailable
	}

	for i := 0; i < len(p.entries); i++ {
		idx := (p.cursor + i) % len(p.entries)
		acct := p.entries[idx]
		if acct.Status == StatusActive && acct.Type == acctType {
			p.cursor = (idx + 1) % len(p.entries)
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

// ReportFailure 标记账号为过期（如 sentinel 401），Acquire 时会自动跳过。
// 后续健康检查会尝试续期。
func (p *Pool) ReportFailure(acct *Account) bool {
	if acct == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.entries {
		if a.ID == acct.ID {
			a.Status = StatusExpired
			a.FailedCalls++
			return true
		}
	}
	return false
}

// ExpiredAccounts 返回所有状态为 Expired 的账号（用于健康检查批量处理）。
func (p *Pool) ExpiredAccounts() []*Account {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []*Account
	for _, a := range p.entries {
		if a.Status == StatusExpired {
			out = append(out, a)
		}
	}
	return out
}

// TokenRenewer 续期回调函数，由 bootstrap 提供实现（避免 import 循环）。
// 返回 true 表示续期成功，false 表示失败。
type TokenRenewer func(acct *Account) bool

// StartHealthCheck 启动健康检查 goroutine。
// 每隔 interval 扫描所有过期账号，调用 renew 尝试续期。
// 返回一个 stop 函数，调用后可停止健康检查。
func (p *Pool) StartHealthCheck(interval time.Duration, renew TokenRenewer) func() {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.runHealthCheck(renew)
			case <-stop:
				return
			}
		}
	}()
	return func() { close(stop) }
}

func (p *Pool) runHealthCheck(renew TokenRenewer) {
	for _, acct := range p.ExpiredAccounts() {
		if acct.Status == StatusExpired && renew != nil {
			if renew(acct) {
				p.mu.Lock()
				acct.Status = StatusActive
				p.mu.Unlock()
				log.Printf("[health] account %s renewed successfully", acct.ID[:8])
			}
		}
	}
}
