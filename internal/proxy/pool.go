package proxy

import (
	"log"
	"net"
	"sync"
)

// Pool 管理代理 IP 的分配与回收。
// IPv4 从代理列表轮询，IPv6 从 CIDR 自动生成（TODO: IPv6 模式）。
type Pool struct {
	mu       sync.Mutex
	ipv4List []string
	ipv6CIDR string
	cursor   int
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
	if ip != "" {
		log.Printf("[proxy] released %s", ip)
	}
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
