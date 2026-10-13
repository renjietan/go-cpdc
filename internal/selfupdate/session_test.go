package selfupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"hash/crc32"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var testAddr = &net.UDPAddr{IP: net.IPv4(192, 168, 1, 20), Port: 54321}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

type fakeWrite struct {
	data []byte
	addr *net.UDPAddr
}

type fakeRead struct {
	data []byte
	addr *net.UDPAddr
	err  error
}

type fakeConn struct {
	mu        sync.Mutex
	writes    []fakeWrite
	reads     chan fakeRead
	deadlines []time.Time
}

func (c *fakeConn) WriteToUDP(data []byte, addr *net.UDPAddr) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, fakeWrite{data: append([]byte(nil), data...), addr: addr})
	return len(data), nil
}

func (c *fakeConn) snapshotWrites() []fakeWrite {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]fakeWrite(nil), c.writes...)
}

func (c *fakeConn) ReadFromUDP(buf []byte) (int, *net.UDPAddr, error) {
	select {
	case r := <-c.reads:
		if r.err != nil {
			return 0, nil, r.err
		}
		copy(buf, r.data)
		return len(r.data), r.addr, nil
	default:
		return 0, nil, timeoutErr{}
	}
}

func (c *fakeConn) SetReadDeadline(t time.Time) error {
	c.deadlines = append(c.deadlines, t)
	return nil
}

func (c *fakeConn) Close() error { return nil }

func TestSessionHandleScanResetsAndReplies(t *testing.T) {
	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	s := newSession(testDevice, conn)
	s.assembler.SetHeader(1, 4, 0, "x.zip")
	s.assembler.Put(1, []byte("data"))
	s.targetVersion = "1.2.3.4"

	s.handle(testAddr, []byte("Scan"))

	if s.assembler.packetCount != 0 {
		t.Fatalf("assembler not reset: %+v", s.assembler)
	}
	if s.targetVersion != "" {
		t.Fatalf("targetVersion not reset: %q", s.targetVersion)
	}
	assertSingleWrite(t, conn, "Scan:success")
}

func TestSessionHandleAuthAndVersion(t *testing.T) {
	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	s := newSession(testDevice, conn)

	s.handle(testAddr, []byte("auth"))
	if len(conn.writes) != 1 || !bytes.HasPrefix(conn.writes[0].data, []byte("authAck:")) {
		t.Fatalf("auth writes = %v", conn.writes)
	}

	conn.writes = nil
	s.handle(testAddr, []byte("version:9.9.9.9"))
	if len(conn.writes) != 1 || !bytes.HasPrefix(conn.writes[0].data, []byte("version_ok:")) {
		t.Fatalf("version writes = %v", conn.writes)
	}
	if s.targetVersion != "9.9.9.9" {
		t.Fatalf("targetVersion = %q, want 9.9.9.9", s.targetVersion)
	}
}

func TestSessionHandleVersionInvalidFormat(t *testing.T) {
	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	s := newSession(testDevice, conn)

	s.handle(testAddr, []byte("version:not-a-version"))
	assertSingleWrite(t, conn, "version_fail:")
	if !bytes.HasSuffix(conn.writes[0].data, []byte("format")) {
		t.Fatalf("version_fail reason = %q, want format", conn.writes[0].data)
	}
	if s.targetVersion != "" {
		t.Fatalf("targetVersion = %q, want empty", s.targetVersion)
	}
}

func TestSessionHandleVersionReadsCurrentVersion(t *testing.T) {
	runDir := t.TempDir()
	writeFile(t, filepath.Join(runDir, configFileName), `{"version":"1.0.0.0"}`)

	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	info := testDevice
	info.ConfigPath = runDir // 目录 → 读 <runDir>/cpdc_config.json
	s := newSession(info, conn)

	s.handle(testAddr, []byte("version:9.9.9.9"))
	assertSingleWrite(t, conn, "version_ok:")
	if !bytes.HasSuffix(conn.writes[0].data, []byte("1.0.0.0")) {
		t.Fatalf("version_ok = %q, want suffix 1.0.0.0", conn.writes[0].data)
	}

	// 修改配置版本后再次校验，应实时读到新版本。
	writeFile(t, filepath.Join(runDir, configFileName), `{"version":"2.0.0.0"}`)
	conn.writes = nil
	s.handle(testAddr, []byte("version:9.9.9.9"))
	assertSingleWrite(t, conn, "version_ok:")
	if !bytes.HasSuffix(conn.writes[0].data, []byte("2.0.0.0")) {
		t.Fatalf("version_ok = %q, want suffix 2.0.0.0", conn.writes[0].data)
	}
}

func TestSessionRepliesToServerPort(t *testing.T) {
	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	s := newSession(testDevice, conn)
	source := &net.UDPAddr{IP: net.IPv4(192, 168, 1, 20), Port: 54321}

	s.handle(source, []byte("auth"))

	writes := conn.snapshotWrites()
	if len(writes) != 1 {
		t.Fatalf("writes = %v", writes)
	}
	if writes[0].addr.Port != serverPort || !writes[0].addr.IP.Equal(net.IPv4bcast) {
		t.Fatalf("reply destination = %v, want 255.255.255.255:%d", writes[0].addr, serverPort)
	}
}

func TestSessionHandleFileSetsHeaderAndReplies(t *testing.T) {
	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	s := newSession(testDevice, conn)

	s.handle(testAddr, fileCommand(0x11223344, 2, 100, "pkg.zip"))

	if s.assembler.packetCount != 2 || s.assembler.totalBytes != 100 || s.assembler.crc32 != 0x11223344 || s.assembler.fileName != "pkg.zip" {
		t.Fatalf("assembler header = %+v", s.assembler)
	}
	assertSingleWrite(t, conn, "fileAck:")
}

func TestSessionHandlePacketLengthMismatch(t *testing.T) {
	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	s := newSession(testDevice, conn)
	s.assembler.SetHeader(1, 3, 0, "x.zip")

	payload := []byte("abc")
	cmd := packetCommand(crc32.ChecksumIEEE(payload), 1, uint32(len(payload)+1), payload)
	s.handle(testAddr, cmd)

	assertSingleWrite(t, conn, "packet_fail:")
	if !bytes.HasSuffix(conn.writes[0].data, []byte("length")) {
		t.Fatalf("packet_fail reason = %q, want length", conn.writes[0].data)
	}
}

func TestSessionHandlePacketCRCMismatch(t *testing.T) {
	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	s := newSession(testDevice, conn)
	s.assembler.SetHeader(1, 3, 0, "x.zip")

	payload := []byte("abc")
	cmd := packetCommand(crc32.ChecksumIEEE(payload)^0xFFFFFFFF, 1, uint32(len(payload)), payload)
	s.handle(testAddr, cmd)

	assertSingleWrite(t, conn, "packet_fail:")
	if !bytes.HasSuffix(conn.writes[0].data, []byte("crc")) {
		t.Fatalf("packet_fail reason = %q, want crc", conn.writes[0].data)
	}
}

func TestSessionHandlePacketDropsOutOfRange(t *testing.T) {
	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	s := newSession(testDevice, conn)
	s.assembler.SetHeader(2, 2, 0, "x.zip")

	payload := []byte("a")
	s.handle(testAddr, packetCommand(crc32.ChecksumIEEE(payload), 0, 1, payload))
	s.handle(testAddr, packetCommand(crc32.ChecksumIEEE(payload), 3, 1, payload))

	if len(conn.writes) != 0 {
		t.Fatalf("writes = %v, want none", conn.writes)
	}
}

func TestSessionFullTransferValidatesAndInstalls(t *testing.T) {
	zipBytes := buildZip(t)
	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	s := newSession(testDevice, conn)

	oldSpawn := spawnProcess
	oldExit := exitNow
	oldInterval := writeOkSendInterval
	oldWait := waitForFinalizeStarted
	oldWrite := writeOldExited
	spawnProcess = func(string, []string) error { return nil }
	exitNow = func(int) {}
	writeOkSendInterval = 0
	waitForFinalizeStarted = func(string, time.Duration) error { return nil }
	writeOldExited = func(string) {}
	defer func() {
		spawnProcess = oldSpawn
		exitNow = oldExit
		writeOkSendInterval = oldInterval
		waitForFinalizeStarted = oldWait
		writeOldExited = oldWrite
	}()

	var installed []byte
	s.write = func(_ context.Context, data []byte, _ DeviceInfo, _ string) (string, string, error) {
		installed = append([]byte(nil), data...)
		return "restart.sh", "/tmp", nil
	}

	mid := len(zipBytes) / 2
	packets := [][]byte{zipBytes[:mid], zipBytes[mid:]}
	crc := crc32.ChecksumIEEE(zipBytes)

	s.handle(testAddr, fileCommand(crc, uint32(len(packets)), uint32(len(zipBytes)), "install.zip"))
	for i, payload := range packets {
		s.handle(testAddr, packetCommand(crc32.ChecksumIEEE(payload), uint32(i+1), uint32(len(payload)), payload))
	}
	s.handle(testAddr, validCommand(crc, uint32(len(packets)), uint32(len(zipBytes)), "install.zip"))
	s.handle(testAddr, writeCommand(crc, uint32(len(packets)), uint32(len(zipBytes)), "install.zip"))

	wantPrefixes := []string{"fileAck:", "packet_ok:", "packet_ok:", "valid_ok:", "write_ok:", "write_ok:", "write_ok:"}
	writes := conn.snapshotWrites()
	if len(writes) != len(wantPrefixes) {
		t.Fatalf("writes = %d, want %d: %v", len(writes), len(wantPrefixes), writes)
	}
	for i, prefix := range wantPrefixes {
		if !bytes.HasPrefix(writes[i].data, []byte(prefix)) {
			t.Fatalf("write[%d] prefix = %q, want %q", i, writes[i].data, prefix)
		}
	}

	if !bytes.Equal(installed, zipBytes) {
		t.Fatalf("installed bytes differ: got %d want %d", len(installed), len(zipBytes))
	}
}

func TestSessionValidateAndReplyFailures(t *testing.T) {
	payload := []byte("0123456789")

	tests := []struct {
		name       string
		totalBytes int
		crc        uint32
		zipValid   bool
		wantReason string
	}{
		{name: "size", totalBytes: len(payload) + 1, crc: crc32.ChecksumIEEE(payload), zipValid: true, wantReason: "size"},
		{name: "crc", totalBytes: len(payload), crc: crc32.ChecksumIEEE(payload) ^ 0xFFFFFFFF, zipValid: true, wantReason: "crc"},
		{name: "zip", totalBytes: len(payload), crc: crc32.ChecksumIEEE(payload), zipValid: false, wantReason: "zip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &fakeConn{reads: make(chan fakeRead, 1)}
			s := newSession(testDevice, conn)
			s.assembler.SetHeader(1, tt.totalBytes, tt.crc, "x.zip")
			s.assembler.Put(1, payload)

			s.handleValid(testAddr, validCommand(tt.crc, 1, uint32(tt.totalBytes), "x.zip"))

			assertSingleWrite(t, conn, "valid_fail:")
			if !bytes.HasSuffix(conn.writes[0].data, []byte(tt.wantReason)) {
				t.Fatalf("valid_fail reason = %q, want %q", conn.writes[0].data, tt.wantReason)
			}
		})
	}
}

func TestSessionHandlePacketOutOfOrderTriggersSequenceFail(t *testing.T) {
	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	s := newSession(testDevice, conn)
	s.assembler.SetHeader(3, 3, 0, "x.zip")

	// Packet 2 arrives before packet 1, so it is not the expected next packet.
	payload := []byte("b")
	s.handle(testAddr, packetCommand(crc32.ChecksumIEEE(payload), 2, uint32(len(payload)), payload))

	writes := conn.snapshotWrites()
	if len(writes) != 1 {
		t.Fatalf("writes = %d, want 1: %v", len(writes), writes)
	}
	if !bytes.HasPrefix(writes[0].data, []byte("packet_fail:")) {
		t.Fatalf("write = %q, want packet_fail", writes[0].data)
	}
	if !bytes.HasSuffix(writes[0].data, []byte("sequence:1")) {
		t.Fatalf("packet_fail reason = %q, want sequence:1", writes[0].data)
	}
}

func TestSessionHandlePacketDuplicateReAcks(t *testing.T) {
	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	s := newSession(testDevice, conn)
	s.assembler.SetHeader(2, 2, 0, "x.zip")

	payload := []byte("a")
	s.handle(testAddr, packetCommand(crc32.ChecksumIEEE(payload), 1, 1, payload))
	s.handle(testAddr, packetCommand(crc32.ChecksumIEEE(payload), 1, 1, payload))

	writes := conn.snapshotWrites()
	if len(writes) != 2 {
		t.Fatalf("writes = %d, want 2 (duplicate re-acked): %v", len(writes), writes)
	}
	for i, w := range writes {
		if !bytes.HasPrefix(w.data, []byte("packet_ok:")) {
			t.Fatalf("write[%d] = %q, want packet_ok", i, w.data)
		}
	}
	if s.assembler.Received() != 1 {
		t.Fatalf("Received = %d, want 1 (duplicate must not advance)", s.assembler.Received())
	}
}

func TestSessionRunReadsAndDispatches(t *testing.T) {
	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	s := newSession(testDevice, conn)
	conn.reads <- fakeRead{data: []byte("auth"), addr: testAddr}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(conn.writes) != 1 || !bytes.HasPrefix(conn.writes[0].data, []byte("authAck:")) {
		t.Fatalf("Run writes = %v", conn.writes)
	}
}

func TestSessionInstallFailureRepliesWriteFail(t *testing.T) {
	zipBytes := buildZip(t)
	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	s := newSession(testDevice, conn)

	s.write = func(_ context.Context, _ []byte, _ DeviceInfo, _ string) (string, string, error) {
		return "", "", &writeError{code: "overwrite_binary_open_fail", path: "/tmp/CPDC-HF"}
	}

	mid := len(zipBytes) / 2
	packets := [][]byte{zipBytes[:mid], zipBytes[mid:]}
	crc := crc32.ChecksumIEEE(zipBytes)

	s.handle(testAddr, fileCommand(crc, uint32(len(packets)), uint32(len(zipBytes)), "install.zip"))
	for i, payload := range packets {
		s.handle(testAddr, packetCommand(crc32.ChecksumIEEE(payload), uint32(i+1), uint32(len(payload)), payload))
	}
	s.handle(testAddr, validCommand(crc, uint32(len(packets)), uint32(len(zipBytes)), "install.zip"))
	s.handle(testAddr, writeCommand(crc, uint32(len(packets)), uint32(len(zipBytes)), "install.zip"))

	last := conn.snapshotWrites()[len(conn.snapshotWrites())-1].data
	if !bytes.HasPrefix(last, []byte("write_fail:")) {
		t.Fatalf("last write = %q, want write_fail", last)
	}
	if !bytes.HasSuffix(last, []byte("overwrite_binary_open_fail:/tmp/CPDC-HF")) {
		t.Fatalf("write_fail reason = %q, want overwrite_binary_open_fail:/tmp/CPDC-HF", last)
	}
}


func TestSessionScanDeletesReceiptMarker(t *testing.T) {
	runDir := t.TempDir()
	oldExe := executablePath
	executablePath = func() (string, error) { return filepath.Join(runDir, "CPDC-HF"), nil }
	defer func() { executablePath = oldExe }()

	writeFile(t, filepath.Join(runDir, receiptMarkerName), `{"version":"1.2.3.4"}`)

	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	s := newSession(testDevice, conn)
	s.handle(testAddr, []byte("Scan"))

	if _, err := os.Stat(filepath.Join(runDir, receiptMarkerName)); !os.IsNotExist(err) {
		t.Fatalf("receipt marker not deleted: %v", err)
	}
	assertSingleWrite(t, conn, "Scan:success")
}

func TestUpdateFailReason(t *testing.T) {
	if got := updateFailReason(fmt.Errorf("%w: boom", errRestartError)); got != "restart_error" {
		t.Fatalf("restart reason = %q", got)
	}
}

func assertSingleWrite(t *testing.T, conn *fakeConn, prefix string) {
	t.Helper()
	if len(conn.writes) != 1 {
		t.Fatalf("writes = %v, want exactly one", conn.writes)
	}
	if !bytes.HasPrefix(conn.writes[0].data, []byte(prefix)) {
		t.Fatalf("write = %q, want prefix %q", conn.writes[0].data, prefix)
	}
}

func fileCommand(crc uint32, packetCount, totalBytes uint32, fileName string) []byte {
	data := []byte("file:")
	data = appendUint32(data, crc)
	data = appendUint32(data, packetCount)
	data = appendUint32(data, totalBytes)
	return append(data, []byte(fileName)...)
}

func packetCommand(crc uint32, packetNumber, packetLength uint32, payload []byte) []byte {
	data := []byte("packet:")
	data = appendUint32(data, crc)
	data = appendUint32(data, packetNumber)
	data = appendUint32(data, packetLength)
	return append(data, payload...)
}

func validCommand(crc uint32, packetCount, totalBytes uint32, fileName string) []byte {
	data := []byte("valid:")
	data = appendUint32(data, crc)
	data = appendUint32(data, packetCount)
	data = appendUint32(data, totalBytes)
	return append(data, []byte(fileName)...)
}

func writeCommand(crc uint32, packetCount, totalBytes uint32, fileName string) []byte {
	data := []byte("write:")
	data = appendUint32(data, crc)
	data = appendUint32(data, packetCount)
	data = appendUint32(data, totalBytes)
	return append(data, []byte(fileName)...)
}

func buildZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("cpdc_config.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(`{"version":"1.0.0.0"}`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
