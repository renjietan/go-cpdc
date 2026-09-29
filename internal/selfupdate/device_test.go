package selfupdate

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetectDeviceTypeReadsTypeFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "cpdc_config.json"), `{"deviceTypes":["HF"]}`)
	writeFile(t, filepath.Join(dir, "cpdc_config.MR9360.json"), `{"deviceTypes":["HF"]}`)
	writeFile(t, filepath.Join(dir, "ReReadJson.ini"), "x")

	got, err := DetectDeviceType(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "MR9360" {
		t.Fatalf("DetectDeviceType = %q, want MR9360", got)
	}
}

func TestDetectDeviceTypeLooksBesideConfigFile(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "cpdc-config-vehInte.json")
	writeFile(t, configFile, `{"deviceTypes":["VehInter"]}`)
	writeFile(t, filepath.Join(dir, "cpdc_config.VehInter.json"), `{"deviceTypes":["VehInter"]}`)

	got, err := DetectDeviceType(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if got != "VehInter" {
		t.Fatalf("DetectDeviceType = %q, want VehInter", got)
	}
}

func TestDetectDeviceTypeIgnoresDirectoriesAndNonMatchingFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "cpdc_config.CCU.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "cpdc_config.CCU.json.backup"), "x")
	writeFile(t, filepath.Join(dir, "readme.txt"), "x")
	writeFile(t, filepath.Join(dir, "cpdc_config.CCUAudio.json"), `{}`)

	got, err := DetectDeviceType(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "CCUAudio" {
		t.Fatalf("DetectDeviceType = %q, want CCUAudio", got)
	}
}

func TestDetectDeviceTypeReportsMissingAndAmbiguous(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "cpdc_config.json"), `{}`)
		if _, err := DetectDeviceType(dir); err == nil {
			t.Fatal("DetectDeviceType succeeded, want error")
		}
	})

	t.Run("ambiguous", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "cpdc_config.CCU.json"), `{}`)
		writeFile(t, filepath.Join(dir, "cpdc_config.VehInter.json"), `{}`)
		if _, err := DetectDeviceType(dir); err == nil {
			t.Fatal("DetectDeviceType succeeded, want error")
		}
	})
}

func TestReadVersionReadsRootVersion(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "cpdc_config.json"), `{"version":"1.2.3"}`)
		got, err := ReadVersion(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got != "1.2.3" {
			t.Fatalf("ReadVersion = %q, want 1.2.3", got)
		}
	})

	t.Run("explicit file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "custom.json")
		writeFile(t, path, `{"version":"0.1.1.1"}`)
		got, err := ReadVersion(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != "0.1.1.1" {
			t.Fatalf("ReadVersion = %q, want 0.1.1.1", got)
		}
	})
}

func TestReadVersionToleratesMissingOrNonStringVersion(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing", body: `{"deviceTypes":["CCU"]}`},
		{name: "number", body: `{"version":3}`},
		{name: "null", body: `{"version":null}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "cpdc_config.json"), tt.body)
			got, err := ReadVersion(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got != "" {
				t.Fatalf("ReadVersion = %q, want empty", got)
			}
		})
	}
}

func TestReadVersionReportsReadAndDecodeErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		if _, err := ReadVersion(filepath.Join(t.TempDir(), "nope")); err == nil {
			t.Fatal("ReadVersion succeeded, want error")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "cpdc_config.json"), `{"version":`)
		if _, err := ReadVersion(dir); err == nil {
			t.Fatal("ReadVersion succeeded, want error")
		}
	})
}

func TestResolveConfigPaths(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "custom.json")

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wantDefault := filepath.Join(filepath.Dir(executable), configFileName)

	t.Run("empty", func(t *testing.T) {
		gotDir, gotFile, err := resolveConfigPaths("")
		if err != nil {
			t.Fatal(err)
		}
		if gotDir != filepath.Dir(executable) {
			t.Fatalf("config dir = %q, want %q", gotDir, filepath.Dir(executable))
		}
		if gotFile != wantDefault {
			t.Fatalf("config file = %q, want %q", gotFile, wantDefault)
		}
	})

	t.Run("directory", func(t *testing.T) {
		gotDir, gotFile, err := resolveConfigPaths(dir)
		if err != nil {
			t.Fatal(err)
		}
		if gotDir != dir {
			t.Fatalf("config dir = %q, want %q", gotDir, dir)
		}
		if gotFile != filepath.Join(dir, configFileName) {
			t.Fatalf("config file = %q, want %q", gotFile, filepath.Join(dir, configFileName))
		}
	})

	t.Run("file", func(t *testing.T) {
		gotDir, gotFile, err := resolveConfigPaths(file)
		if err != nil {
			t.Fatal(err)
		}
		if gotDir != dir {
			t.Fatalf("config dir = %q, want %q", gotDir, dir)
		}
		if gotFile != file {
			t.Fatalf("config file = %q, want %q", gotFile, file)
		}
	})

	t.Run("missing file path", func(t *testing.T) {
		missing := filepath.Join(dir, "missing.json")
		gotDir, gotFile, err := resolveConfigPaths(missing)
		if err != nil {
			t.Fatal(err)
		}
		if gotDir != dir {
			t.Fatalf("config dir = %q, want %q", gotDir, dir)
		}
		if gotFile != missing {
			t.Fatalf("config file = %q, want %q", gotFile, missing)
		}
	})
}

func TestReadVersionLooksBesideConfigFile(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "custom.json")
	writeFile(t, configFile, `{"version":"9.8.7"}`)

	got, err := ReadVersion(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if got != "9.8.7" {
		t.Fatalf("ReadVersion = %q, want 9.8.7", got)
	}
}

func TestIPDetectorCachesResult(t *testing.T) {
	t.Run("value", func(t *testing.T) {
		var calls int
		var detector ipDetector
		resolve := func() (string, error) {
			calls++
			return "192.168.1.10", nil
		}
		for i := 0; i < 3; i++ {
			got, err := detector.detect(resolve)
			if err != nil {
				t.Fatal(err)
			}
			if got != "192.168.1.10" {
				t.Fatalf("detect = %q, want 192.168.1.10", got)
			}
		}
		if calls != 1 {
			t.Fatalf("resolver called %d times, want 1", calls)
		}
	})

	t.Run("error", func(t *testing.T) {
		var calls int
		var detector ipDetector
		resolve := func() (string, error) {
			calls++
			return "", errors.New("no interface")
		}
		for i := 0; i < 2; i++ {
			if _, err := detector.detect(resolve); err == nil {
				t.Fatal("detect succeeded, want error")
			}
		}
		if calls != 1 {
			t.Fatalf("resolver called %d times, want 1", calls)
		}
	})
}

func TestDetectLocalIPReturnsUsableIPv4(t *testing.T) {
	ip, err := detectLocalIP()
	if err == nil {
		if parsed := net.ParseIP(ip); parsed == nil || parsed.To4() == nil {
			t.Fatalf("detectLocalIP = %q, want IPv4", ip)
		}
	}
}

func TestParseRunCommand(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCmd    []string
		wantConfig string
	}{
		{
			name:       "space separated",
			args:       []string{"cpdc", "--config-path", "/data/user", "--log-level", "debug"},
			wantCmd:    []string{"cpdc", "--config-path", "/data/user", "--log-level", "debug"},
			wantConfig: "/data/user",
		},
		{
			name:       "equals separated",
			args:       []string{"cpdc", "--config-path=/data/user/config.json"},
			wantCmd:    []string{"cpdc", "--config-path=/data/user/config.json"},
			wantConfig: "/data/user/config.json",
		},
		{
			name:       "single dash",
			args:       []string{"cpdc", "-config-path", "/data/user", "-interface", "eth0"},
			wantCmd:    []string{"cpdc", "-config-path", "/data/user", "-interface", "eth0"},
			wantConfig: "/data/user",
		},
		{
			name:       "single dash equals",
			args:       []string{"cpdc", "-config-path=/data/user"},
			wantCmd:    []string{"cpdc", "-config-path=/data/user"},
			wantConfig: "/data/user",
		},
		{
			name:       "absent",
			args:       []string{"cpdc", "--log-level", "debug"},
			wantCmd:    []string{"cpdc", "--log-level", "debug"},
			wantConfig: "",
		},
		{
			name:       "trailing flag without value",
			args:       []string{"cpdc", "--config-path"},
			wantCmd:    []string{"cpdc", "--config-path"},
			wantConfig: "",
		},
		{
			name:       "first occurrence wins",
			args:       []string{"cpdc", "--config-path", "first", "--config-path", "second"},
			wantCmd:    []string{"cpdc", "--config-path", "first", "--config-path", "second"},
			wantConfig: "first",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCmd, gotConfig := ParseRunCommand(tt.args)
			if !reflect.DeepEqual(gotCmd, tt.wantCmd) {
				t.Fatalf("run command = %v, want %v", gotCmd, tt.wantCmd)
			}
			if gotConfig != tt.wantConfig {
				t.Fatalf("config path = %q, want %q", gotConfig, tt.wantConfig)
			}
		})
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
