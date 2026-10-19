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

// readReceiptMarker returns the pending new version, the persisted device IP,
// the persisted CPDS address, and whether a marker file exists below runDir.
func readReceiptMarker(runDir string) (string, string, string, bool) {
	data, err := os.ReadFile(filepath.Join(runDir, receiptMarkerName))
	if err != nil {
		return "", "", "", false
	}
	var marker struct {
		Version string `json:"version"`
		IP      string `json:"ip"`
		CpdsIP  string `json:"cpds_ip"`
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		return "", "", "", false
	}
	return marker.Version, marker.IP, marker.CpdsIP, true
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

// sendUpdateReceipt sends update_ok or update_fail to the given destination
// receiptSendCount times, one second apart, to tolerate UDP packet loss.
func sendUpdateReceipt(conn datagramConn, destination *net.UDPAddr, d DeviceInfo, newVersion string, err error) error {
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
		logDump("TX", destination.IP.String(), data, 0)
		_, lastErr = conn.WriteToUDP(data, destination)
	}
	return lastErr
}

// SendStartupReceipt reads a pending receipt marker and, when present, replies
// to CPDS and deletes the marker. It reports whether a receipt was sent.
func (s *Session) SendStartupReceipt(runDir, configPath string) bool {
	dataDir := runDir
	if s.info.CachePath != "" {
		dataDir = s.info.CachePath
	}
	newVersion, persistedIP, cpdsIP, ok := readReceiptMarker(dataDir)
	if !ok {
		return false
	}
	if persistedIP != "" {
		s.info.IP = persistedIP
	}

	// 计算回执结果。
	var receiptErr error
	if s.info.DeviceType == "VehInter" {
		// VehInter：只有第三方成功拉起（有 handoff 标记）才回 update_ok。
		if !hasVehInterHandoff(dataDir) {
			return false
		}
		receiptErr = nil
	} else {
		_, configFile, resolveErr := resolveConfigPathsIn(configPath, runDir)
		currentVersion := ""
		var versionErr error = resolveErr
		if resolveErr == nil {
			currentVersion, versionErr = readVersionFile(configFile)
		}
		receiptErr = receiptError(newVersion, currentVersion, versionErr)
		if reason := readFinalizeFailReason(dataDir); reason != "" {
			receiptErr = errors.New(reason)
		}
	}

	// 发送：CPDS 地址在本地网段时单播；跨网段或地址为空时绑业务卡广播。
	sent := false
	if cpdsIP != "" {
		if dst := net.ParseIP(cpdsIP).To4(); dst != nil {
			if _, onSubnet, err := networkResolver(&net.UDPAddr{IP: dst}, nil); err == nil && onSubnet {
				_ = sendUpdateReceipt(s.conn, &net.UDPAddr{IP: dst, Port: serverPort}, s.info, newVersion, receiptErr)
				sent = true
			}
		}
	}
	if !sent {
		_ = s.sendReceiptBroadcast(persistedIP, newVersion, receiptErr)
	}
	_ = removeReceiptMarker(dataDir)
	_ = os.Remove(filepath.Join(dataDir, finalizeFailName))
	_ = os.Remove(filepath.Join(dataDir, vehInterHandoffName))
	return true
}

// sendReceiptBroadcast 在业务卡上绑一个临时广播 socket，向 255.255.255.255:39004
// 广播回执（连发 receiptSendCount 次）。仅在 marker 未记录 CPDS 地址时兜底使用。
func (s *Session) sendReceiptBroadcast(businessIP, newVersion string, receiptErr error) error {
	ip := net.ParseIP(businessIP).To4()
	if ip == nil {
		return errors.New("invalid business IP for receipt")
	}
	conn, err := openBroadcastSocket(ip)
	if err != nil {
		return err
	}
	defer conn.Close()
	return sendUpdateReceipt(conn, &net.UDPAddr{IP: net.IPv4bcast, Port: serverPort}, s.info, newVersion, receiptErr)
}

func hasVehInterHandoff(dataDir string) bool {
	data, err := os.ReadFile(filepath.Join(dataDir, vehInterHandoffName))
	return err == nil && len(data) > 0
}

// readFinalizeFailReason returns the failure reason left by the finalize
// helper, or an empty string when there is no failure marker.
func readFinalizeFailReason(dataDir string) string {
	data, err := os.ReadFile(filepath.Join(dataDir, finalizeFailName))
	if err != nil {
		return ""
	}
	return string(data)
}

// hasFinalizeFail reports whether the finalize helper left a failure marker.
func hasFinalizeFail(dataDir string) bool {
	return readFinalizeFailReason(dataDir) != ""
}

// RecoverFailedFinalize restores the old version into cpdc_config.json when a
// previous finalize left a failure marker. It is called before the startup
// version snapshot so the old binary reports its real version.
func RecoverFailedFinalize(runDir, configPath, cachePath string) {
	dataDir := runDir
	if cachePath != "" {
		dataDir = cachePath
	}
	if !hasFinalizeFail(dataDir) {
		return
	}
	params, err := LoadFinalizeParams(filepath.Join(dataDir, finalizeParamsName))
	if err != nil {
		return
	}
	if params.OldVersion != "" {
		_ = writeVersionToConfig(params.ConfigPath, runDir, params.OldVersion)
	}
}
