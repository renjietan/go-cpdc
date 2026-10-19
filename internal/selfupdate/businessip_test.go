package selfupdate

import (
	"net"
	"testing"
)

func TestIPv4And(t *testing.T) {
	ip := net.IPv4(192, 168, 1, 10)
	mask := net.IPMask(net.ParseIP("255.255.255.0").To4())
	if got := ipv4And(ip, mask).String(); got != "192.168.1.0" {
		t.Fatalf("ipv4And = %q, want 192.168.1.0", got)
	}
}

func TestIsLinkLocalIPv4(t *testing.T) {
	if !isLinkLocalIPv4(net.IPv4(169, 254, 1, 2)) {
		t.Fatal("169.254.1.2 should be link-local")
	}
	if isLinkLocalIPv4(net.IPv4(10, 0, 0, 1)) {
		t.Fatal("10.0.0.1 should not be link-local")
	}
}

func TestMatchLocalIPForSourceRejectsInvalidSource(t *testing.T) {
	if _, err := matchLocalIPForSource(nil); err == nil {
		t.Fatal("nil source should fail")
	}
	if _, err := matchLocalIPForSource(net.IPv4zero); err == nil {
		t.Fatal("0.0.0.0 source should fail")
	}
	if _, err := matchLocalIPForSource(net.ParseIP("::1")); err == nil {
		t.Fatal("IPv6 loopback source should fail")
	}
}

func TestBestLocalIPForSource(t *testing.T) {
	mask24 := net.IPMask(net.ParseIP("255.255.255.0").To4())
	mask16 := net.IPMask(net.ParseIP("255.255.0.0").To4())

	candidates := []localIPCandidate{
		{ip: net.IPv4(10, 32, 3, 135), mask: mask24},
		{ip: net.IPv4(10, 32, 3, 136), mask: mask24},
		{ip: net.IPv4(10, 32, 0, 1), mask: mask16},
	}

	// 与 10.32.3.135 同网段（/24），且 10.32.3.136 也同网段时取前缀最长；
	// 这里两个 /24 候选前缀相等，取先出现者。
	if got := bestLocalIPForSource(net.IPv4(10, 32, 3, 200), candidates); got != "10.32.3.135" {
		t.Fatalf("best = %q, want 10.32.3.135", got)
	}

	// 只有 /16 候选命中时选它。
	candidates = []localIPCandidate{{ip: net.IPv4(10, 32, 0, 1), mask: mask16}}
	if got := bestLocalIPForSource(net.IPv4(10, 32, 9, 9), candidates); got != "10.32.0.1" {
		t.Fatalf("best = %q, want 10.32.0.1", got)
	}

	// 无同网段候选 → 空。
	if got := bestLocalIPForSource(net.IPv4(172, 16, 0, 1), candidates); got != "" {
		t.Fatalf("best = %q, want empty", got)
	}
}

func TestResolveNetworkPrefersLocalIP(t *testing.T) {
	addr := &net.UDPAddr{IP: net.IPv4(10, 32, 3, 4)}
	localIP := net.IPv4(192, 168, 2, 104)
	ip, _, err := resolveNetwork(addr, localIP)
	if err != nil {
		t.Fatalf("resolveNetwork = %v", err)
	}
	if ip != "192.168.2.104" {
		t.Fatalf("ip = %q, want 192.168.2.104", ip)
	}
}
