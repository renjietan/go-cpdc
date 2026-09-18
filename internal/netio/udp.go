package netio

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

const (
	ReceivePort = 39001
	ReplyPort   = 39002
)

var (
	broadcastReply = &net.UDPAddr{IP: net.IPv4bcast, Port: ReplyPort}
	loopbackReply  = &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: ReplyPort}
)

type Transport struct {
	receiver           *net.UDPConn
	loopbackWriter     packetWriter
	broadcasts         []broadcastSender
	requestedInterface string
	selectCandidates   func(string) ([]Candidate, error)
	openSender         func(Candidate) (packetSender, error)
	rpFilter           rpFilterController
	writeMu            sync.Mutex
}

type broadcastSender struct {
	candidate  Candidate
	connection packetSender
}

type packetWriter interface {
	WriteToUDP([]byte, *net.UDPAddr) (int, error)
}

type packetSender interface {
	packetWriter
	Close() error
}

type sendTarget struct {
	key         string
	name        string
	writer      packetWriter
	destination *net.UDPAddr
	broadcast   bool
}

type writeReport struct {
	broadcastSuccesses int
	failedBroadcasts   []string
	failures           []error
}

func Listen(candidates []Candidate, requestedInterface string) (*Transport, error) {
	if len(candidates) == 0 {
		return nil, errors.New("no broadcast interface selected")
	}
	receiver, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: ReceivePort})
	if err != nil {
		return nil, fmt.Errorf("listen UDP %d: %w", ReceivePort, err)
	}
	if err := receiver.SetReadBuffer(4 << 20); err != nil {
		_ = receiver.Close()
		return nil, fmt.Errorf("set UDP receive buffer: %w", err)
	}
	transport := &Transport{
		receiver:           receiver,
		loopbackWriter:     receiver,
		requestedInterface: requestedInterface,
		selectCandidates:   SelectCandidates,
		openSender:         openBroadcastSender,
		rpFilter:           newPlatformRPFilterController(),
	}
	broadcasts, err := transport.openBroadcasts(candidates)
	if err != nil {
		_ = receiver.Close()
		return nil, errors.Join(err, transport.rpFilter.Restore())
	}
	transport.broadcasts = broadcasts
	return transport, nil
}

func (t *Transport) openBroadcasts(candidates []Candidate) ([]broadcastSender, error) {
	if t.rpFilter != nil {
		if err := t.rpFilter.Apply(candidateInterfaceNames(candidates)); err != nil {
			return nil, fmt.Errorf("configure rp_filter for broadcast interfaces: %w", err)
		}
	}
	broadcasts := make([]broadcastSender, 0, len(candidates))
	var initializationErrors []error
	for _, candidate := range candidates {
		connection, err := t.openSender(candidate)
		if err != nil {
			initializationErrors = append(initializationErrors, err)
			slog.Warn("skipping broadcast interface",
				"interface", candidate.Name,
				"ip", candidate.IPString(),
				"error_type", "network_interface_initialization",
				"error", err,
			)
			continue
		}
		broadcasts = append(broadcasts, broadcastSender{candidate: candidate, connection: connection})
	}
	if len(broadcasts) == 0 {
		if len(initializationErrors) == 0 {
			return nil, errors.New("initialize broadcast senders: no candidate")
		}
		return nil, fmt.Errorf("initialize broadcast senders: %w", errors.Join(initializationErrors...))
	}
	return broadcasts, nil
}

func openBroadcastSender(candidate Candidate) (packetSender, error) {
	connection, err := net.ListenUDP("udp4", &net.UDPAddr{IP: candidate.IP.To4(), Port: 0})
	if err != nil {
		return nil, fmt.Errorf("bind %s (%s): %w", candidate.Name, candidate.IPString(), err)
	}
	if err := enableBroadcast(connection); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("enable broadcast on %s: %w", candidate.Name, err)
	}
	if err := connection.SetWriteBuffer(1 << 20); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("set send buffer on %s: %w", candidate.Name, err)
	}
	return connection, nil
}

func (t *Transport) Read(deadline time.Time) ([]byte, *net.UDPAddr, error) {
	if err := t.receiver.SetReadDeadline(deadline); err != nil {
		return nil, nil, err
	}
	buffer := make([]byte, 64*1024)
	n, source, err := t.receiver.ReadFromUDP(buffer)
	if err != nil {
		return nil, nil, err
	}
	return append([]byte(nil), buffer[:n]...), source, nil
}

// Send emits one broadcast copy per usable interface and exactly one loopback
// copy. CPDC never forwards received packets, so delivery cannot recurse.
func (t *Transport) Send(datagram []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	targets := t.broadcastTargets()
	if t.loopbackWriter != nil {
		targets = append(targets, sendTarget{name: "loopback", writer: t.loopbackWriter, destination: loopbackReply})
	}
	firstReport, firstErr := writeCopies(datagram, targets)
	if len(firstReport.failedBroadcasts) == 0 {
		if firstReport.broadcastSuccesses > 0 {
			return nil
		}
		if firstErr != nil {
			return firstErr
		}
		return errors.New("no UDP broadcast target")
	}

	if err := t.refreshBroadcasts(); err != nil {
		slog.Warn("refresh UDP broadcast senders failed", "error_type", "network_interface_refresh", "error", err)
		if firstReport.broadcastSuccesses > 0 {
			return nil
		}
		return errors.Join(errors.Join(firstReport.failures...), err)
	}

	retryReport, retryErr := writeCopies(datagram, t.broadcastTargets())
	if retryReport.broadcastSuccesses > 0 {
		t.pruneFailedBroadcasts(retryReport.failedBroadcasts)
		return nil
	}
	if firstReport.broadcastSuccesses > 0 {
		return nil
	}
	return errors.Join(errors.Join(firstReport.failures...), retryErr)
}

func (t *Transport) broadcastTargets() []sendTarget {
	targets := make([]sendTarget, 0, len(t.broadcasts))
	for _, sender := range t.broadcasts {
		targets = append(targets, sendTarget{
			key:         candidateKey(sender.candidate),
			name:        sender.candidate.Name + " (" + sender.candidate.IPString() + ")",
			writer:      sender.connection,
			destination: broadcastReply,
			broadcast:   true,
		})
	}
	return targets
}

func (t *Transport) refreshBroadcasts() error {
	if t.selectCandidates == nil || t.openSender == nil {
		return errors.New("UDP broadcast refresh is not configured")
	}
	candidates, err := t.selectCandidates(t.requestedInterface)
	if err != nil {
		return fmt.Errorf("select refreshed broadcast interfaces: %w", err)
	}
	refreshed, err := t.openBroadcasts(candidates)
	if err != nil {
		return err
	}
	old := t.broadcasts
	t.broadcasts = refreshed
	closeBroadcasts(old)
	slog.Info("UDP broadcast senders refreshed", "interfaces", len(refreshed))
	return nil
}

func (t *Transport) pruneFailedBroadcasts(failed []string) {
	if len(failed) == 0 {
		return
	}
	failedSet := make(map[string]struct{}, len(failed))
	for _, key := range failed {
		failedSet[key] = struct{}{}
	}
	kept := t.broadcasts[:0]
	for _, sender := range t.broadcasts {
		if _, failed := failedSet[candidateKey(sender.candidate)]; !failed {
			kept = append(kept, sender)
			continue
		}
		if err := sender.connection.Close(); err != nil {
			slog.Warn("close failed UDP broadcast sender", "interface", sender.candidate.Name, "ip", sender.candidate.IPString(), "error", err)
		}
	}
	t.broadcasts = kept
}

func candidateKey(candidate Candidate) string {
	return fmt.Sprintf("%d|%s|%s", candidate.Index, candidate.Name, candidate.IPString())
}

func writeCopies(datagram []byte, targets []sendTarget) (writeReport, error) {
	report := writeReport{}
	successes := 0
	for _, target := range targets {
		n, err := target.writer.WriteToUDP(datagram, target.destination)
		if err == nil && n != len(datagram) {
			err = io.ErrShortWrite
		}
		if err != nil {
			wrapped := fmt.Errorf("send to %s: %w", target.name, err)
			report.failures = append(report.failures, wrapped)
			if target.broadcast {
				report.failedBroadcasts = append(report.failedBroadcasts, target.key)
			}
			slog.Warn("UDP send target failed", "target", target.name, "error_type", "network_send", "error", err)
			continue
		}
		successes++
		if target.broadcast {
			report.broadcastSuccesses++
		}
	}
	if successes > 0 {
		return report, nil
	}
	if len(report.failures) == 0 {
		return report, errors.New("no UDP send target")
	}
	return report, errors.Join(report.failures...)
}

func (t *Transport) Close() error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	closeErrors := make([]error, 0, len(t.broadcasts)+2)
	for _, sender := range t.broadcasts {
		closeErrors = append(closeErrors, sender.connection.Close())
	}
	if t.receiver != nil {
		closeErrors = append(closeErrors, t.receiver.Close())
	}
	if t.rpFilter != nil {
		closeErrors = append(closeErrors, t.rpFilter.Restore())
	}
	return errors.Join(closeErrors...)
}

func candidateInterfaceNames(candidates []Candidate) []string {
	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		names = append(names, candidate.Name)
	}
	return names
}

func closeBroadcasts(senders []broadcastSender) {
	for _, sender := range senders {
		if err := sender.connection.Close(); err != nil {
			slog.Warn("close old UDP broadcast sender", "interface", sender.candidate.Name, "ip", sender.candidate.IPString(), "error", err)
		}
	}
}

func ReplyDestinations() []*net.UDPAddr {
	return []*net.UDPAddr{
		{IP: append(net.IP(nil), broadcastReply.IP...), Port: broadcastReply.Port},
		{IP: append(net.IP(nil), loopbackReply.IP...), Port: loopbackReply.Port},
	}
}
