package netio

import (
	"errors"
	"net"
	"testing"
)

type fakePacketWriter struct {
	err          error
	payloads     [][]byte
	destinations []string
	closed       int
}

type fakeRPFilterController struct {
	applied      [][]string
	applyErr     error
	restoreCalls int
}

func (c *fakeRPFilterController) Apply(interfaceNames []string) error {
	c.applied = append(c.applied, append([]string(nil), interfaceNames...))
	return c.applyErr
}

func (c *fakeRPFilterController) Restore() error {
	c.restoreCalls++
	return nil
}

func (w *fakePacketWriter) WriteToUDP(payload []byte, destination *net.UDPAddr) (int, error) {
	w.payloads = append(w.payloads, append([]byte(nil), payload...))
	w.destinations = append(w.destinations, destination.String())
	if w.err != nil {
		return 0, w.err
	}
	return len(payload), nil
}

func (w *fakePacketWriter) Close() error {
	w.closed++
	return nil
}

func TestWriteCopiesWritesEveryTarget(t *testing.T) {
	one := &fakePacketWriter{}
	two := &fakePacketWriter{}
	loopback := &fakePacketWriter{}
	payload := []byte("same datagram")
	targets := []sendTarget{
		{key: "eth0", name: "eth0", writer: one, destination: broadcastReply, broadcast: true},
		{key: "eth1", name: "eth1", writer: two, destination: broadcastReply, broadcast: true},
		{name: "loopback", writer: loopback, destination: loopbackReply},
	}

	report, err := writeCopies(payload, targets)
	if err != nil {
		t.Fatal(err)
	}
	if report.broadcastSuccesses != 2 || len(report.failedBroadcasts) != 0 {
		t.Fatalf("report = %+v", report)
	}
	for name, writer := range map[string]*fakePacketWriter{"eth0": one, "eth1": two, "loopback": loopback} {
		if len(writer.payloads) != 1 || string(writer.payloads[0]) != string(payload) {
			t.Fatalf("%s payloads = %q", name, writer.payloads)
		}
	}
	if one.destinations[0] != "255.255.255.255:39002" || two.destinations[0] != "255.255.255.255:39002" {
		t.Fatalf("broadcast destinations = %v, %v", one.destinations, two.destinations)
	}
	if loopback.destinations[0] != "127.0.0.1:39002" {
		t.Fatalf("loopback destination = %v", loopback.destinations)
	}
}

func TestWriteCopiesToleratesPartialFailure(t *testing.T) {
	failed := &fakePacketWriter{err: errors.New("network down")}
	working := &fakePacketWriter{}
	report, err := writeCopies([]byte("payload"), []sendTarget{
		{key: "failed", name: "failed", writer: failed, destination: broadcastReply, broadcast: true},
		{name: "working", writer: working, destination: loopbackReply},
	})
	if err != nil {
		t.Fatalf("partial failure returned %v", err)
	}
	if len(report.failedBroadcasts) != 1 || report.failedBroadcasts[0] != "failed" {
		t.Fatalf("failed broadcasts = %v", report.failedBroadcasts)
	}
	if len(failed.payloads) != 1 || len(working.payloads) != 1 {
		t.Fatalf("calls = failed %d, working %d", len(failed.payloads), len(working.payloads))
	}
}

func TestWriteCopiesFailsWhenEveryTargetFails(t *testing.T) {
	_, err := writeCopies([]byte("payload"), []sendTarget{
		{key: "one", name: "one", writer: &fakePacketWriter{err: errors.New("one failed")}, destination: broadcastReply, broadcast: true},
		{name: "two", writer: &fakePacketWriter{err: errors.New("two failed")}, destination: loopbackReply},
	})
	if err == nil {
		t.Fatal("all-target failure returned nil")
	}
}

func TestTransportOpenBroadcastsDisablesRPFilterBeforeOpeningSenders(t *testing.T) {
	controller := &fakeRPFilterController{}
	opened := 0
	transport := &Transport{
		rpFilter: controller,
		openSender: func(candidate Candidate) (packetSender, error) {
			if len(controller.applied) != 1 {
				t.Fatalf("rp_filter Apply calls before open = %d", len(controller.applied))
			}
			opened++
			return &fakePacketWriter{}, nil
		},
	}
	candidates := []Candidate{{Name: "eth0"}, {Name: "eth1"}}

	senders, err := transport.openBroadcasts(candidates)
	if err != nil {
		t.Fatal(err)
	}
	if opened != 2 || len(senders) != 2 {
		t.Fatalf("opened = %d, senders = %d", opened, len(senders))
	}
	if got := controller.applied[0]; len(got) != 2 || got[0] != "eth0" || got[1] != "eth1" {
		t.Fatalf("applied interfaces = %v", got)
	}
}

func TestTransportSendRefreshesFailedBroadcastsAndRetriesOnce(t *testing.T) {
	stale := &fakePacketWriter{err: errors.New("stale source address")}
	fresh := &fakePacketWriter{}
	loopback := &fakePacketWriter{}
	oldCandidate := Candidate{Name: "eth0", Index: 2, IP: net.ParseIP("192.168.2.2").To4(), Mask: net.CIDRMask(24, 32)}
	newCandidate := Candidate{Name: "eth0", Index: 2, IP: net.ParseIP("10.64.2.132").To4(), Mask: net.CIDRMask(24, 32)}
	selectorCalls := 0
	transport := &Transport{
		broadcasts:         []broadcastSender{{candidate: oldCandidate, connection: stale}},
		loopbackWriter:     loopback,
		requestedInterface: "eth0",
		selectCandidates: func(requested string) ([]Candidate, error) {
			selectorCalls++
			if requested != "eth0" {
				t.Fatalf("requested interface = %q", requested)
			}
			return []Candidate{newCandidate}, nil
		},
		openSender: func(candidate Candidate) (packetSender, error) {
			if candidate.IPString() != newCandidate.IPString() {
				t.Fatalf("opened candidate = %+v", candidate)
			}
			return fresh, nil
		},
	}
	payload := []byte("same protobuf datagram")

	if err := transport.Send(payload); err != nil {
		t.Fatal(err)
	}
	if selectorCalls != 1 || stale.closed != 1 {
		t.Fatalf("selector calls = %d, stale closes = %d", selectorCalls, stale.closed)
	}
	if len(stale.payloads) != 1 || len(fresh.payloads) != 1 || string(fresh.payloads[0]) != string(payload) {
		t.Fatalf("stale payloads = %q, fresh payloads = %q", stale.payloads, fresh.payloads)
	}
	if len(loopback.payloads) != 1 {
		t.Fatalf("loopback writes = %d, want 1", len(loopback.payloads))
	}
}

func TestTransportSendKeepsOldPoolWhenRefreshFails(t *testing.T) {
	stale := &fakePacketWriter{err: errors.New("stale source address")}
	transport := &Transport{
		broadcasts:     []broadcastSender{{candidate: Candidate{Name: "eth0"}, connection: stale}},
		loopbackWriter: &fakePacketWriter{},
		selectCandidates: func(string) ([]Candidate, error) {
			return nil, errors.New("enumeration failed")
		},
		openSender: func(Candidate) (packetSender, error) {
			t.Fatal("sender opener called after enumeration failure")
			return nil, nil
		},
	}

	if err := transport.Send([]byte("payload")); err == nil {
		t.Fatal("send succeeded without a physical broadcast")
	}
	if stale.closed != 0 || len(transport.broadcasts) != 1 {
		t.Fatalf("old pool changed: closes=%d senders=%d", stale.closed, len(transport.broadcasts))
	}
}

func TestTransportSendKeepsOldPoolWhenRPFilterRefreshFails(t *testing.T) {
	stale := &fakePacketWriter{err: errors.New("stale source address")}
	controller := &fakeRPFilterController{applyErr: errors.New("permission denied")}
	transport := &Transport{
		broadcasts:     []broadcastSender{{candidate: Candidate{Name: "eth0"}, connection: stale}},
		loopbackWriter: &fakePacketWriter{},
		rpFilter:       controller,
		selectCandidates: func(string) ([]Candidate, error) {
			return []Candidate{{Name: "eth1"}}, nil
		},
		openSender: func(Candidate) (packetSender, error) {
			t.Fatal("sender opened after rp_filter preparation failure")
			return nil, nil
		},
	}

	if err := transport.Send([]byte("payload")); err == nil {
		t.Fatal("send succeeded after rp_filter preparation failure")
	}
	if stale.closed != 0 || len(transport.broadcasts) != 1 {
		t.Fatalf("old pool changed: closes=%d senders=%d", stale.closed, len(transport.broadcasts))
	}
	if len(controller.applied) != 1 || controller.applied[0][0] != "eth1" {
		t.Fatalf("rp_filter calls = %v", controller.applied)
	}
}

func TestTransportSendPrunesRetryFailures(t *testing.T) {
	stale := &fakePacketWriter{err: errors.New("stale source address")}
	working := &fakePacketWriter{}
	disconnected := &fakePacketWriter{err: errors.New("network down")}
	loopback := &fakePacketWriter{}
	selectorCalls := 0
	transport := &Transport{
		broadcasts:     []broadcastSender{{candidate: Candidate{Name: "old"}, connection: stale}},
		loopbackWriter: loopback,
		selectCandidates: func(string) ([]Candidate, error) {
			selectorCalls++
			return []Candidate{{Name: "working", Index: 1}, {Name: "disconnected", Index: 2}}, nil
		},
		openSender: func(candidate Candidate) (packetSender, error) {
			if candidate.Name == "working" {
				return working, nil
			}
			return disconnected, nil
		},
	}

	if err := transport.Send([]byte("first")); err != nil {
		t.Fatal(err)
	}
	if len(transport.broadcasts) != 1 || transport.broadcasts[0].candidate.Name != "working" {
		t.Fatalf("remaining senders = %+v", transport.broadcasts)
	}
	if disconnected.closed != 1 || stale.closed != 1 {
		t.Fatalf("closed: disconnected=%d stale=%d", disconnected.closed, stale.closed)
	}
	if err := transport.Send([]byte("second")); err != nil {
		t.Fatal(err)
	}
	if selectorCalls != 1 {
		t.Fatalf("selector calls = %d, want 1", selectorCalls)
	}
	if len(loopback.payloads) != 2 {
		t.Fatalf("loopback writes = %d, want 2", len(loopback.payloads))
	}
}

func TestTransportCloseRestoresRPFilter(t *testing.T) {
	controller := &fakeRPFilterController{}
	transport := &Transport{
		broadcasts: []broadcastSender{{candidate: Candidate{Name: "eth0"}, connection: &fakePacketWriter{}}},
		rpFilter:   controller,
	}

	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	if controller.restoreCalls != 1 {
		t.Fatalf("Restore calls = %d", controller.restoreCalls)
	}
}
