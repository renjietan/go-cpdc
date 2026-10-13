package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	cpdv1 "cpdc/gen/cpd/v1"
)

func TestLoadGeneratesAndPersistsESN(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, map[string]any{
		"deviceTypes": []string{"CCU", "TEDS"},
		"esn":         "",
		"txbzName":    "",
		"txbzHash":    "",
		"nodeId":      "",
		"deviceIds":   map[string]string{"CCU": "", "TEDS": ""},
	})

	random := bytes.NewReader(append(make([]byte, 16), bytes.Repeat([]byte{1}, 16)...))
	cfg, err := Load(root, random)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9]{39}$`).MatchString(cfg.ESN) {
		t.Fatalf("ESN = %q, want exactly 39 decimal digits", cfg.ESN)
	}
	if strings.Trim(cfg.ESN, "0") == "" {
		t.Fatal("ESN must not be all zero")
	}

	reloaded, err := Load(root, bytes.NewReader(bytes.Repeat([]byte{2}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ESN != cfg.ESN {
		t.Fatalf("persisted ESN = %q, want %q", reloaded.ESN, cfg.ESN)
	}
}

func TestLoadValidatesDeviceTypeCombinations(t *testing.T) {
	tests := []struct {
		name  string
		types []string
	}{
		{name: "empty", types: nil},
		{name: "unknown", types: []string{"Radar"}},
		{name: "non-canonical handheld spelling", types: []string{"MultiBandHandheld"}},
		{name: "duplicate", types: []string{"CCU", "CCU"}},
		{name: "server and IEC", types: []string{"TEDS", "IEC"}},
		{name: "two radios", types: []string{"MultiBandRadio", "HF"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeConfig(t, root, map[string]any{
				"deviceTypes": tt.types,
				"esn":         strings.Repeat("1", 39),
				"deviceIds":   map[string]string{},
			})
			if _, err := Load(root, bytes.NewReader(nil)); err == nil {
				t.Fatal("Load succeeded, want validation error")
			}
		})
	}
}

func TestLoadAcceptsStandaloneCCUAudio(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, map[string]any{
		"deviceTypes": []string{"CCUAudio"},
		"esn":         strings.Repeat("2", 39),
		"deviceIds":   map[string]string{"CCUAudio": ""},
	})

	cfg, err := Load(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.DeviceTypes) != 1 || cfg.DeviceTypes[0] != cpdv1.DeviceType_DEVICE_TYPE_CCU_AUDIO {
		t.Fatalf("device types = %v, want CCUAudio(8)", cfg.DeviceTypes)
	}
	if got := DeviceTypeName(cfg.DeviceTypes[0]); got != "CCUAudio" {
		t.Fatalf("device type name = %q", got)
	}
}

func TestLoadRejectsCCUAudioCombinedWithAnotherType(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, map[string]any{
		"deviceTypes": []string{"CCUAudio", "TEDS"},
		"esn":         strings.Repeat("2", 39),
		"deviceIds":   map[string]string{},
	})
	if _, err := Load(root, nil); err == nil {
		t.Fatal("Load accepted CCUAudio combined with Server")
	}
}

func TestLoadRejectsAllZeroESN(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, map[string]any{
		"deviceTypes": []string{"CCU"},
		"esn":         strings.Repeat("0", 39),
		"deviceIds":   map[string]string{},
	})
	if _, err := Load(root, nil); err == nil {
		t.Fatal("all-zero ESN was accepted")
	}
}

func TestUpdateSnapshotPreservesIdentityAndUnknownFields(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, map[string]any{
		"deviceTypes":  []string{"CCU", "TEDS"},
		"esn":          strings.Repeat("7", 39),
		"txbzName":     "old.zip",
		"txbzHash":     strings.Repeat("b", 64),
		"nodeId":       "old-node",
		"deviceIds":    map[string]string{"CCU": "old-ccu", "TEDS": "old-server"},
		"operatorNote": "preserve me",
	})

	cfg, err := Load(root, bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	err = cfg.UpdateSnapshot(Snapshot{
		TxbzName: "txbz_new.zip",
		TxbzHash: strings.Repeat("a", 64),
		NodeID:   "nn_vehicle_1",
		DeviceIDs: map[string]string{
			"CCU":  "dc_ccu_1",
			"TEDS": "dc_teds_1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(root, FileName))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["operatorNote"] != "preserve me" {
		t.Fatalf("unknown field lost: %v", got)
	}
	if got["txbzName"] != "txbz_new.zip" || got["nodeId"] != "nn_vehicle_1" {
		t.Fatalf("snapshot not updated: %v", got)
	}
	if got["esn"] != strings.Repeat("7", 39) {
		t.Fatalf("identity changed: %v", got["esn"])
	}
}

func TestLoadAcceptsVehInter(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, map[string]any{
		"deviceTypes": []string{"VehInter"},
		"esn":         strings.Repeat("3", 39),
		"deviceIds":   map[string]string{"VehInter": ""},
	})

	cfg, err := Load(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.DeviceTypes) != 1 || cfg.DeviceTypes[0] != cpdv1.DeviceType_DEVICE_TYPE_VEH_INTER {
		t.Fatalf("device types = %v, want VehInter(9)", cfg.DeviceTypes)
	}
	if got := DeviceTypeName(cfg.DeviceTypes[0]); got != "VehInter" {
		t.Fatalf("device type name = %q", got)
	}
}

func TestLoadPathReadsSpecifiedFile(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, "cpdc_config.CCU.json")
	if err := os.WriteFile(configFile, []byte(`{"deviceTypes":["CCU"],"esn":"`+strings.Repeat("1", 39)+`","deviceIds":{"CCU":""}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadPath(configFile, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.DeviceTypes) != 1 || cfg.DeviceTypes[0] != cpdv1.DeviceType_DEVICE_TYPE_CCU {
		t.Fatalf("device types = %v", cfg.DeviceTypes)
	}
}

func TestLoadPathReadsDirectoryAndPersistsESNThere(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(configDir, FileName)
	if err := os.WriteFile(configFile, []byte(`{"deviceTypes":["CCU"],"esn":"","deviceIds":{"CCU":""}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadPath(configDir, root, bytes.NewReader(bytes.Repeat([]byte{1}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ESN == "" || strings.Trim(cfg.ESN, "0") == "" {
		t.Fatal("expected generated ESN")
	}
	reloaded, err := LoadPath(configDir, root, bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ESN != cfg.ESN {
		t.Fatalf("ESN not persisted to specified path: %q vs %q", reloaded.ESN, cfg.ESN)
	}
}

func TestLoadParsesReReadjsonPath(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, map[string]any{
		"deviceTypes":    []string{"CCU"},
		"esn":            strings.Repeat("4", 39),
		"deviceIds":      map[string]string{"CCU": ""},
		"ReReadjsonPath": "/tmp/ReReadJson.ini",
	})

	cfg, err := Load(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReReadjsonPath != "/tmp/ReReadJson.ini" {
		t.Fatalf("ReReadjsonPath = %q", cfg.ReReadjsonPath)
	}
}

func TestLoadParsesTarPath(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, map[string]any{
		"deviceTypes": []string{"VehInter"},
		"esn":         strings.Repeat("5", 39),
		"deviceIds":   map[string]string{"VehInter": ""},
		"tarPath":     "/data/local/user",
	})

	cfg, err := Load(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TarPath != "/data/local/user" {
		t.Fatalf("TarPath = %q", cfg.TarPath)
	}
}

func TestLoadParsesCachePath(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, map[string]any{
		"deviceTypes": []string{"VehInter"},
		"esn":         strings.Repeat("6", 39),
		"deviceIds":   map[string]string{"VehInter": ""},
		"cachePath":   "/data/local/tmp/cpdc",
	})

	cfg, err := Load(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CachePath != "/data/local/tmp/cpdc" {
		t.Fatalf("CachePath = %q", cfg.CachePath)
	}
}

func writeConfig(t *testing.T, root string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
