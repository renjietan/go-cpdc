package selfupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type installOverrides struct {
	spawnedName string
	spawnedArgs []string
	exitCode    int
	exited      bool
}

func (o *installOverrides) apply(t *testing.T, exePath string) func() {
	t.Helper()
	oldExe := executablePath
	oldSpawn := spawnProcess
	oldExit := exitNow
	executablePath = func() (string, error) { return exePath, nil }
	spawnProcess = func(name string, args []string) error {
		o.spawnedName = name
		o.spawnedArgs = append([]string(nil), args...)
		return nil
	}
	exitNow = func(code int) {
		o.exitCode = code
		o.exited = true
	}
	return func() {
		executablePath = oldExe
		spawnProcess = oldSpawn
		exitNow = oldExit
	}
}

func TestFindInstallFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "CPDC-CCU"), "binary")
	writeFile(t, filepath.Join(dir, "cpdc_config.json"), "{}")
	writeFile(t, filepath.Join(dir, "ReReadJson.ini"), "x")

	got, err := findInstallFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "CPDC-CCU" {
		t.Fatalf("install file = %q", got)
	}
}

func TestFindInstallFileRejectsMissingAndAmbiguous(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "cpdc_config.json"), "{}")
		if _, err := findInstallFile(dir); err == nil {
			t.Fatal("findInstallFile succeeded, want error")
		}
	})
	t.Run("ambiguous", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "CPDC-CCU"), "a")
		writeFile(t, filepath.Join(dir, "checksum.txt"), "b")
		if _, err := findInstallFile(dir); err == nil {
			t.Fatal("findInstallFile succeeded, want error")
		}
	})
}

func TestKeepTempDir(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "1", want: true},
		{value: "true", want: true},
		{value: "yes", want: true},
		{value: "on", want: true},
		{value: "TRUE", want: true},
		{value: "0", want: false},
		{value: "false", want: false},
		{value: "", want: false},
		{value: "random", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv(keepTempEnv, tt.value)
			if got := keepTempDir(); got != tt.want {
				t.Fatalf("keepTempDir(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestCleanupInstallDir(t *testing.T) {
	runDir := t.TempDir()
	scriptPath := filepath.Join(runDir, installDirName, restartScriptName)
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CleanupInstallDir(runDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(runDir, installDirName)); !os.IsNotExist(err) {
		t.Fatalf("install dir still exists: %v", err)
	}
}

func TestSafeZipPathRejectsTraversal(t *testing.T) {
	for _, name := range []string{"", "../evil", "/abs", `..\evil`, "a/../../evil"} {
		if _, err := safeZipPath(name); err == nil {
			t.Fatalf("safeZipPath(%q) succeeded, want error", name)
		}
	}
	if got, err := safeZipPath("MR9360/CPDC-HF"); err != nil || got != "MR9360/CPDC-HF" {
		t.Fatalf("safeZipPath = %q, %v", got, err)
	}
}

func TestUnzipToTempExtractsNestedFiles(t *testing.T) {
	zipBytes := buildPackageZip(t, map[string]string{
		"MR9360/CPDC-HF":          "binary",
		"MR9360/cpdc_config.json": `{"version":"1.0.0.0"}`,
	})

	dir, err := unzipToTemp(zipBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	if got := readFile(t, filepath.Join(dir, "MR9360", "CPDC-HF")); got != "binary" {
		t.Fatalf("extracted binary = %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "MR9360", configFileName)); got != `{"version":"1.0.0.0"}` {
		t.Fatalf("extracted config = %q", got)
	}
}

func buildPackageZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestWriteFilesStagesBinaryAndParams(t *testing.T) {
	runDir := t.TempDir()
	exePath := filepath.Join(runDir, "CPDC-HF")
	iniDir := t.TempDir()
	iniPath := filepath.Join(iniDir, iniFileName)
	zipBytes := buildPackageZip(t, map[string]string{
		"MR9360/CPDC-HF":                 "BINARY-CONTENT",
		"MR9360/cpdc_config.json":        `{"version":"1.2.3.4","ReReadjsonPath":"` + filepath.ToSlash(iniPath) + `"}`,
		"MR9360/cpdc_config.MR9360.json": `{"deviceTypes":["HF"]}`,
		"MR9360/ReReadJson.ini":          "[ReReadVal]\nVal=1\n",
	})

	oldExe := executablePath
	executablePath = func() (string, error) { return exePath, nil }
	defer func() { executablePath = oldExe }()

	info := DeviceInfo{DeviceType: "MR9360", IP: "192.168.1.10", RunCommand: []string{"/opt/cpdc/CPDC-HF", "--config-path", "/data/user"}}
	paramsPath, gotRunDir, err := writeFiles(context.Background(), zipBytes, info, "9.9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if gotRunDir != runDir {
		t.Fatalf("runDir = %q, want %q", gotRunDir, runDir)
	}
	if got := readFile(t, filepath.Join(runDir, installDirName, "new_exe")); got != "BINARY-CONTENT" {
		t.Fatalf("staged binary = %q", got)
	}
	cfg := readJSON(t, filepath.Join(runDir, configFileName))
	if cfg["version"] != "9.9.9.9" {
		t.Fatalf("active config version = %v", cfg["version"])
	}
	if cfg["ReReadjsonPath"] != filepath.ToSlash(iniPath) {
		t.Fatalf("active config ReReadjsonPath = %v", cfg["ReReadjsonPath"])
	}
	if got := readFile(t, filepath.Join(runDir, "cpdc_config.MR9360.json")); got != `{"deviceTypes":["HF"]}` {
		t.Fatalf("type marker = %q", got)
	}
	if got := readFile(t, iniPath); got != "[ReReadVal]\nVal=1\n" {
		t.Fatalf("ini = %q", got)
	}
	marker := readJSON(t, filepath.Join(runDir, receiptMarkerName))
	if marker["version"] != "9.9.9.9" || marker["ip"] != "192.168.1.10" {
		t.Fatalf("marker = %v", marker)
	}
	params := readJSON(t, paramsPath)
	if params["old_exe"] != exePath {
		t.Fatalf("params old_exe = %v", params["old_exe"])
	}
	if params["old_version"] != "1.2.3.4" {
		t.Fatalf("params old_version = %v, want 1.2.3.4", params["old_version"])
	}
	if params["backup_exe"] != exePath+".9.9.9.9.bak" {
		t.Fatalf("params backup_exe = %v", params["backup_exe"])
	}
}

func TestWriteFilesMatchesTypeDirCaseInsensitively(t *testing.T) {
	runDir := t.TempDir()
	exePath := filepath.Join(runDir, "CPDC-CCU")
	writeFile(t, filepath.Join(runDir, configFileName), `{"deviceTypes":["CCU"]}`)
	zipBytes := buildPackageZip(t, map[string]string{"ccu/CPDC-CCU": "ccu-binary"})

	oldExe := executablePath
	executablePath = func() (string, error) { return exePath, nil }
	defer func() { executablePath = oldExe }()

	if _, _, err := writeFiles(context.Background(), zipBytes, DeviceInfo{DeviceType: "CCU", RunCommand: []string{"/opt/cpdc/CPDC-CCU"}}, "2.0.0.0"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(runDir, installDirName, "new_exe")); got != "ccu-binary" {
		t.Fatalf("staged binary = %q", got)
	}
}

func TestWriteFilesWritesVersionIntoExistingConfigWhenPackageLacksConfig(t *testing.T) {
	runDir := t.TempDir()
	exePath := filepath.Join(runDir, "CPDC-CCU")
	writeFile(t, filepath.Join(runDir, configFileName), `{"deviceTypes":["CCU"],"esn":"123456"}`)
	zipBytes := buildPackageZip(t, map[string]string{"CCU/CPDC-CCU": "ccu-binary"})

	oldExe := executablePath
	executablePath = func() (string, error) { return exePath, nil }
	defer func() { executablePath = oldExe }()

	if _, _, err := writeFiles(context.Background(), zipBytes, DeviceInfo{DeviceType: "CCU", RunCommand: []string{"/opt/cpdc/CPDC-CCU"}}, "2.0.0.0"); err != nil {
		t.Fatal(err)
	}
	cfg := readJSON(t, filepath.Join(runDir, configFileName))
	if cfg["version"] != "2.0.0.0" {
		t.Fatalf("version = %v", cfg["version"])
	}
	if cfg["esn"] != "123456" {
		t.Fatalf("esn lost: %v", cfg["esn"])
	}
}

func TestWriteFilesConfigPathDirectoryAndFile(t *testing.T) {
	for _, tt := range []struct {
		name     string
		asFile   bool
		wantName string
	}{
		{name: "directory", asFile: false, wantName: configFileName},
		{name: "file", asFile: true, wantName: "cpdc-config-custom.json"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runDir := t.TempDir()
			exePath := filepath.Join(runDir, "CPDC-CCU")
			configDir := t.TempDir()
			var configPath string
			if tt.asFile {
				configPath = filepath.Join(configDir, tt.wantName)
				writeFile(t, configPath, `{"deviceTypes":["CCU"]}`)
			} else {
				configPath = configDir
				writeFile(t, filepath.Join(configDir, configFileName), `{"deviceTypes":["CCU"]}`)
			}
			zipBytes := buildPackageZip(t, map[string]string{"CCU/CPDC-CCU": "ccu-binary"})

			oldExe := executablePath
			executablePath = func() (string, error) { return exePath, nil }
			defer func() { executablePath = oldExe }()

			info := DeviceInfo{DeviceType: "CCU", ConfigPath: configPath, RunCommand: []string{"/opt/cpdc/CPDC-CCU"}}
			if _, _, err := writeFiles(context.Background(), zipBytes, info, "2.0.0.0"); err != nil {
				t.Fatal(err)
			}
			if got := readJSON(t, filepath.Join(configDir, tt.wantName))["version"]; got != "2.0.0.0" {
				t.Fatalf("config version = %v", got)
			}
		})
	}
}

func TestWriteFilesFailsWhenTypeDirMissing(t *testing.T) {
	runDir := t.TempDir()
	exePath := filepath.Join(runDir, "CPDC-CCU")
	zipBytes := buildPackageZip(t, map[string]string{"Other/CPDC-CCU": "binary"})

	oldExe := executablePath
	executablePath = func() (string, error) { return exePath, nil }
	defer func() { executablePath = oldExe }()

	if _, _, err := writeFiles(context.Background(), zipBytes, DeviceInfo{DeviceType: "CCU", RunCommand: []string{"/opt/cpdc/CPDC-CCU"}}, "2.0.0.0"); err == nil {
		t.Fatal("writeFiles succeeded, want error")
	}
}

func TestWriteFilesFailsWhenInstallFileMissing(t *testing.T) {
	runDir := t.TempDir()
	exePath := filepath.Join(runDir, "CPDC-CCU")
	zipBytes := buildPackageZip(t, map[string]string{"CCU/cpdc_config.json": `{"version":"1.0.0.0"}`})

	oldExe := executablePath
	executablePath = func() (string, error) { return exePath, nil }
	defer func() { executablePath = oldExe }()

	if _, _, err := writeFiles(context.Background(), zipBytes, DeviceInfo{DeviceType: "CCU", RunCommand: []string{"/opt/cpdc/CPDC-CCU"}}, "2.0.0.0"); err == nil {
		t.Fatal("writeFiles succeeded, want error")
	}
}

func TestWriteFilesUsesCachePathForNonExecutables(t *testing.T) {
	runDir := t.TempDir()
	cacheDir := t.TempDir()
	exePath := filepath.Join(runDir, "CPDC-HF")
	zipBytes := buildPackageZip(t, map[string]string{
		"MR9360/CPDC-HF":          "BINARY-CONTENT",
		"MR9360/cpdc_config.json": `{"version":"2.0.0.0"}`,
	})

	oldExe := executablePath
	executablePath = func() (string, error) { return exePath, nil }
	defer func() { executablePath = oldExe }()

	info := DeviceInfo{
		DeviceType: "MR9360",
		IP:         "10.0.0.1",
		RunCommand: []string{"/opt/cpdc/CPDC-HF"},
		CachePath:  cacheDir,
	}
	paramsPath, _, err := writeFiles(context.Background(), zipBytes, info, "2.0.0.0")
	if err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, filepath.Join(runDir, installDirName, "new_exe")); got != "BINARY-CONTENT" {
		t.Fatalf("new exe should stay in run dir, got %q", got)
	}
	if got := readJSON(t, filepath.Join(cacheDir, receiptMarkerName)); got["version"] != "2.0.0.0" {
		t.Fatalf("marker should be in cache dir, got %v", got)
	}
	if paramsPath != filepath.Join(cacheDir, finalizeParamsName) {
		t.Fatalf("params path = %q, want %q", paramsPath, filepath.Join(cacheDir, finalizeParamsName))
	}
	params := readJSON(t, paramsPath)
	if params["fail_marker"] != filepath.Join(cacheDir, finalizeFailName) {
		t.Fatalf("fail_marker = %v", params["fail_marker"])
	}
	if params["finalize_log_path"] != filepath.Join(cacheDir, logDirName, "cpdc_restart.log") {
		t.Fatalf("finalize_log_path = %v", params["finalize_log_path"])
	}
}
