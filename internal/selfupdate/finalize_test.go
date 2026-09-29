package selfupdate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFinalizeParams(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, finalizeParamsName)
	params := FinalizeParams{
		OldPID:     123,
		OldExe:     filepath.Join(dir, "old"),
		NewExe:     filepath.Join(dir, "new"),
		BackupExe:  filepath.Join(dir, "old.bak"),
		RunCommand: []string{"/opt/cpdc/CPDC-HF"},
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadFinalizeParams(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.OldPID != 123 || got.OldExe != params.OldExe || got.BackupExe != params.BackupExe {
		t.Fatalf("params = %+v", got)
	}
}

func TestLoadFinalizeParamsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, finalizeParamsName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFinalizeParams(path); err == nil {
		t.Fatal("LoadFinalizeParams succeeded, want error")
	}
}

func TestRunFinalizeRejectsInvalidParams(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, finalizeParamsName)
	if err := os.WriteFile(path, []byte(`{"old_pid":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RunFinalize(path); err == nil {
		t.Fatal("RunFinalize succeeded, want error")
	}
}

func TestHasFinalizeFail(t *testing.T) {
	dir := t.TempDir()
	if hasFinalizeFail(dir) {
		t.Fatal("expected no finalize fail marker")
	}
	if err := os.MkdirAll(filepath.Join(dir, installDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, installDirName, finalizeFailName), []byte("boom"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !hasFinalizeFail(dir) {
		t.Fatal("expected finalize fail marker")
	}
}
