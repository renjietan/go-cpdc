package selfupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"cpdc/internal/fsutil"
)

const (
	receiptMarkerName   = ".cpdc_update_receipt.json"
	vehInterHandoffName = "vehinter_handoff"
	iniFileName         = "ReReadJson.ini"
	installDirName      = "install_cpdc"
	logDirName          = "logs"
	restartScriptName   = "restart.sh"
)

// writeError is a self-update write-phase failure. code is the wire reason code
// and path is an optional detail appended after ":" in the wire reason.
type writeError struct {
	code string
	path string
}

func (e *writeError) Error() string {
	if e.path == "" {
		return e.code
	}
	return e.code + ":" + e.path
}

// errRestartError marks the launch-script failure reported through update_fail.
var errRestartError = errors.New("restart_error")

// Install-file discovery sentinels.
var (
	errInstallFileNotFound = errors.New("install_file_not_found")
	errInstallFileMultiple = errors.New("install_file_multiple")
)

// writeFailReason returns the wire reason string for a write-phase error.
func writeFailReason(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// phaseWriteError builds a writeError for an atomic-copy failure, classifying
// the failure stage into open / temp / rename.
func phaseWriteError(prefix, path string, err error) error {
	phase := "temp"
	switch {
	case errors.Is(err, fsutil.ErrOpenSource):
		phase = "open"
	case errors.Is(err, fsutil.ErrRename):
		phase = "rename"
	}
	return &writeError{code: prefix + "_" + phase + "_fail", path: path}
}

// keepTempEnv enables a debugging switch that keeps the extracted install
// temporary directory instead of removing it after the install attempt.
const keepTempEnv = "CPDC_KEEP_TEMP"

// keepTempDir reports whether the extracted temporary directory should be kept.
func keepTempDir() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(keepTempEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// spawnProcess starts a detached replacement process for restartSelf.
var spawnProcess = func(name string, args []string) error {
	cmd := newDetachedCommand(name, args)
	return cmd.Start()
}

// exitNow terminates the current process after the replacement is launched.
var exitNow = func(code int) { os.Exit(code) }

// writeFiles performs the whole write phase (before restart). On success it
// returns the restart script path and the run directory; on failure it returns
// a *writeError carrying the wire reason code and optional path.
func writeFiles(ctx context.Context, zipBytes []byte, info DeviceInfo, targetVersion string) (paramsPath, runDir string, err error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	temp, err := unzipToTemp(zipBytes)
	if err != nil {
		return "", "", &writeError{code: "unzip_fail"}
	}
	fmt.Fprintf(os.Stdout, "[TEMP] self-update 解压临时目录: %s\n", temp)
	if keepTempDir() {
		fmt.Fprintf(os.Stdout, "[KEEP] %s 已设置，保留临时目录: %s\n", keepTempEnv, temp)
	} else {
		defer func() { _ = os.RemoveAll(temp) }()
	}

	typeDir, err := findTypeDir(temp, info.DeviceType)
	if err != nil {
		return "", "", &writeError{code: "type_dir_not_found", path: info.DeviceType}
	}
	installFile, err := findInstallFile(typeDir)
	if err != nil {
		return "", "", installFileError(err)
	}
	reReadjsonPath, err := readPackageConfig(typeDir)
	if err != nil {
		return "", "", &writeError{code: "read_config_fail", path: filepath.Join(typeDir, configFileName)}
	}
	exePath, err := executablePath()
	if err != nil {
		return "", "", &writeError{code: "executable_path_fail"}
	}
	runDir = filepath.Dir(exePath)
	dataDir, dataErr := resolveDataDir(info)
	if dataErr != nil {
		return "", "", &writeError{code: "executable_path_fail"}
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", "", &writeError{code: "mkdir_fail", path: dataDir}
	}

	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if err := overwriteJSON(typeDir, info.ConfigPath, runDir, info.DeviceType); err != nil {
		return "", "", err
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if err := overwriteINI(typeDir, runDir, reReadjsonPath); err != nil {
		return "", "", err
	}

	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if err := writeReceiptMarker(targetVersion, info.IP, dataDir); err != nil {
		return "", "", &writeError{code: "write_marker_fail", path: filepath.Join(dataDir, receiptMarkerName)}
	}
	// The update is committed past this point: no more cancellation checks.
	_, configFile, _ := resolveConfigPathsIn(info.ConfigPath, runDir)
	oldVersion, _ := readVersionFile(configFile)
	if err := writeVersionToConfig(info.ConfigPath, runDir, targetVersion); err != nil {
		_ = removeReceiptMarker(dataDir)
		return "", "", &writeError{code: "version_write_fail", path: configFile}
	}
	stagingDir := filepath.Join(runDir, installDirName)
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		_ = removeReceiptMarker(dataDir)
		return "", "", &writeError{code: "mkdir_fail", path: stagingDir}
	}
	newExePath := filepath.Join(stagingDir, "new_exe")
	if err := fsutil.AtomicCopy(filepath.Join(typeDir, installFile), newExePath, 0o755); err != nil {
		_ = removeReceiptMarker(dataDir)
		return "", "", phaseWriteError("overwrite_binary", newExePath, err)
	}
	if info.DeviceType == "VehInter" {
		// VehInter 由第三方接管替换与拉起，不写 finalize 参数。
		return "", runDir, nil
	}
	if info.DeviceType != "VehInter" {
		paramsPath, err = prepareFinalizeParams(info, targetVersion, runDir, dataDir, newExePath, oldVersion)
		if err != nil {
			_ = removeReceiptMarker(dataDir)
			return "", "", err
		}
	}
	return paramsPath, runDir, nil
}

// prepareFinalizeParams writes the finalize parameter file for the replacement
// process and returns its path.
func prepareFinalizeParams(info DeviceInfo, targetVersion, runDir, dataDir, newExePath, oldVersion string) (string, error) {
	exePath, err := executablePath()
	if err != nil {
		return "", &writeError{code: "executable_path_fail"}
	}
	logDir := filepath.Join(dataDir, logDirName)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", &writeError{code: "mkdir_fail", path: logDir}
	}
	params := FinalizeParams{
		OldPID:          os.Getpid(),
		OldExe:          exePath,
		NewExe:          newExePath,
		BackupExe:       exePath + "." + targetVersion + ".bak",
		RunCommand:      info.RunCommand,
		LogPath:         filepath.Join(logDir, "cpdc_"+targetVersion+".log"),
		FinalizeLogPath: filepath.Join(logDir, "cpdc_restart.log"),
		FailMarker:      filepath.Join(dataDir, finalizeFailName),
		DataDir:         dataDir,
		OldVersion:      oldVersion,
		ConfigPath:      info.ConfigPath,
	}
	paramsPath := filepath.Join(dataDir, finalizeParamsName)
	data, err := json.MarshalIndent(params, "", "  ")
	if err != nil {
		return "", &writeError{code: "write_params_fail", path: paramsPath}
	}
	if err := os.WriteFile(paramsPath, data, 0o600); err != nil {
		return "", &writeError{code: "write_params_fail", path: paramsPath}
	}
	return paramsPath, nil
}

// installFileError maps a findInstallFile error to its write reason.
func installFileError(err error) error {
	if errors.Is(err, errInstallFileMultiple) {
		return &writeError{code: "install_file_multiple"}
	}
	return &writeError{code: "install_file_not_found"}
}

// updateFailReason maps a launch error to its update_fail reason code.
func updateFailReason(err error) string {
	return errRestartError.Error()
}

func unzipToTemp(zipBytes []byte) (string, error) {
	dir, err := os.MkdirTemp("", "cpdc-update-*")
	if err != nil {
		return "", err
	}
	reader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("open update ZIP: %w", err)
	}
	if err := extractZip(reader, dir); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func extractZip(reader *zip.Reader, dir string) error {
	for _, file := range reader.File {
		if err := extractZipEntry(dir, file); err != nil {
			return err
		}
	}
	return nil
}

func extractZipEntry(dir string, file *zip.File) error {
	name, err := safeZipPath(file.Name)
	if err != nil {
		return err
	}
	target := filepath.Join(dir, filepath.FromSlash(name))
	if file.FileInfo().IsDir() {
		return os.MkdirAll(target, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, rc)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func safeZipPath(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, 0) || strings.Contains(name, `\`) {
		return "", errors.New("unsafe ZIP path")
	}
	clean := path.Clean(name)
	if clean == "." || path.IsAbs(clean) || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", errors.New("unsafe ZIP path")
	}
	return clean, nil
}

// findTypeDir returns the directory below root whose name matches deviceType
// case-insensitively.
func findTypeDir(root, deviceType string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.EqualFold(entry.Name(), deviceType) {
			return filepath.Join(root, entry.Name()), nil
		}
	}
	return "", fmt.Errorf("device type directory %q not found", deviceType)
}

// findInstallFile returns the single install file below typeDir. The install
// file is identified as a file with no extension whose name starts with the
// uppercase "CPDC-" prefix; other files the device may carry (log / sh / json /
// ini / etc.) are ignored.
func findInstallFile(typeDir string) (string, error) {
	entries, err := os.ReadDir(typeDir)
	if err != nil {
		return "", err
	}
	var found string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !isInstallFileName(name, platformIsWindows) {
			continue
		}
		if found != "" {
			return "", fmt.Errorf("%w: %s and %s", errInstallFileMultiple, found, name)
		}
		found = name
	}
	if found == "" {
		return "", errInstallFileNotFound
	}
	return found, nil
}

// platformIsWindows reports whether the current platform is Windows. It is a
// package variable so tests can override it.
var platformIsWindows = runtime.GOOS == "windows"

// isInstallFileName reports whether name matches the install-file naming rule
// for the given platform:
//   - non-Windows: no extension and an uppercase "CPDC-" prefix;
//   - Windows: a "CPDC" prefix with a ".exe" extension.
func isInstallFileName(name string, isWindows bool) bool {
	if isWindows {
		return strings.HasPrefix(name, "CPDC") && strings.HasSuffix(name, ".exe")
	}
	return filepath.Ext(name) == "" && strings.HasPrefix(name, "CPDC-")
}

// readPackageConfig returns ReReadjsonPath from the package's cpdc_config.json.
// A missing config is treated as an empty value.
func readPackageConfig(typeDir string) (string, error) {
	actual, ok := findFileFold(typeDir, configFileName)
	if !ok {
		return "", nil
	}
	data, err := os.ReadFile(filepath.Join(typeDir, actual))
	if err != nil {
		return "", err
	}
	var raw struct {
		ReReadjsonPath string `json:"ReReadjsonPath"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", err
	}
	return raw.ReReadjsonPath, nil
}

// overwriteJSON replaces the active config file and the type marker when the
// corresponding source files are present in the package.
func overwriteJSON(typeDir, configPath, runDir, deviceType string) error {
	configDir, configFile, err := resolveConfigPathsIn(configPath, runDir)
	if err != nil {
		return err
	}
	if err := copyIfExists(typeDir, configFileName, configFile, 0o600, "overwrite_config"); err != nil {
		return err
	}
	typeMarker := "cpdc_config." + deviceType + ".json"
	if err := copyIfExists(typeDir, typeMarker, filepath.Join(configDir, typeMarker), 0o600, "overwrite_config"); err != nil {
		return err
	}
	return nil
}

func overwriteInstallFile(typeDir, installFile, exePath string) error {
	if err := fsutil.AtomicCopy(filepath.Join(typeDir, installFile), exePath, 0o755); err != nil {
		return phaseWriteError("overwrite_binary", exePath, err)
	}
	return nil
}

// overwriteINI replaces ReReadJson.ini when present. reReadjsonPath is the full
// destination file path when set; otherwise it lands beside the executable.
func overwriteINI(typeDir, runDir, reReadjsonPath string) error {
	dst := filepath.Join(runDir, iniFileName)
	if reReadjsonPath != "" {
		dst = reReadjsonPath
	}
	return copyIfExists(typeDir, iniFileName, dst, 0o644, "overwrite_ini")
}

func writeReceiptMarker(newVersion, ip, runDir string) error {
	data, err := json.Marshal(map[string]string{"version": newVersion, "ip": ip})
	if err != nil {
		return err
	}
	return fsutil.AtomicWriteFile(filepath.Join(runDir, receiptMarkerName), data, 0o600)
}

func removeReceiptMarker(runDir string) error {
	err := os.Remove(filepath.Join(runDir, receiptMarkerName))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// writeVersionToConfig sets the root version field of the active config,
// preserving every other field.
func writeVersionToConfig(configPath, runDir, newVersion string) error {
	_, configFile, err := resolveConfigPathsIn(configPath, runDir)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		return err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	raw["version"] = newVersion
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	return fsutil.AtomicWriteFile(configFile, out, 0o600)
}

func copyIfExists(srcDir, srcName, dst string, perm os.FileMode, prefix string) error {
	actual, ok := findFileFold(srcDir, srcName)
	if !ok {
		return nil
	}
	if err := fsutil.AtomicCopy(filepath.Join(srcDir, actual), dst, perm); err != nil {
		return phaseWriteError(prefix, dst, err)
	}
	return nil
}

func findFileFold(dir, name string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.EqualFold(entry.Name(), name) {
			return entry.Name(), true
		}
	}
	return "", false
}

// CleanupInstallDir removes the restart script staging directory.
func CleanupInstallDir(runDir string) error {
	return os.RemoveAll(filepath.Join(runDir, installDirName))
}
