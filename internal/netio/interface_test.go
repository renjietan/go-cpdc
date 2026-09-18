package netio

import (
	"net"
	"testing"
)

func TestChooseCandidates(t *testing.T) {
	one := Candidate{Name: "Ethernet", Index: 4, IP: net.ParseIP("10.20.30.40").To4(), Mask: net.CIDRMask(24, 32)}
	two := Candidate{Name: "USB LAN", Index: 7, IP: net.ParseIP("192.168.1.2").To4(), Mask: net.CIDRMask(16, 32)}

	got, err := ChooseCandidates([]Candidate{two, one}, "")
	if err != nil || len(got) != 2 || got[0].Index != one.Index || got[1].Index != two.Index {
		t.Fatalf("automatic candidates = %+v, %v", got, err)
	}
	got, err = ChooseCandidates([]Candidate{one, two}, "USB LAN")
	if err != nil || len(got) != 1 || got[0].Index != two.Index {
		t.Fatalf("explicit candidate = %+v, %v", got, err)
	}
	if _, err := ChooseCandidates([]Candidate{one}, "missing"); err == nil {
		t.Fatal("missing explicit interface succeeded")
	}
	if _, err := ChooseCandidates(nil, ""); err == nil {
		t.Fatal("empty candidates succeeded")
	}
}

func TestCandidateForInterfaceFiltersFlagsAndSelectsOneIPv4(t *testing.T) {
	eligible := net.Interface{Name: "eth0", Index: 4, Flags: net.FlagUp | net.FlagBroadcast}
	addresses := []net.Addr{
		&net.IPNet{IP: net.ParseIP("192.168.1.20").To4(), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("10.64.0.20").To4(), Mask: net.CIDRMask(16, 32)},
		&net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(64, 128)},
	}
	candidate, ok := candidateForInterface(eligible, addresses)
	if !ok || candidate.Name != "eth0" || candidate.IPString() != "10.64.0.20" || candidate.SubnetMaskString() != "255.255.0.0" {
		t.Fatalf("candidate = %+v, ok=%v", candidate, ok)
	}

	for name, flags := range map[string]net.Flags{
		"down":           net.FlagBroadcast,
		"no broadcast":   net.FlagUp,
		"loopback":       net.FlagUp | net.FlagBroadcast | net.FlagLoopback,
		"point to point": net.FlagUp | net.FlagBroadcast | net.FlagPointToPoint,
	} {
		t.Run(name, func(t *testing.T) {
			iface := eligible
			iface.Flags = flags
			if _, ok := candidateForInterface(iface, addresses); ok {
				t.Fatal("ineligible interface produced a candidate")
			}
		})
	}
	if _, ok := candidateForInterface(eligible, addresses[2:]); ok {
		t.Fatal("IPv6-only interface produced a candidate")
	}
}

func TestCandidateAddressStrings(t *testing.T) {
	candidate := Candidate{
		Name: "eth0", IP: net.IPv4(10, 64, 1, 7).To4(), Mask: net.CIDRMask(20, 32),
	}
	if candidate.IPString() != "10.64.1.7" {
		t.Fatalf("IPString = %q", candidate.IPString())
	}
	if candidate.SubnetMaskString() != "255.255.240.0" {
		t.Fatalf("SubnetMaskString = %q", candidate.SubnetMaskString())
	}
}

func TestReplyDestinationsAreBroadcastAndLoopback(t *testing.T) {
	destinations := ReplyDestinations()
	if len(destinations) != 2 {
		t.Fatalf("destination count = %d, want 2", len(destinations))
	}
	if destinations[0].String() != "255.255.255.255:39002" {
		t.Fatalf("broadcast = %v", destinations[0])
	}
	if destinations[1].String() != "127.0.0.1:39002" {
		t.Fatalf("loopback = %v", destinations[1])
	}
}
