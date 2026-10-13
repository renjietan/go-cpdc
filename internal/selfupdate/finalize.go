package selfupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	finalizeParamsName         = "finalize_params.json"
	finalizeFailName           = "finalize_fail"
	finalizeStartedName        = "finalize_started"
	oldExitedName              = "old_exited"
	oldExitedWriteErrorLogName = "old_exited_write_error.log"
)

const (
	finalizeWaitTimeout  = 10 * time.Second
	finalizeWaitInterval = 50 * time.Millisecond
	finalizeRenameTries  = 3
	finalizeRenameDelay  = 500 * time.Millisecond
)

// FinalizeParams is the JSON contract written by the old process and read by
// the --self-update-finalize process.
type FinalizeParams struct {
	OldPID          int      `json:"old_pid"`
	OldExe          string   `json:"old_exe"`
	NewExe          string   `json:"new_exe"`
	BackupExe       string   `json:"backup_exe"`
	RunCommand      []string `json:"run_command"`
	LogPath         string   `json:"log_path"`
	FinalizeLogPath string   `json:"finalize_log_path"`
	FailMarker      string   `json:"fail_marker"`
	DataDir         string   `json:"data_dir"`
	OldVersion      string   `json:"old_version"`
	ConfigPath      string   `json:"config_path"`
}

// LoadFinalizeParams reads and decodes the finalize parameter file.
func LoadFinalizeParams(path string) (FinalizeParams, error) {
	var params FinalizeParams
	data, err := os.ReadFile(path)
	if err != nil {
		return params, err
	}
	if err := json.Unmarshal(data, &params); err != nil {
		return params, err
	}
	return params, nil
}

func finalizeFailReason(code, detail string) string {
	if detail == "" {
		return code
	}
	return code + ":" + detail
}

// RunFinalize waits for the old process to exit, atomically swaps the new
// executable into place, starts the new process, and cleans up. On failure it
// writes a fail marker and rolls back to the old executable.
func RunFinalize(paramsPath string) error {
	params, err := LoadFinalizeParams(paramsPath)
	if err != nil {
		logFinalizeLoadParamsError(paramsPath, err)
		return fmt.Errorf("load finalize params: %w", err)
	}
	if params.OldExe == "" || params.NewExe == "" || params.BackupExe == "" || len(params.RunCommand) == 0 {
		return failFinalizeWithoutLog(params, finalizeFailReason("finalize_invalid_params", ""))
	}

	logFile, err := os.OpenFile(params.FinalizeLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return failFinalizeWithoutLog(params, finalizeFailReason("finalize_self_log_fail", params.FinalizeLogPath))
	}
	defer logFile.Close()
	logf := func(format string, args ...any) {
		_, _ = fmt.Fprintf(logFile, "%s [FINALIZE] %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
	}
	logf("finalize start old=%s new=%s backup=%s", params.OldExe, params.NewExe, params.BackupExe)

	// 通知旧进程 finalize 已启动（旧进程最多等 3 秒）。
	_ = writeFinalizeStarted(params.DataDir)

	if err := waitForOldExit(params); err != nil {
		logf("old process still alive: %v; terminating", err)
		_ = terminateProcess(params.OldPID)
		if err := waitForProcessExit(params.OldPID, time.Second); err != nil {
			return failFinalize(params, logFile, logf, finalizeFailReason("finalize_terminate_fail", fmt.Sprintf("%d:%v", params.OldPID, err)))
		}
	}

	if err := renameWithRetry(params.OldExe, params.BackupExe); err != nil {
		return failFinalize(params, logFile, logf, finalizeFailReason("finalize_rename_old_fail", params.OldExe+"->"+params.BackupExe+":"+err.Error()))
	}
	if err := renameWithRetry(params.NewExe, params.OldExe); err != nil {
		return failFinalize(params, logFile, logf, finalizeFailReason("finalize_rename_new_fail", params.NewExe+"->"+params.OldExe+":"+err.Error()))
	}

	newLog, err := os.OpenFile(params.LogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return failFinalize(params, logFile, logf, finalizeFailReason("finalize_open_log_fail", params.LogPath+":"+err.Error()))
	}
	if err := startDetached(params.RunCommand, newLog); err != nil {
		_ = newLog.Close()
		return failFinalize(params, logFile, logf, finalizeFailReason("finalize_start_new_fail", params.RunCommand[0]+":"+err.Error()))
	}
	_ = newLog.Close()

	// Success: remove the backup, params file, markers and staging directory.
	_ = os.Remove(params.BackupExe)
	_ = os.Remove(paramsPath)
	_ = os.Remove(params.FailMarker)
	_ = os.Remove(filepath.Join(params.DataDir, finalizeStartedName))
	_ = os.Remove(filepath.Join(params.DataDir, oldExitedName))
	_ = CleanupInstallDir(filepath.Dir(params.OldExe))
	logf("finalize ok")
	return nil
}

func failFinalize(params FinalizeParams, logFile *os.File, logf func(string, ...any), reason string) error {
	logf("finalize failed: %s", reason)
	_ = os.MkdirAll(filepath.Dir(params.FailMarker), 0o755)
	_ = os.WriteFile(params.FailMarker, []byte(reason), 0o600)

	// 恢复设备 config 的旧版本号，避免旧 exe 以新版本号运行。
	if params.OldVersion != "" {
		if err := writeVersionToConfig(params.ConfigPath, filepath.Dir(params.OldExe), params.OldVersion); err != nil {
			logf("restore old version failed: %v", err)
		}
	}

	_ = renameWithRetry(params.BackupExe, params.OldExe)
	_ = startDetached(params.RunCommand, logFile)
	return fmt.Errorf("%w: %s", errFinalizeFail, reason)
}

func failFinalizeWithoutLog(params FinalizeParams, reason string) error {
	marker := params.FailMarker
	if marker == "" && params.DataDir != "" {
		marker = filepath.Join(params.DataDir, finalizeFailName)
	}
	if marker != "" {
		_ = os.MkdirAll(filepath.Dir(marker), 0o755)
		_ = os.WriteFile(marker, []byte(reason), 0o600)
	}
	_ = renameWithRetry(params.BackupExe, params.OldExe)
	if len(params.RunCommand) > 0 {
		_ = startDetached(params.RunCommand, io.Discard)
	}
	return fmt.Errorf("%w: %s", errFinalizeFail, reason)
}

func logFinalizeLoadParamsError(paramsPath string, cause error) {
	dir := filepath.Dir(paramsPath)
	_ = os.MkdirAll(filepath.Join(dir, logDirName), 0o755)
	f, err := os.OpenFile(filepath.Join(dir, logDirName, "cpdc_restart.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s [FINALIZE] finalize_load_params_fail: %v\n", time.Now().Format("2006-01-02 15:04:05"), cause)
}

func writeFinalizeStarted(dataDir string) error {
	if dataDir == "" {
		return nil
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, finalizeStartedName), []byte("1"), 0o600)
}

var waitForFinalizeStarted = func(dataDir string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(filepath.Join(dataDir, finalizeStartedName)); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("finalize did not start")
		}
		time.Sleep(finalizeWaitInterval)
	}
}

func waitForOldExit(params FinalizeParams) error {
	deadline := time.Now().Add(finalizeWaitTimeout)
	marker := filepath.Join(params.DataDir, oldExitedName)
	for {
		if _, err := os.Stat(marker); err == nil {
			return nil
		}
		if !processAlive(params.OldPID) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("process did not exit")
		}
		time.Sleep(finalizeWaitInterval)
	}
}

var writeOldExited = func(dataDir string) {
	if dataDir == "" {
		return
	}
	path := filepath.Join(dataDir, oldExitedName)
	if err := os.WriteFile(path, []byte("1"), 0o600); err != nil {
		logOldExitedWriteError(dataDir, err)
	}
}

func logOldExitedWriteError(dataDir string, cause error) {
	write := func(dir string) bool {
		_ = os.MkdirAll(dir, 0o755)
		f, err := os.OpenFile(filepath.Join(dir, oldExitedWriteErrorLogName), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return false
		}
		defer f.Close()
		_, _ = fmt.Fprintf(f, "%s %v\n", time.Now().Format("2006-01-02 15:04:05"), cause)
		return true
	}
	if !write(filepath.Join(dataDir, logDirName)) {
		if runDir, err := executableDir(); err == nil {
			_ = write(filepath.Join(runDir, logDirName))
		}
	}
}

func waitForProcessExit(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			return errors.New("process did not exit")
		}
		time.Sleep(finalizeWaitInterval)
	}
	return nil
}

func renameWithRetry(oldPath, newPath string) error {
	var lastErr error
	for i := 0; i < finalizeRenameTries; i++ {
		if i > 0 {
			time.Sleep(finalizeRenameDelay)
		}
		if err := os.Rename(oldPath, newPath); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return lastErr
}

func startDetached(runCommand []string, stdout io.Writer) error {
	if len(runCommand) == 0 {
		return errors.New("empty run command")
	}
	cmd := newDetachedCommand(runCommand[0], runCommand[1:])
	cmd.Stdout = stdout
	cmd.Stderr = stdout
	return cmd.Start()
}
