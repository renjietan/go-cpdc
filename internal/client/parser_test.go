package client

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	cpdv1 "cpdc/gen/cpd/v1"
	"cpdc/internal/output"
)

func redirectCCUUpdateDir(t *testing.T, root string) {
	t.Helper()
	old := output.CCUAbsoluteUpdateDir
	output.CCUAbsoluteUpdateDir = filepath.Join(filepath.Dir(filepath.Dir(root)), "update")
	t.Cleanup(func() { output.CCUAbsoluteUpdateDir = old })
}

func TestClientTestConfigKeepsParentOutputsOutOfSystemTempRoot(t *testing.T) {
	root, _ := testConfig(t, []string{"CCU"})
	outputParent := filepath.Clean(filepath.Dir(filepath.Dir(root)))
	if outputParent == filepath.Clean(os.TempDir()) {
		t.Fatalf("test root %q places ../../update in the system temp root", root)
	}
}

func TestParserWritesOutputsAndSuccessSnapshot(t *testing.T) {
	root, cfg := testConfig(t, []string{"CCU"})
	redirectCCUUpdateDir(t, root)
	packagePath, expanded := writeParserPackage(t)
	hash := sha256.Sum256([]byte("source identity"))
	outcome := NewParser().Parse(ParseRequest{
		Context: context.Background(), Root: root, Config: cfg, PackagePath: packagePath,
		PackageName: "txbz_source.zip", FileHash: hash[:], ExpectedExpandedSize: expanded,
		NodeID: "nn_1", Bindings: map[cpdv1.DeviceType]string{cpdv1.DeviceType_DEVICE_TYPE_CCU: "dc_ccu_1"},
	})
	if outcome.Result != cpdv1.Result_RESULT_SUCCESS || outcome.ErrorCode != cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED {
		t.Fatalf("outcome = %+v", outcome)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(root)), "update", "dc_ccu_1.json")); err != nil {
		t.Fatalf("CCU output missing: %v", err)
	}
	reloaded, err := os.ReadFile(filepath.Join(root, "cpdc_config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(reloaded, &value); err != nil {
		t.Fatal(err)
	}
	if value["txbzName"] != "txbz_source.zip" || value["nodeId"] != "nn_1" || value["txbzHash"] != hex.EncodeToString(hash[:]) {
		t.Fatalf("snapshot = %v", value)
	}
}

func TestParserSyncsAfterOutputsAndSnapshot(t *testing.T) {
	root, cfg := testConfig(t, []string{"CCU"})
	redirectCCUUpdateDir(t, root)
	packagePath, expanded := writeParserPackage(t)
	hash := sha256.Sum256([]byte("source identity"))
	parser := NewParser()
	synced := false
	parser.syncFilesystems = func() {
		synced = true
		if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(root)), "update", "dc_ccu_1.json")); err != nil {
			t.Fatalf("CCU output was not committed before sync: %v", err)
		}
		if cfg.TxbzName != "txbz_source.zip" || cfg.NodeID != "nn_1" {
			t.Fatalf("snapshot was not updated before sync: %+v", cfg)
		}
		data, err := os.ReadFile(filepath.Join(root, "cpdc_config.json"))
		if err != nil {
			t.Fatalf("read snapshot during sync: %v", err)
		}
		if !bytes.Contains(data, []byte(`"txbzName": "txbz_source.zip"`)) {
			t.Fatalf("snapshot file was not committed before sync: %s", data)
		}
	}
	outcome := parser.Parse(ParseRequest{
		Context: context.Background(), Root: root, Config: cfg, PackagePath: packagePath,
		PackageName: "txbz_source.zip", FileHash: hash[:], ExpectedExpandedSize: expanded,
		NodeID: "nn_1", Bindings: map[cpdv1.DeviceType]string{cpdv1.DeviceType_DEVICE_TYPE_CCU: "dc_ccu_1"},
	})
	if outcome.Result != cpdv1.Result_RESULT_SUCCESS {
		t.Fatalf("outcome = %+v", outcome)
	}
	if !synced {
		t.Fatal("parse success was returned without syncing filesystems")
	}
}

func TestParserRejectsExpandedSizeMismatchWithoutSnapshot(t *testing.T) {
	root, cfg := testConfig(t, []string{"CCU"})
	packagePath, expanded := writeParserPackage(t)
	hash := sha256.Sum256([]byte("source identity"))
	outcome := NewParser().Parse(ParseRequest{
		Context: context.Background(), Root: root, Config: cfg, PackagePath: packagePath,
		PackageName: "txbz_source.zip", FileHash: hash[:], ExpectedExpandedSize: expanded + 1,
		NodeID: "nn_1", Bindings: map[cpdv1.DeviceType]string{cpdv1.DeviceType_DEVICE_TYPE_CCU: "dc_ccu_1"},
	})
	if outcome.Result != cpdv1.Result_RESULT_FAILED || outcome.ErrorCode != cpdv1.ErrorCode_ERROR_CODE_INVALID_ZIP_SIZE {
		t.Fatalf("outcome = %+v", outcome)
	}
	if cfg.TxbzName != "" || cfg.NodeID != "" {
		t.Fatalf("failed parse updated snapshot: %+v", cfg)
	}
}

func TestParserRechecksStorageBeforeOpeningPackage(t *testing.T) {
	root, cfg := testConfig(t, []string{"CCU"})
	packagePath, expanded := writeParserPackage(t)
	hash := sha256.Sum256([]byte("source identity"))
	outcome := NewParser().Parse(ParseRequest{
		Context: context.Background(), Root: root, Config: cfg, PackagePath: packagePath,
		PackageName: "txbz_source.zip", FileHash: hash[:], ExpectedExpandedSize: expanded,
		RequiredWorkspace: 4096, AvailableSpace: func(string) (uint64, error) { return 4095, nil },
		NodeID: "nn_1", Bindings: map[cpdv1.DeviceType]string{cpdv1.DeviceType_DEVICE_TYPE_CCU: "dc_ccu_1"},
	})
	if outcome.Result != cpdv1.Result_RESULT_FAILED || outcome.ErrorCode != cpdv1.ErrorCode_ERROR_CODE_INSUFFICIENT_STORAGE {
		t.Fatalf("outcome = %+v", outcome)
	}
	if _, err := os.Stat(filepath.Join(root, "txbz_ccu", "dc_ccu_1.json")); !os.IsNotExist(err) {
		t.Fatalf("output was written despite space rejection: %v", err)
	}
}

func writeParserPackage(t *testing.T) (string, uint64) {
	t.Helper()
	entries := map[string][]byte{
		"0_contacts/contacts_1.json":    []byte(`{"File":{"Guid":"c"},"UnitTree":{"SubUnits":["unit_1"],"NetNodes":["nn_1"]}}`),
		"3_device_config/dc_ccu_1.json": []byte(`{"File":{"Guid":"ccu"},"Model":"CCU"}`),
		"4_net_node/nn_1.json":          []byte(`{"File":{"Guid":"n"},"SystemConfiguration":{"LANMember":{"CCU":["dc_ccu_1"]},"LANPrimary":{},"Radio":{}}}`),
		"6_unit/unit_1.json":            []byte(`{"File":{"Guid":"u"},"UnitName":"Unit"}`),
	}
	path := filepath.Join(t.TempDir(), "source.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	var expanded uint64
	for name, data := range entries {
		expanded += uint64(len(data))
		stream, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path, expanded
}
