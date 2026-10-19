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
	"path/filepath"
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
	// receivedLocalIP 返回最近一次收到的数据报的本地目的 IP（收到包的那张网卡）。
	// 无法获取时返回 nil。
	receivedLocalIP() net.IP
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
	// path and run directory. The final argument is the CPDS source address,
	// which is persisted into the receipt marker for the post-reboot receipt.
	write func(context.Context, []byte, DeviceInfo, string, string) (string, string, error)

	cancelMu      sync.Mutex
	installCancel *installHandle

	// replyViaBroadcast 为 true 表示 CPDS 源地址不在本地网段，单播可能路由不回，
	// 上行回复改用业务网卡广播兜底。
	replyViaBroadcast bool

	// lastLocalIP 是最近一次收到的数据报的本地目的 IP（接收网卡），由 Run 读包后回填。
	lastLocalIP net.IP
}

// installHandle owns the cancellation function for one in-flight install.
type installHandle struct {
	cancel context.CancelFunc
}

func NewSession(info DeviceInfo) (*Session, error) {
	if info.DeviceType == "" {
		return nil, errors.New("device type is required")
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: listenPort})
	if err != nil {
		return nil, fmt.Errorf("listen UDP %d: %w", listenPort, err)
	}
	if err := enablePacketInfo(conn); err != nil {
		fmt.Fprintf(os.Stdout, "[WARN] enable IP_PKTINFO failed, fallback to subnet matching: %v\n", err)
	}
	wrapped := &udpConn{conn: conn}
	logConnOpen(wrapped.conn.LocalAddr(), info)
	s := newSession(info, wrapped)
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
		s.lastLocalIP = s.conn.receivedLocalIP()
		s.handle(addr, data)
	}
}

// Reset clears any in-flight assembly and returns the session to its idle
// state. Scan triggers Reset before replying Scan:success.
func (s *Session) Reset() {
	s.cancelInstall()
	s.assembler.Reset()
	s.targetVersion = ""
	if dataDir, err := resolveDataDir(s.info); err == nil {
		_ = removeReceiptMarker(dataDir)
		_ = os.Remove(filepath.Join(dataDir, finalizeFailName))
		_ = os.Remove(filepath.Join(dataDir, writeDoneName))
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
	if !s.ensureNetwork(addr, s.lastLocalIP) {
		return
	}
	_ = s.send(addr, []byte(scanAck))
}

// ensureNetwork 在收到 Scan 时确定业务网卡并回填 info.IP。优先使用读包时
// 拿到的接收网卡 IP（localIP，来自 IP_PKTINFO），拿不到再回退网段匹配。
// 判定失败（源地址非法 / 无任何可用网卡）返回 false，不回 Scan:success。
func (s *Session) ensureNetwork(addr *net.UDPAddr, localIP net.IP) bool {
	if addr == nil {
		return false
	}
	ip, onSubnet, err := networkResolver(addr, localIP)
	if err != nil || ip == "" {
		return false
	}
	s.info.IP = ip
	s.replyViaBroadcast = !onSubnet
	return true
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

// finalizeStartWaitTimeout is how long the old process waits for the finalize
// helper to write its started marker before failing the write phase.
var finalizeStartWaitTimeout = 3 * time.Second

// writeOkSendInterval is the delay between repeated write_ok sends. It is a
// package variable so tests can shorten it to zero.
var writeOkSendInterval = time.Second

func (s *Session) handleWrite(addr *net.UDPAddr, data []byte) {
	crc, packetCount, totalBytes, fileName, err := parseWriteHeader(data)
	if err != nil {
		return
	}
	if dataDir, derr := resolveDataDir(s.info); derr == nil {
		if d, ok := readWriteDone(dataDir); ok &&
			d.CRC == crc && d.PacketCount == packetCount && d.TotalBytes == totalBytes && d.FileName == fileName {
			for i := 0; i < writeOkSendCount; i++ {
				if i > 0 {
					time.Sleep(writeOkSendInterval)
				}
				_ = s.send(addr, encodeWriteOk(s.info))
			}
			return
		}
	}
	if !s.headerMatches(crc, packetCount, totalBytes, fileName) {
		_ = s.send(addr, encodeWriteFail(s.info, "mismatch"))
		return
	}
	if s.write == nil {
		return
	}
	s.cleanupForWrite()
	dataDir, dataErr := resolveDataDir(s.info)
	if dataErr != nil {
		_ = s.send(addr, encodeWriteFail(s.info, "executable_path_fail"))
		return
	}
	paramsPath, _, err := s.write(context.Background(), s.assembler.Bytes(), s.info, s.targetVersion, addr.IP.String())
	if err != nil {
		_ = s.send(addr, encodeWriteFail(s.info, writeFailReason(err)))
		return
	}
	if s.info.DeviceType != "VehInter" && paramsPath != "" {
		_ = updateFinalizeParamsFileHeader(paramsPath, crc, packetCount, totalBytes, fileName)
	}
	// VehInter 由第三方接管替换与拉起，不启动 finalize。
	if s.info.DeviceType == "VehInter" {
		_ = os.MkdirAll(dataDir, 0o755)
		_ = os.WriteFile(filepath.Join(dataDir, vehInterHandoffName), []byte("1"), 0o600)
		for i := 0; i < writeOkSendCount; i++ {
			if i > 0 {
				time.Sleep(writeOkSendInterval)
			}
			_ = s.send(addr, encodeWriteOk(s.info))
		}
		exitNow(0)
		return
	}

	exePath, exeErr := executablePath()
	if exeErr != nil {
		_ = s.send(addr, encodeWriteFail(s.info, "executable_path_fail"))
		return
	}
	if err := spawnProcess(exePath, []string{"--self-update-finalize", "--params", paramsPath}); err != nil {
		_ = s.send(addr, encodeWriteFail(s.info, "restart_spawn_fail"))
		return
	}
	if err := waitForFinalizeStarted(dataDir, finalizeStartWaitTimeout); err != nil {
		_ = s.send(addr, encodeWriteFail(s.info, "finalize_start_fail"))
		return
	}
	for i := 0; i < writeOkSendCount; i++ {
		if i > 0 {
			time.Sleep(writeOkSendInterval)
		}
		_ = s.send(addr, encodeWriteOk(s.info))
	}
	writeOldExited(dataDir)
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
		if dataDir, derr := resolveDataDir(s.info); derr == nil {
			_ = removeReceiptMarker(dataDir)
			_ = os.Remove(filepath.Join(dataDir, finalizeParamsName))
			_ = os.Remove(filepath.Join(dataDir, finalizeFailName))
			_ = os.Remove(filepath.Join(dataDir, finalizeStartedName))
			_ = os.Remove(filepath.Join(dataDir, oldExitedName))
			_ = os.Remove(filepath.Join(dataDir, vehInterHandoffName))
			_ = os.Remove(filepath.Join(dataDir, writeDoneName))
		}
	}
}

// send replies to the source address of the command that triggered the reply.
// Replies are unicast back to CPDS rather than broadcast, so the kernel picks
// the correct egress interface automatically.
func (s *Session) send(addr *net.UDPAddr, data []byte) error {
	if addr == nil || addr.IP == nil {
		return errors.New("missing reply destination")
	}
	if s.replyViaBroadcast {
		return s.broadcastReply(data)
	}
	destination := &net.UDPAddr{IP: addr.IP, Port: serverPort}
	logDump("TX", destination.IP.String(), data, s.assembler.packetCount)

	n, err := s.conn.WriteToUDP(data, destination)
	if err != nil {
		fmt.Fprintf(os.Stdout, "[TX-ERR] 发送 %d 字节到 %s 失败: %v\n", len(data), destination.String(), err)
		return err
	}
	if n != len(data) {
		fmt.Fprintf(os.Stdout, "[TX-WARN] 发送 %d/%d 字节到 %s\n", n, len(data), destination.String())
		return io.ErrShortWrite
	}
	return nil
}

// openBroadcastSocket 创建绑定到指定网卡 IP、并开启 SO_BROADCAST 的 UDP socket。
// 它是包变量，便于测试注入 in-memory socket。
var openBroadcastSocket = func(ip net.IP) (datagramConn, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip, Port: 0})
	if err != nil {
		return nil, err
	}
	if err := enableBroadcast(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &udpConn{conn: conn}, nil
}

// broadcastReply 在业务网卡上绑一个临时广播 socket，向 255.255.255.255:39004
// 广播一次。跨网段设备通常没有默认路由，从 0.0.0.0 广播会 ENETUNREACH，
// 必须绑到具体网卡 IP 才能走该网卡的广播。
func (s *Session) broadcastReply(data []byte) error {
	ip := net.ParseIP(s.info.IP).To4()
	if ip == nil {
		return errors.New("invalid business IP for broadcast reply")
	}
	conn, err := openBroadcastSocket(ip)
	if err != nil {
		return err
	}
	defer conn.Close()
	destination := &net.UDPAddr{IP: net.IPv4bcast, Port: serverPort}
	logDump("TX", destination.IP.String(), data, s.assembler.packetCount)
	n, err := conn.WriteToUDP(data, destination)
	if err != nil {
		fmt.Fprintf(os.Stdout, "[TX-ERR] 发送 %d 字节到 %s 失败: %v\n", len(data), destination.String(), err)
		return err
	}
	if n != len(data) {
		fmt.Fprintf(os.Stdout, "[TX-WARN] 发送 %d/%d 字节到 %s\n", n, len(data), destination.String())
		return io.ErrShortWrite
	}
	return nil
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
