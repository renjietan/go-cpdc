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
	finalizeParamsName = "finalize_params.json"
	finalizeFailName   = "finalize_fail"
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

// RunFinalize waits for the old process to exit, atomically swaps the new
// executable into place, starts the new process, and cleans up. On failure it
// writes a fail marker and rolls back to the old executable.
func RunFinalize(paramsPath string) error {
	params, err := LoadFinalizeParams(paramsPath)
	if err != nil {
		return fmt.Errorf("load finalize params: %w", err)
	}
	if params.OldExe == "" || params.NewExe == "" || params.BackupExe == "" || len(params.RunCommand) == 0 {
		return errors.New("invalid finalize params")
	}

	logFile, err := os.OpenFile(params.FinalizeLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open finalize log: %w", err)
	}
	defer logFile.Close()
	logf := func(format string, args ...any) {
		_, _ = fmt.Fprintf(logFile, "%s [FINALIZE] %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
	}
	logf("finalize start old=%s new=%s backup=%s", params.OldExe, params.NewExe, params.BackupExe)

	if err := waitForProcessExit(params.OldPID, finalizeWaitTimeout); err != nil {
		logf("old process still alive: %v; terminating", err)
		_ = terminateProcess(params.OldPID)
		if err := waitForProcessExit(params.OldPID, time.Second); err != nil {
			return failFinalize(params, logFile, logf, fmt.Errorf("terminate old process: %w", err))
		}
	}

	if err := renameWithRetry(params.OldExe, params.BackupExe); err != nil {
		return failFinalize(params, logFile, logf, fmt.Errorf("rename old exe: %w", err))
	}
	if err := renameWithRetry(params.NewExe, params.OldExe); err != nil {
		return failFinalize(params, logFile, logf, fmt.Errorf("rename new exe: %w", err))
	}

	newLog, err := os.OpenFile(params.LogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return failFinalize(params, logFile, logf, fmt.Errorf("open new log: %w", err))
	}
	if err := startDetached(params.RunCommand, newLog); err != nil {
		_ = newLog.Close()
		return failFinalize(params, logFile, logf, fmt.Errorf("start new process: %w", err))
	}
	_ = newLog.Close()

	// Success: remove the backup, params file and any previous fail marker, then
	// clean the staging directory.
	_ = os.Remove(params.BackupExe)
	_ = os.Remove(paramsPath)
	_ = os.Remove(params.FailMarker)
	_ = CleanupInstallDir(filepath.Dir(params.OldExe))
	logf("finalize ok")
	return nil
}

func failFinalize(params FinalizeParams, logFile *os.File, logf func(string, ...any), cause error) error {
	logf("finalize failed: %v", cause)
	_ = os.MkdirAll(filepath.Dir(params.FailMarker), 0o755)
	_ = os.WriteFile(params.FailMarker, []byte(cause.Error()), 0o600)

	// Roll back: put the backup back and start the old executable again. If a
	// rename already failed, keep going as far as possible.
	_ = renameWithRetry(params.BackupExe, params.OldExe)
	oldLog := logFile
	if oldLog != nil {
		_ = startDetached(params.RunCommand, oldLog)
	}
	return fmt.Errorf("%w: %v", errFinalizeFail, cause)
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
