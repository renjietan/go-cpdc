package netio

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
)

type Candidate struct {
	Name  string
	Index int
	IP    net.IP
	Mask  net.IPMask
}

func (c Candidate) IPString() string {
	return c.IP.To4().String()
}

func (c Candidate) SubnetMaskString() string {
	if len(c.Mask) != net.IPv4len {
		return ""
	}
	return net.IP(c.Mask).String()
}

func DiscoverCandidates() ([]Candidate, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var candidates []Candidate
	for _, iface := range interfaces {
		if !eligibleInterface(iface) {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("read addresses for %s: %w", iface.Name, err)
		}
		if candidate, ok := candidateForInterface(iface, addresses); ok {
			candidates = append(candidates, candidate)
		}
	}
	sortCandidates(candidates)
	return candidates, nil
}

func candidateForInterface(iface net.Interface, addresses []net.Addr) (Candidate, bool) {
	if !eligibleInterface(iface) {
		return Candidate{}, false
	}
	type address struct {
		ip   net.IP
		mask net.IPMask
	}
	var ipv4 []address
	for _, item := range addresses {
		ipNet, ok := item.(*net.IPNet)
		if !ok || ipNet.IP.To4() == nil || ipNet.IP.IsLoopback() || len(ipNet.Mask) != net.IPv4len {
			continue
		}
		ipv4 = append(ipv4, address{
			ip: append(net.IP(nil), ipNet.IP.To4()...), mask: append(net.IPMask(nil), ipNet.Mask...),
		})
	}
	if len(ipv4) == 0 {
		return Candidate{}, false
	}
	sort.Slice(ipv4, func(i, j int) bool { return bytesLess(ipv4[i].ip, ipv4[j].ip) })
	return Candidate{Name: iface.Name, Index: iface.Index, IP: ipv4[0].ip, Mask: ipv4[0].mask}, true
}

func eligibleInterface(iface net.Interface) bool {
	return iface.Flags&net.FlagUp != 0 &&
		iface.Flags&net.FlagBroadcast != 0 &&
		iface.Flags&net.FlagLoopback == 0 &&
		iface.Flags&net.FlagPointToPoint == 0
}

func ChooseCandidates(candidates []Candidate, requested string) ([]Candidate, error) {
	selected := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if requested == "" || strings.EqualFold(candidate.Name, requested) {
			selected = append(selected, candidate)
		}
	}
	if len(selected) == 0 {
		if requested != "" {
			return nil, fmt.Errorf("network interface %q has no eligible broadcast IPv4 address", requested)
		}
		return nil, errors.New("no eligible broadcast IPv4 interface")
	}
	if requested != "" && len(selected) != 1 {
		return nil, fmt.Errorf("network interface %q is not unique", requested)
	}
	sortCandidates(selected)
	return selected, nil
}

func SelectCandidates(requested string) ([]Candidate, error) {
	candidates, err := DiscoverCandidates()
	if err != nil {
		return nil, err
	}
	return ChooseCandidates(candidates, requested)
}

func sortCandidates(candidates []Candidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Index != candidates[j].Index {
			return candidates[i].Index < candidates[j].Index
		}
		if candidates[i].Name != candidates[j].Name {
			return candidates[i].Name < candidates[j].Name
		}
		return bytesLess(candidates[i].IP, candidates[j].IP)
	})
}

func bytesLess(a, b []byte) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
