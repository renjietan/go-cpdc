package selfupdate

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"
)

var (
	errVersionReadError = errors.New("version_read_error")
	errVersionMismatch  = errors.New("version_mismatch")
	errFinalizeFail     = errors.New("finalize_fail")
)

// readReceiptMarker returns the pending new version, the persisted source IP,
// and whether a marker file exists below runDir.
func readReceiptMarker(runDir string) (string, string, bool) {
	data, err := os.ReadFile(filepath.Join(runDir, receiptMarkerName))
	if err != nil {
		return "", "", false
	}
	var marker struct {
		Version string `json:"version"`
		IP      string `json:"ip"`
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		return "", "", false
	}
	return marker.Version, marker.IP, true
}

// receiptError returns the receipt outcome error, or nil for update_ok.
func receiptError(newVersion, currentVersion string, versionErr error) error {
	if versionErr != nil {
		return errVersionReadError
	}
	if currentVersion != newVersion {
		return errVersionMismatch
	}
	return nil
}

// receiptSendCount is the number of times update_ok/update_fail is sent.
const receiptSendCount = 3

// receiptSendInterval is the delay between repeated receipt sends. It is a
// package variable so tests can shorten it to zero.
var receiptSendInterval = time.Second

// sendUpdateReceipt sends update_ok or update_fail to addr three times, one
// second apart, to tolerate UDP packet loss.
func sendUpdateReceipt(conn datagramConn, addr *net.UDPAddr, d DeviceInfo, newVersion string, err error) error {
	var data []byte
	if err != nil {
		data = encodeUpdateFail(d, err.Error())
	} else {
		data = encodeUpdateOk(d, newVersion)
	}
	var lastErr error
	for i := 0; i < receiptSendCount; i++ {
		if i > 0 {
			time.Sleep(receiptSendInterval)
		}
		logDump("TX", addr.IP.String(), data, 0)
		_, lastErr = conn.WriteToUDP(data, addr)
	}
	return lastErr
}

// ReceiptDestinations returns the broadcast and loopback addresses the startup
// receipt is sent to.
func ReceiptDestinations() []*net.UDPAddr {
	return []*net.UDPAddr{
		{IP: net.IPv4bcast, Port: serverPort},
		{IP: net.IPv4(127, 0, 0, 1), Port: serverPort},
	}
}

// SendStartupReceipt reads a pending receipt marker and, when present, replies
// to addr and deletes the marker. It reports whether a receipt was sent.
func (s *Session) SendStartupReceipt(addr *net.UDPAddr, runDir, configPath string) bool {
	newVersion, persistedIP, ok := readReceiptMarker(runDir)
	if !ok {
		return false
	}
	// 回执 ID 复用 write 阶段持久化的 IP，避免重启后多网卡设备 IP 变化。
	if persistedIP != "" {
		s.info.IP = persistedIP
	}
	_, configFile, resolveErr := resolveConfigPathsIn(configPath, runDir)
	currentVersion := ""
	var versionErr error = resolveErr
	if resolveErr == nil {
		currentVersion, versionErr = readVersionFile(configFile)
	}
	receiptErr := receiptError(newVersion, currentVersion, versionErr)
	// finalize 失败回滚启动旧 exe 时，优先上报 finalize_fail。
	if hasFinalizeFail(runDir) {
		receiptErr = errFinalizeFail
	}
	_ = sendUpdateReceipt(s.conn, addr, s.info, newVersion, receiptErr)
	_ = removeReceiptMarker(runDir)
	_ = os.Remove(filepath.Join(runDir, installDirName, finalizeFailName))
	return true
}

// hasFinalizeFail reports whether the finalize helper left a failure marker.
func hasFinalizeFail(runDir string) bool {
	data, err := os.ReadFile(filepath.Join(runDir, installDirName, finalizeFailName))
	return err == nil && len(data) > 0
}
