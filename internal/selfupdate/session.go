package selfupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

const (
	// listenPort is the UDP port CPDC listens on for self-update commands.
	listenPort = 39003

	// serverPort is the UDP port CPDS listens on for self-update replies.
	// CPDC normally replies to each command's source address; serverPort is
	// only used when CPDC broadcasts an unsolicited update receipt (T17).
	serverPort = 39004
)

const readPollInterval = 250 * time.Millisecond

// datagramConn is the subset of *net.UDPConn used by Session, allowing tests
// to inject an in-memory connection.
type datagramConn interface {
	ReadFromUDP([]byte) (int, *net.UDPAddr, error)
	WriteToUDP([]byte, *net.UDPAddr) (int, error)
	SetReadDeadline(time.Time) error
	Close() error
}

// Session answers self-update downlink commands and assembles transferred
// packets. The UDP read loop is thin so the command handling can be tested
// without real sockets.
type Session struct {
	conn      datagramConn
	info      DeviceInfo
	assembler *Assembler
	// targetVersion is the version carried by the most recent "version:"
	// command. It is consumed by the install phase and cleared on Reset.
	targetVersion string
	// write performs the write phase and returns the finalize parameter file
	// path and run directory.
	write func(context.Context, []byte, DeviceInfo, string) (string, string, error)

	cancelMu      sync.Mutex
	installCancel *installHandle
}

// installHandle owns the cancellation function for one in-flight install.
type installHandle struct {
	cancel context.CancelFunc
}

func NewSession(info DeviceInfo) (*Session, error) {
	if info.DeviceType == "" || info.IP == "" {
		return nil, errors.New("device type and IP are required")
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: listenPort})
	if err != nil {
		return nil, fmt.Errorf("listen UDP %d: %w", listenPort, err)
	}
	logConnOpen(conn.LocalAddr(), info)
	s := newSession(info, conn)
	s.write = writeFiles
	return s, nil
}

func newSession(info DeviceInfo, conn datagramConn) *Session {
	return &Session{conn: conn, info: info, assembler: NewAssembler()}
}

// Run reads downlink commands until ctx is cancelled and dispatches each one.
func (s *Session) Run(ctx context.Context) error {
	defer func() {
		_ = s.conn.Close()
		logConnClose(s.info)
	}()
	buffer := make([]byte, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if err := s.conn.SetReadDeadline(time.Now().Add(readPollInterval)); err != nil {
			return err
		}
		n, addr, err := s.conn.ReadFromUDP(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var networkError net.Error
			if errors.As(err, &networkError) && networkError.Timeout() {
				continue
			}
			return err
		}
		data := append([]byte(nil), buffer[:n]...)
		s.handle(addr, data)
	}
}

// Reset clears any in-flight assembly and returns the session to its idle
// state. Scan triggers Reset before replying Scan:success.
func (s *Session) Reset() {
	s.cancelInstall()
	s.assembler.Reset()
	s.targetVersion = ""
	if runDir, err := executableDir(); err == nil {
		_ = removeReceiptMarker(runDir)
	}
}

func (s *Session) cancelInstall() {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	if s.installCancel != nil {
		s.installCancel.cancel()
		s.installCancel = nil
	}
}

func (s *Session) handle(addr *net.UDPAddr, data []byte) {
	logDump("RX", addr.IP.String(), data, s.assembler.packetCount)

	switch parseCommand(data) {
	case commandScan:
		s.handleScan(addr)
	case commandAuth:
		s.handleAuth(addr)
	case commandVersion:
		s.handleVersion(addr, data)
	case commandFile:
		s.handleFile(addr, data)
	case commandPacket:
		s.handlePacket(addr, data)
	case commandValid:
		s.handleValid(addr, data)
	case commandWrite:
		s.handleWrite(addr, data)
	}
}

func (s *Session) handleScan(addr *net.UDPAddr) {
	s.Reset()
	_ = s.send(addr, []byte(scanAck))
}

func (s *Session) handleAuth(addr *net.UDPAddr) {
	_ = s.send(addr, encodeAuthAck(s.info))
}

func (s *Session) handleVersion(addr *net.UDPAddr, data []byte) {
	target, err := parseVersionCommand(data)
	if err != nil {
		_ = s.send(addr, encodeVersionFail(s.info, "format"))
		return
	}
	s.targetVersion = target
	// 校验阶段实时读当前版本；读失败（配置缺失/JSON 非法）回退到启动快照。
	if current, err := ReadVersion(s.info.ConfigPath); err == nil {
		s.info.Version = current
	}
	_ = s.send(addr, encodeVersionOk(s.info))
}

func (s *Session) handleFile(addr *net.UDPAddr, data []byte) {
	crc, packetCount, totalBytes, fileName, err := parseFileHeader(data)
	if err != nil {
		return
	}
	s.assembler.SetHeader(int(packetCount), int(totalBytes), crc, fileName)
	_ = s.send(addr, encodeFileAck(s.info, crc, packetCount, totalBytes, fileName))
}

func (s *Session) handlePacket(addr *net.UDPAddr, data []byte) {
	crc, packetNumber, packetLength, payload, err := parsePacket(data)
	if err != nil {
		return
	}
	if packetNumber == 0 || packetNumber > uint32(s.assembler.packetCount) {
		return
	}
	expected := uint32(s.assembler.Received() + 1)
	if packetNumber < expected {
		// Duplicate of an already-received packet: re-ack without changing the
		// received counter or overwriting stored data, so CPDS can move on.
		_ = s.send(addr, encodePacketOk(s.info, crc, packetNumber, packetLength))
		return
	}
	if packetNumber > expected {
		reason := fmt.Sprintf("sequence:%d", expected)
		_ = s.send(addr, encodePacketFail(s.info, crc, packetNumber, packetLength, reason))
		return
	}
	if len(payload) != int(packetLength) {
		_ = s.send(addr, encodePacketFail(s.info, crc, packetNumber, packetLength, "length"))
		return
	}
	if crc32.ChecksumIEEE(payload) != crc {
		_ = s.send(addr, encodePacketFail(s.info, crc, packetNumber, packetLength, "crc"))
		return
	}
	s.assembler.Put(int(packetNumber), payload)
	s.assembler.RecordReceived()
	_ = s.send(addr, encodePacketOk(s.info, crc, packetNumber, packetLength))
}

func (s *Session) handleValid(addr *net.UDPAddr, data []byte) {
	crc, packetCount, totalBytes, fileName, err := parseValidHeader(data)
	if err != nil {
		return
	}
	if !s.headerMatches(crc, packetCount, totalBytes, fileName) {
		_ = s.send(addr, encodeValidFail(s.info, "mismatch"))
		return
	}
	if err := s.assembler.Validate(); err != nil {
		_ = s.send(addr, encodeValidFail(s.info, err.Error()))
		return
	}
	if err := validateZip(s.assembler.Bytes()); err != nil {
		_ = s.send(addr, encodeValidFail(s.info, ErrInvalidZip.Error()))
		return
	}
	_ = s.send(addr, encodeValidOk(s.info))
}

// writeOkSendCount is the number of times write_ok is sent before exiting.
const writeOkSendCount = 3

// writeOkSendInterval is the delay between repeated write_ok sends. It is a
// package variable so tests can shorten it to zero.
var writeOkSendInterval = time.Second

func (s *Session) handleWrite(addr *net.UDPAddr, data []byte) {
	crc, packetCount, totalBytes, fileName, err := parseWriteHeader(data)
	if err != nil {
		return
	}
	if !s.headerMatches(crc, packetCount, totalBytes, fileName) {
		_ = s.send(addr, encodeWriteFail(s.info, "mismatch"))
		return
	}
	if s.write == nil {
		return
	}
	s.cleanupForWrite()
	paramsPath, _, err := s.write(context.Background(), s.assembler.Bytes(), s.info, s.targetVersion)
	if err != nil {
		_ = s.send(addr, encodeWriteFail(s.info, writeFailReason(err)))
		return
	}
	// 用自身 --self-update-finalize 作为替换器；失败则回 write_fail（此时旧进程还没退，能回复）。
	exePath, exeErr := executablePath()
	if exeErr != nil {
		_ = s.send(addr, encodeWriteFail(s.info, "executable_path_fail"))
		return
	}
	if err := spawnProcess(exePath, []string{"--self-update-finalize", "--params", paramsPath}); err != nil {
		_ = s.send(addr, encodeWriteFail(s.info, "restart_spawn_fail"))
		return
	}
	// write_ok 连发 3 次（间隔 1 秒），然后退出旧进程。
	for i := 0; i < writeOkSendCount; i++ {
		if i > 0 {
			time.Sleep(writeOkSendInterval)
		}
		_ = s.send(addr, encodeWriteOk(s.info))
	}
	exitNow(0)
}

func (s *Session) headerMatches(crc, packetCount, totalBytes uint32, fileName string) bool {
	return crc == s.assembler.crc32 &&
		int(packetCount) == s.assembler.packetCount &&
		int(totalBytes) == s.assembler.totalBytes &&
		fileName == s.assembler.fileName
}

// cleanupForWrite removes staging leftovers from a previous incomplete write.
// The install files themselves are atomically overwritten by writeFiles.
func (s *Session) cleanupForWrite() {
	if runDir, err := executableDir(); err == nil {
		_ = CleanupInstallDir(runDir)
		_ = removeReceiptMarker(runDir)
	}
}

func (s *Session) send(addr *net.UDPAddr, data []byte) error {
	logDump("TX", addr.IP.String(), data, s.assembler.packetCount)

	// Replies are sent to the CPDS receive port (39004) on the same interface
	// the command arrived from. The source port is an ephemeral CPDS send
	// socket, so replying to addr directly would not reach the CPDS listener.
	destination := &net.UDPAddr{IP: addr.IP, Port: serverPort}
	n, err := s.conn.WriteToUDP(data, destination)
	if err != nil {
		fmt.Fprintf(os.Stdout, "[TX-ERR] 发送 %d 字节到 %s 失败: %v\n", len(data), destination.String(), err)
	} else if n != len(data) {
		fmt.Fprintf(os.Stdout, "[TX-WARN] 发送 %d/%d 字节到 %s\n", n, len(data), destination.String())
	}
	return err
}

// validateZip reports whether data can be fully read back as a ZIP archive,
// which verifies the central directory and each entry's checksum.
func validateZip(data []byte) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, file := range reader.File {
		rc, err := file.Open()
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(io.Discard, rc)
		closeErr := rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}