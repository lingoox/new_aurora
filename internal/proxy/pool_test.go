package proxy

import (
	"math/big"
	"net"
	"testing"
)

func TestPoolAllocateAndRelease(t *testing.T) {
	p := NewPool([]string{"http://proxy1:8080", "http://proxy2:8080", "http://proxy3:8080"}, "")

	ip1 := p.Allocate()
	ip2 := p.Allocate()
	ip3 := p.Allocate()
	ip4 := p.Allocate()

	if ip1 == ip2 || ip2 == ip3 {
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

func TestIPv6Allocate(t *testing.T) {
	p := NewPool(nil, "2001:db8::/120")
	used := make(map[string]bool)
	for i := 0; i < 10; i++ {
		ip := p.Allocate()
		if ip == "" {
			t.Fatalf("Allocate returned empty at iteration %d", i)
		}
		if !IsIPv6(ip) {
			t.Fatalf("Allocate returned non-IPv6: %s", ip)
		}
		if used[ip] {
			t.Fatalf("Duplicate IPv6 address: %s", ip)
		}
		used[ip] = true
	}
}

func TestIPv6Release(t *testing.T) {
	p := NewPool(nil, "2001:db8::/120")
	ip1 := p.Allocate()
	ip2 := p.Allocate()
	if ip1 == ip2 {
		t.Fatalf("got same IP twice: %s", ip1)
	}
	p.Release(ip1)
	// 释放后再分配，应该拿到相同 IP（或不同，都行，不 panic 即可）
	_ = p.Allocate()
}

func TestNthIPv6(t *testing.T) {
	base := net.ParseIP("2001:db8::")
	if base == nil {
		t.Fatal("cannot parse base IP")
	}
	ip1, err := nthIPv6(base, big.NewInt(1))
	if err != nil || ip1 == nil {
		t.Fatalf("nthIPv6(1) failed: %v", err)
	}
	if ip1.String() != "2001:db8::1" {
		t.Errorf("nthIPv6(1) = %s, want 2001:db8::1", ip1.String())
	}
	ip100, err := nthIPv6(base, big.NewInt(256))
	if err != nil || ip100 == nil {
		t.Fatalf("nthIPv6(256) failed: %v", err)
	}
	if ip100.String() != "2001:db8::100" {
		t.Errorf("nthIPv6(256) = %s, want 2001:db8::100", ip100.String())
	}
}

func TestIsIPv6(t *testing.T) {
	if !IsIPv6("2001:db8::1") {
		t.Error("IsIPv6 should be true for IPv6 address")
	}
	if IsIPv6("192.168.1.1") {
		t.Error("IsIPv6 should be false for IPv4 address")
	}
	if IsIPv6("not-an-ip") {
		t.Error("IsIPv6 should be false for invalid input")
	}
	if IsIPv6("") {
		t.Error("IsIPv6 should be false for empty string")
	}
}

func TestIPv6PoolIgnoresIPv4(t *testing.T) {
	p := NewPool(nil, "2001:db8::/64")
	// 不传 IPv4 代理，IPv6 应该正常工作
	ip := p.Allocate()
	if ip == "" || !IsIPv6(ip) {
		t.Fatalf("IPv6 allocate should work without IPv4 proxies, got %q", ip)
	}
}
