package proxy

import (
	"fmt"
	"log"
	"math/big"
	"net"
	"sync"
)

// Pool 管理代理 IP 的分配与回收。
// IPv4 从代理列表轮询，IPv6 从 CIDR 自动生成。
type Pool struct {
	mu       sync.Mutex
	cursor   int // IPv4 轮询游标
	ipv4List []string
	ipv6CIDR string

	// IPv6 状态
	ipv6Net    *net.IPNet
	ipv6Base   net.IP
	ipv6Count  *big.Int // 子网内地址总数
	ipv6Cursor *big.Int // 下一个分配序号
	ipv6Used   map[string]bool
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
// IPv6 模式：从 CIDR 生成唯一 IP。
// IPv4 模式：从现有列表 round-robin。
func (p *Pool) Allocate() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ipv6CIDR != "" {
		ip := p.allocateIPv6()
		if ip != "" {
			return ip
		}
	}

	if len(p.ipv4List) == 0 {
		return ""
	}

	ip := p.ipv4List[p.cursor]
	p.cursor = (p.cursor + 1) % len(p.ipv4List)
	return ip
}

// Release 回收一个代理 IP。
func (p *Pool) Release(ip string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ip != "" {
		if IsIPv6IP(ip) {
			p.releaseIPv6(ip)
		}
		log.Printf("[proxy] released %s", ip)
	}
}

// Count 返回可用 IPv4 代理数量。
func (p *Pool) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.ipv4List)
}

// ─── IPv6 ───────────────────────────────────────────────────────

// initIPv6 解析 CIDR，初始化 IPv6 子网计算。
func (p *Pool) initIPv6() error {
	if p.ipv6Net != nil {
		return nil
	}
	_, ipNet, err := net.ParseCIDR(p.ipv6CIDR)
	if err != nil {
		return fmt.Errorf("invalid IPv6 CIDR %q: %w", p.ipv6CIDR, err)
	}
	p.ipv6Net = ipNet
	p.ipv6Base = ipNet.IP
	p.ipv6Count = subnetSize(ipNet)
	p.ipv6Cursor = big.NewInt(0)
	p.ipv6Used = make(map[string]bool)
	return nil
}

// allocateIPv6 从子网生成下一个未使用的 IPv6 地址。
func (p *Pool) allocateIPv6() string {
	if err := p.initIPv6(); err != nil {
		log.Printf("[proxy] ipv6 init error: %v", err)
		return ""
	}

	maxAttempts := 100
	for i := 0; i < maxAttempts; i++ {
		ip, err := nthIPv6(p.ipv6Base, p.ipv6Cursor)
		if err != nil {
			p.ipv6Cursor = big.NewInt(1)
			continue
		}
		ipStr := ip.String()
		if !p.ipv6Used[ipStr] {
			p.ipv6Used[ipStr] = true
			p.ipv6Cursor.Add(p.ipv6Cursor, big.NewInt(1))
			return ipStr
		}
		p.ipv6Cursor.Add(p.ipv6Cursor, big.NewInt(1))
		// 循环回到子网开头
		if p.ipv6Cursor.Cmp(p.ipv6Count) >= 0 {
			p.ipv6Cursor = big.NewInt(1)
		}
	}
	return ""
}

// releaseIPv6 释放一个 IPv6 地址供重新使用。
func (p *Pool) releaseIPv6(ip string) {
	delete(p.ipv6Used, ip)
}

// ─── Helper ─────────────────────────────────────────────────────

// IsIPv6 判断 IP 字符串是否为 IPv6 地址。
func IsIPv6(ip string) bool {
	return IsIPv6IP(ip)
}

// IsIPv6IP 判断 IP 字符串是否为 IPv6 地址。
func IsIPv6IP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	return parsed.To4() == nil && parsed.To16() != nil
}

// IPv6Count 返回子网内可用地址总数。
func (p *Pool) IPv6Count() *big.Int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ipv6CIDR == "" {
		return big.NewInt(0)
	}
	if err := p.initIPv6(); err != nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(p.ipv6Count)
}

// nthIPv6 返回子网中的第 N 个 IP（从 1 开始）。
// base 是子网第一个可用地址，n 是序号。
func nthIPv6(base net.IP, n *big.Int) (net.IP, error) {
	if n == nil || n.Sign() <= 0 {
		return nil, fmt.Errorf("invalid index")
	}
	ip := make(net.IP, len(base))
	copy(ip, base)
	// 从最后 8 字节加 n
	octetLen := len(ip)
	carry := new(big.Int).Set(n)
	for i := octetLen - 1; i >= 0 && carry.Sign() > 0; i-- {
		sum := big.NewInt(int64(ip[i]))
		sum.Add(sum, carry)
		ip[i] = byte(sum.Int64() & 0xff)
		carry.Rsh(carry, 8)
	}
	return ip, nil
}

// subnetSize 返回子网内的地址总数。超过 /64 时限制为 /64 的容量。
func subnetSize(ipNet *net.IPNet) *big.Int {
	ones, bits := ipNet.Mask.Size()
	hostBits := bits - ones
	if hostBits <= 0 {
		return big.NewInt(1)
	}
	if hostBits > 64 {
		hostBits = 64 // 防止溢出，/64 足够大
	}
	size := new(big.Int).Lsh(big.NewInt(1), uint(hostBits))
	return size
}
