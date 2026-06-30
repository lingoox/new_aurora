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
	ID   string
	Type AccountType

	// 认证
	Token        string // access_token 或 UUID
	RefreshToken string // 仅 free/puid 有

	// 身份
	PUID       string
	TeamUserID string

	// 隔离单元（每个账号独立）
	Client      interface{}        // *bogdanfinn.TlsClient — 避免 import 循环
	Proxy       string             // 专属代理 IP
	Fingerprint BrowserFingerprint // 专属指纹

	// WSS (free/puid 有, noauth 无)
	WSSActor interface{} // *WSSActor — 避免 import 循环

	// 状态
	Status    AccountStatus
	ExpiresAt time.Time

	// 统计
	TotalCalls  int64
	FailedCalls int64
	LastUsed    time.Time
	LastChecked time.Time
	CreatedAt   time.Time
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
