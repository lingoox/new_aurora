package accounts

import (
	"errors"
	"sync"
)

var ErrNoAvailable = errors.New("no available account of the requested type")

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

// ReportFailure 标记账号为不可用（如 token 过期），Acquire 时会自动跳过。
// 返回 true 表示成功标记，false 表示账号不在池中。
func (p *Pool) ReportFailure(acct *Account) bool {
	if acct == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, a := range p.accounts {
		if a.ID == acct.ID {
			a.Status = StatusDisabled
			a.FailedCalls++
			return true
		}
	}
	return false
}
