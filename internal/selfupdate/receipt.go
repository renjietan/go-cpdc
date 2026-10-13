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

// sendUpdateReceipt broadcasts update_ok or update_fail to 255.255.255.255:39004
// three times, one second apart, to tolerate UDP packet loss.
func sendUpdateReceipt(conn datagramConn, d DeviceInfo, newVersion string, err error) error {
	var data []byte
	if err != nil {
		data = encodeUpdateFail(d, err.Error())
	} else {
		data = encodeUpdateOk(d, newVersion)
	}
	destination := &net.UDPAddr{IP: net.IPv4bcast, Port: serverPort}
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

// ReceiptDestinations returns the broadcast and loopback addresses the startup
// receipt is sent to.
func ReceiptDestinations() []*net.UDPAddr {
	return []*net.UDPAddr{
		{IP: net.IPv4bcast, Port: serverPort},
		{IP: net.IPv4(127, 0, 0, 1), Port: serverPort},
	}
}

// SendStartupReceipt reads a pending receipt marker and, when present, replies
// to CPDS and deletes the marker. It reports whether a receipt was sent.
func (s *Session) SendStartupReceipt(runDir, configPath string) bool {
	dataDir := runDir
	if s.info.CachePath != "" {
		dataDir = s.info.CachePath
	}
	newVersion, persistedIP, ok := readReceiptMarker(dataDir)
	if !ok {
		return false
	}
	if persistedIP != "" {
		s.info.IP = persistedIP
	}

	// VehInter：只有第三方成功拉起后才回 update_ok。
	if s.info.DeviceType == "VehInter" {
		if !hasVehInterHandoff(dataDir) {
			return false
		}
		for _, writer := range s.writers {
			_ = sendUpdateReceipt(writer, s.info, newVersion, nil)
		}
		_ = removeReceiptMarker(dataDir)
		_ = os.Remove(filepath.Join(dataDir, vehInterHandoffName))
		return true
	}

	_, configFile, resolveErr := resolveConfigPathsIn(configPath, runDir)
	currentVersion := ""
	var versionErr error = resolveErr
	if resolveErr == nil {
		currentVersion, versionErr = readVersionFile(configFile)
	}
	receiptErr := receiptError(newVersion, currentVersion, versionErr)
	if reason := readFinalizeFailReason(dataDir); reason != "" {
		receiptErr = errors.New(reason)
	}
	for _, writer := range s.writers {
		_ = sendUpdateReceipt(writer, s.info, newVersion, receiptErr)
	}
	_ = removeReceiptMarker(dataDir)
	_ = os.Remove(filepath.Join(dataDir, finalizeFailName))
	return true
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
