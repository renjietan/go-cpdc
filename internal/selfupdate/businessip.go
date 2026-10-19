package selfupdate

import (
	"errors"
	"net"
)

// networkResolver 是包变量，便于测试替换。返回 (业务网卡 IP, CPDS 源是否在
// 本地网段, error)。
var networkResolver = resolveNetwork

// resolveNetwork 优先使用读包时拿到的接收网卡 IP（IP_PKTINFO 的 spec_dst）；
// 拿不到（非 Linux 或未开启）则回退 resolveBusinessIP 的网段匹配。
func resolveNetwork(addr *net.UDPAddr, localIP net.IP) (string, bool, error) {
	if localIP != nil && localIP.To4() != nil {
		_, err := matchLocalIPForSource(addr.IP)
		return localIP.String(), err == nil, nil
	}
	return resolveBusinessIP(addr.IP)
}

// resolveBusinessIP 判定自更新使用的业务网卡 IP，并报告 CPDS 源地址是否落在
// 本地网段（onSubnet）。判定顺序：
//  1. 源网段匹配：与 CPDS 源 IP 同网段的本地网卡（onSubnet=true）；
//  2. 默认路由网卡（仅 Linux 实现，非 Linux 平台跳过）；
//  3. 兜底：现有 candidates[0] 启发式。
func resolveBusinessIP(src net.IP) (ip string, onSubnet bool, err error) {
	if ip, err := matchLocalIPForSource(src); err == nil && ip != "" {
		return ip, true, nil
	}
	if ip, err := defaultRouteIP(); err == nil && ip != "" {
		return ip, false, nil
	}
	ip, err = detectLocalIP()
	if err != nil {
		return "", false, err
	}
	return ip, false, nil
}

// matchLocalIPForSource 在本地 IPv4 中查找与 src 同网段的网卡 IP。
// 排除回环与链路本地（169.254.x）地址；多个命中时取最长前缀（最精确）的网卡。
func matchLocalIPForSource(src net.IP) (string, error) {
	src4 := src.To4()
	if src4 == nil || src4.IsUnspecified() {
		return "", errors.New("invalid source address")
	}
	candidates, err := localIPv4Candidates()
	if err != nil {
		return "", err
	}
	best := bestLocalIPForSource(src4, candidates)
	if best == "" {
		return "", errors.New("no local IPv4 on the source subnet")
	}
	return best, nil
}

// localIPCandidate 是一张本地网卡上的一个 IPv4 及其子网掩码。
type localIPCandidate struct {
	ip   net.IP
	mask net.IPMask
}

// localIPv4Candidates 枚举所有 UP 状态网卡上的非回环、非链路本地 IPv4。
func localIPv4Candidates() ([]localIPCandidate, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var candidates []localIPCandidate
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil || ip4.IsLoopback() || isLinkLocalIPv4(ip4) {
				continue
			}
			mask4 := ipNet.Mask
			if len(mask4) != net.IPv4len {
				continue
			}
			candidates = append(candidates, localIPCandidate{ip: ip4, mask: mask4})
		}
	}
	return candidates, nil
}

// bestLocalIPForSource 在候选地址中选择与 src 同网段、前缀最长的本地 IP；
// 无匹配返回空字符串。
func bestLocalIPForSource(src net.IP, candidates []localIPCandidate) string {
	best := ""
	bestPrefix := -1
	for _, candidate := range candidates {
		if !ipv4And(src, candidate.mask).Equal(ipv4And(candidate.ip, candidate.mask)) {
			continue
		}
		ones, _ := candidate.mask.Size()
		if ones > bestPrefix {
			bestPrefix = ones
			best = candidate.ip.String()
		}
	}
	return best
}

// ipv4And 计算 IPv4 地址与掩码的按位与。
func ipv4And(ip net.IP, mask net.IPMask) net.IP {
	v4 := ip.To4()
	m4 := mask
	if len(m4) == net.IPv6len {
		m4 = m4[12:]
	}
	out := make(net.IP, net.IPv4len)
	for i := 0; i < net.IPv4len; i++ {
		out[i] = v4[i] & m4[i]
	}
	return out
}

// isLinkLocalIPv4 判断是否为链路本地地址 169.254.0.0/16。
func isLinkLocalIPv4(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	return ip4[0] == 169 && ip4[1] == 254
}
