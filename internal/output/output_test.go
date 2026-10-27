package output

import (
	"archive/tar"
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	cpdv1 "cpdc/gen/cpd/v1"
	"cpdc/internal/packagezip"
)

func TestProcessMultiBandRadioOutputs(t *testing.T) {
	root := t.TempDir()
	pkg, packagePath := openTestPackage(t, radioEntries("MMR200", "dc_MMR200_1", true))
	defer pkg.Close()
	results, code := Process(context.Background(), Request{
		Root: root, Package: pkg, PackageName: filepath.Base(packagePath), NodeID: "nn_1",
		DeviceTypes: []cpdv1.DeviceType{cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_RADIO},
		Bindings:    map[cpdv1.DeviceType]string{cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_RADIO: "dc_MMR200_1"},
	})
	if code != cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED || len(results) != 1 || results[0].Result != cpdv1.Result_RESULT_SUCCESS {
		t.Fatalf("results = %v, code = %v", results, code)
	}
	planNames := readTarNames(t, filepath.Join(root, "plan_local.tar"))
	wantPlan := []string{
		"1_resource/", "1_resource/key.json", "1_resource/optional_resource.bin",
		"2_radio_subnet/", "2_radio_subnet/rs_1.json",
		"3_device_config/", "3_device_config/dc_MMR200_1.json",
	}
	if strings.Join(planNames, "|") != strings.Join(wantPlan, "|") {
		t.Fatalf("plan files = %v, want %v", planNames, wantPlan)
	}
	missionNames := readTarNames(t, filepath.Join(root, "mission.tar.gz"))
	if !containsAll(missionNames, "1_resource/key.json", "2_radio_subnet/rs_1.json", "3_device_config/dc_MMR200_1.json", "3_device_config/dc_PMR200_other.json") {
		t.Fatalf("mission files = %v", missionNames)
	}
	ini, err := os.ReadFile(filepath.Join(root, "ReReadJson.ini"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(ini)
	for _, expected := range []string{"[ReReadVal]", "Val=1", "[MissionVersion]", "Val=3", "[PlanLocalUser]", "Val=A car"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("INI %q does not contain %q", text, expected)
		}
	}
}

func TestProcessHFOnlyUpdatesRequiredINIKey(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ReReadJson.ini"), []byte("[Other]\nKeep=yes\n\n[ReReadVal]\nVal=0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pkg, packagePath := openTestPackage(t, radioEntries("MR9360", "dc_MR9360_1", false))
	defer pkg.Close()
	_, code := Process(context.Background(), Request{
		Root: root, Package: pkg, PackageName: filepath.Base(packagePath), NodeID: "nn_1",
		DeviceTypes: []cpdv1.DeviceType{cpdv1.DeviceType_DEVICE_TYPE_HF},
		Bindings:    map[cpdv1.DeviceType]string{cpdv1.DeviceType_DEVICE_TYPE_HF: "dc_MR9360_1"},
	})
	if code != cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED {
		t.Fatalf("code = %v", code)
	}
	if _, err := os.Stat(filepath.Join(root, "mission.tar.gz")); !os.IsNotExist(err) {
		t.Fatalf("HF generated mission: %v", err)
	}
	ini, err := os.ReadFile(filepath.Join(root, "ReReadJson.ini"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(ini)
	if !strings.Contains(text, "Keep=yes") || !strings.Contains(text, "[ReReadVal]\nVal=1") {
		t.Fatalf("INI = %q", text)
	}
}

func TestProcessCCUPublishesToUpdateAndRecursivelyClearsTxbzCCU(t *testing.T) {
	root := filepath.Join(t.TempDir(), "install", "cpdc")
	target := filepath.Join(root, "txbz_ccu")
	if err := os.MkdirAll(filepath.Join(target, "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "old.json"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep", "nested"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	update := filepath.Join(filepath.Dir(filepath.Dir(root)), "update")
	oldUpdateDir := CCUAbsoluteUpdateDir
	CCUAbsoluteUpdateDir = update
	defer func() { CCUAbsoluteUpdateDir = oldUpdateDir }()
	if err := os.MkdirAll(filepath.Join(update, "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(update, "dc_ccu_old.json"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(update, "keep.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(update, "keep", "dc_ccu_nested.json"), []byte("nested"), 0o600); err != nil {
		t.Fatal(err)
	}
	pkg, packagePath := openTestPackage(t, baseEntries(true))
	defer pkg.Close()
	_, code := Process(context.Background(), Request{
		Root: root, Package: pkg, PackageName: filepath.Base(packagePath), NodeID: "nn_1",
		DeviceTypes: []cpdv1.DeviceType{cpdv1.DeviceType_DEVICE_TYPE_CCU},
		Bindings:    map[cpdv1.DeviceType]string{cpdv1.DeviceType_DEVICE_TYPE_CCU: "dc_ccu_1"},
	})
	if code != cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED {
		t.Fatalf("code = %v", code)
	}
	if _, err := os.Stat(filepath.Join(target, "old.json")); !os.IsNotExist(err) {
		t.Fatalf("old file exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "keep")); !os.IsNotExist(err) {
		t.Fatalf("nested txbz_ccu directory still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(update, "dc_ccu_old.json")); !os.IsNotExist(err) {
		t.Fatalf("old update CCU file exists: %v", err)
	}
	for _, path := range []string{filepath.Join(update, "keep.txt"), filepath.Join(update, "keep", "dc_ccu_nested.json")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("unrelated update path removed: %s: %v", path, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(update, "dc_ccu_1.json"))
	if err != nil || !strings.Contains(string(data), `"Guid":"ccu"`) {
		t.Fatalf("CCU update output = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "update")); !os.IsNotExist(err) {
		t.Fatalf("CCU wrote to the CCUAudio update directory: %v", err)
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("txbz_ccu entries = %v, %v", entries, err)
	}
}

func TestProcessCCUAudioPublishesCCUConfigurationToUpdate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "install", "cpdc")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg, packagePath := openTestPackage(t, baseEntries(true))
	defer pkg.Close()
	deviceType := cpdv1.DeviceType_DEVICE_TYPE_CCU_AUDIO
	_, code := Process(context.Background(), Request{
		Root: root, Package: pkg, PackageName: filepath.Base(packagePath), NodeID: "nn_1",
		DeviceTypes: []cpdv1.DeviceType{deviceType},
		Bindings:    map[cpdv1.DeviceType]string{deviceType: "dc_ccu_1"},
	})
	if code != cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED {
		t.Fatalf("code = %v", code)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "update", "dc_ccu_1.json")); err != nil {
		t.Fatalf("CCUAudio update output missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(root)), "update")); !os.IsNotExist(err) {
		t.Fatalf("CCUAudio wrote to the CCU update directory: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "txbz_ccu"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("txbz_ccu entries = %v, %v", entries, err)
	}
}

func TestProcessServerWritesRootLocalNodeAndOriginalName(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cpdc")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	txbzData := filepath.Join(filepath.Dir(root), "txbz_data")
	if err := os.MkdirAll(filepath.Join(txbzData, "stale", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txbzData, "stale", "nested", "old"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	pkg, _ := openTestPackage(t, baseEntries(true))
	defer pkg.Close()
	_, code := Process(context.Background(), Request{
		Root: root, Package: pkg, PackageName: "txbz_original.zip", NodeID: "nn_1",
		DeviceTypes: []cpdv1.DeviceType{cpdv1.DeviceType_DEVICE_TYPE_SERVER},
		Bindings:    map[cpdv1.DeviceType]string{cpdv1.DeviceType_DEVICE_TYPE_SERVER: "dc_teds_1"},
	})
	if code != cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED {
		t.Fatalf("code = %v", code)
	}
	outputPath := filepath.Join(root, "txbz_server", "txbz_original.zip")
	reader, err := zip.OpenReader(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
		if file.Name == "local_node.json" {
			stream, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(stream)
			_ = stream.Close()
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]string
			if err := json.Unmarshal(data, &value); err != nil || value["LocalNode"] != "nn_1" {
				t.Fatalf("local_node = %q, %v", data, err)
			}
		}
	}
	if !containsAll(names, "0_contacts/", "0_contacts/contacts_1.json", "local_node.json") {
		t.Fatalf("ZIP files = %v", names)
	}
	for _, name := range names {
		if strings.HasPrefix(name, "txbz_original/") {
			t.Fatalf("unexpected wrapper directory: %s", name)
		}
	}
	assertExtractedPrimaryOutput(t, txbzData, "txbz_original", "nn_1")
}

func TestProcessIECAlsoExtractsOutputToTxbzData(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cpdc")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg, _ := openTestPackage(t, primaryEntries("dc_IEC_1"))
	defer pkg.Close()
	_, code := Process(context.Background(), Request{
		Root: root, Package: pkg, PackageName: "txbz_original.zip", NodeID: "nn_1",
		DeviceTypes: []cpdv1.DeviceType{cpdv1.DeviceType_DEVICE_TYPE_IEC},
		Bindings:    map[cpdv1.DeviceType]string{cpdv1.DeviceType_DEVICE_TYPE_IEC: "dc_IEC_1"},
	})
	if code != cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED {
		t.Fatalf("code = %v", code)
	}
	if _, err := os.Stat(filepath.Join(root, "txbz_server", "txbz_original.zip")); err != nil {
		t.Fatalf("IEC ZIP output missing: %v", err)
	}
	assertExtractedPrimaryOutput(t, filepath.Join(filepath.Dir(root), "txbz_data"), "txbz_original", "nn_1")
}

func TestProcessMarksRemainingTypesSkipped(t *testing.T) {
	root := t.TempDir()
	pkg, packagePath := openTestPackage(t, baseEntries(true))
	defer pkg.Close()
	results, code := Process(context.Background(), Request{
		Root: root, Package: pkg, PackageName: filepath.Base(packagePath), NodeID: "nn_1",
		DeviceTypes: []cpdv1.DeviceType{cpdv1.DeviceType_DEVICE_TYPE_CCU, cpdv1.DeviceType_DEVICE_TYPE_SERVER},
		Bindings: map[cpdv1.DeviceType]string{
			cpdv1.DeviceType_DEVICE_TYPE_CCU:    "missing",
			cpdv1.DeviceType_DEVICE_TYPE_SERVER: "dc_teds_1",
		},
	})
	if code != cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE || len(results) != 2 {
		t.Fatalf("results = %v, code = %v", results, code)
	}
	if results[1].Stage != cpdv1.ParseStage_PARSE_STAGE_SKIPPED || results[1].ErrorCode != cpdv1.ErrorCode_ERROR_CODE_SKIPPED_AFTER_PREVIOUS_FAILURE {
		t.Fatalf("second result = %v", results[1])
	}
}

func TestProcessVehInterWritesTarAndUpdatesReReadINI(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ReReadJson.ini"), []byte("[Other]\nKeep=yes\n\n[ReReadVal]\nVal=0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pkg, packagePath := openTestPackage(t, vehInterEntries())
	defer pkg.Close()

	results, code := Process(context.Background(), Request{
		Root: root, Package: pkg, PackageName: filepath.Base(packagePath), NodeID: "nn_1",
		DeviceTypes: []cpdv1.DeviceType{cpdv1.DeviceType_DEVICE_TYPE_VEH_INTER},
		Bindings:    map[cpdv1.DeviceType]string{cpdv1.DeviceType_DEVICE_TYPE_VEH_INTER: "dc_VehInter_1"},
	})
	if code != cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED || len(results) != 1 || results[0].Result != cpdv1.Result_RESULT_SUCCESS {
		t.Fatalf("results = %v, code = %v", results, code)
	}

	names := readTarNames(t, filepath.Join(root, "vehinte.tar"))
	want := []string{
		"0_contacts/", "0_contacts/contacts_1.json",
		"3_device_config/", "3_device_config/dc_VehInter_1.json",
		"4_net_node/", "4_net_node/nn_1.json",
		"6_unit/", "6_unit/unit_1.json",
	}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("tar files = %v, want %v", names, want)
	}

	ini, err := os.ReadFile(filepath.Join(root, "ReReadJson.ini"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(ini)
	if !strings.Contains(text, "Keep=yes") || !strings.Contains(text, "[ReReadVal]\nVal=1") {
		t.Fatalf("INI = %q", text)
	}
}

func TestProcessVehInterUsesConfiguredReReadJSONPath(t *testing.T) {
	root := t.TempDir()
	configured := filepath.Join(t.TempDir(), "custom", "ReReadJson.ini")
	pkg, packagePath := openTestPackage(t, vehInterEntries())
	defer pkg.Close()

	results, code := Process(context.Background(), Request{
		Root: root, Package: pkg, PackageName: filepath.Base(packagePath), NodeID: "nn_1",
		DeviceTypes:    []cpdv1.DeviceType{cpdv1.DeviceType_DEVICE_TYPE_VEH_INTER},
		Bindings:       map[cpdv1.DeviceType]string{cpdv1.DeviceType_DEVICE_TYPE_VEH_INTER: "dc_VehInter_1"},
		ReReadjsonPath: configured,
	})
	if code != cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED || len(results) != 1 || results[0].Result != cpdv1.Result_RESULT_SUCCESS {
		t.Fatalf("results = %v, code = %v", results, code)
	}
	if _, err := os.Stat(configured); err != nil {
		t.Fatalf("configured ReReadJson.ini missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "ReReadJson.ini")); !os.IsNotExist(err) {
		t.Fatalf("default ReReadJson.ini should not exist: %v", err)
	}
	data, err := os.ReadFile(configured)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[ReReadVal]\nVal=1") {
		t.Fatalf("INI = %q", data)
	}
}

func TestProcessVehInterUsesConfiguredTarPath(t *testing.T) {
	root := t.TempDir()
	tarDir := filepath.Join(t.TempDir(), "custom_tar")
	pkg, packagePath := openTestPackage(t, vehInterEntries())
	defer pkg.Close()

	results, code := Process(context.Background(), Request{
		Root: root, Package: pkg, PackageName: filepath.Base(packagePath), NodeID: "nn_1",
		DeviceTypes: []cpdv1.DeviceType{cpdv1.DeviceType_DEVICE_TYPE_VEH_INTER},
		Bindings:    map[cpdv1.DeviceType]string{cpdv1.DeviceType_DEVICE_TYPE_VEH_INTER: "dc_VehInter_1"},
		TarPath:     tarDir,
	})
	if code != cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED || len(results) != 1 || results[0].Result != cpdv1.Result_RESULT_SUCCESS {
		t.Fatalf("results = %v, code = %v", results, code)
	}
	if _, err := os.Stat(filepath.Join(tarDir, "vehinte.tar")); err != nil {
		t.Fatalf("configured vehinte.tar missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "vehinte.tar")); !os.IsNotExist(err) {
		t.Fatalf("default vehinte.tar should not exist: %v", err)
	}
}

func vehInterEntries() map[string][]byte {
	return map[string][]byte{
		"0_contacts/contacts_1.json":         []byte(`{"File":{"Guid":"c"},"UnitTree":{"SubUnits":["unit_1"],"NetNodes":["nn_1"]}}`),
		"3_device_config/dc_VehInter_1.json": []byte(`{"Model":"VehInter"}`),
		"4_net_node/nn_1.json":               []byte(`{"File":{"Guid":"n"},"SystemConfiguration":{"LANMember":{"VehInter":["dc_VehInter_1"]},"LANPrimary":{},"Radio":{}}}`),
		"6_unit/unit_1.json":                 []byte(`{"File":{"Guid":"u"},"UnitName":"Unit"}`),
	}
}

func openTestPackage(t *testing.T, entries map[string][]byte) (*packagezip.Package, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "txbz_source.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		stream, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Write(entries[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	pkg, err := packagezip.Validate(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return pkg, path
}

func baseEntries(includeServer bool) map[string][]byte {
	server := ""
	entries := map[string][]byte{
		"0_contacts/contacts_1.json":       []byte(`{"File":{"Guid":"c"},"UnitTree":{"SubUnits":["unit_1"],"NetNodes":["nn_1"]}}`),
		"3_device_config/dc_ccu_1.json":    []byte(`{"File":{"Guid":"ccu"},"Model":"CCU"}`),
		"6_unit/unit_1.json":               []byte(`{"File":{"Guid":"u"},"UnitName":"Unit"}`),
		"1_resource/optional_resource.bin": []byte("resource"),
	}
	if includeServer {
		server = `"TEDS":["dc_teds_1"]`
		entries["3_device_config/dc_teds_1.json"] = []byte(`{"File":{"Guid":"server"},"Model":"TEDS"}`)
	}
	entries["4_net_node/nn_1.json"] = []byte(`{"File":{"Guid":"n"},"SystemConfiguration":{"LANMember":{"CCU":["dc_ccu_1"]},"LANPrimary":{` + server + `},"Radio":{}}}`)
	return entries
}

func primaryEntries(deviceID string) map[string][]byte {
	entries := baseEntries(false)
	entries["3_device_config/"+deviceID+".json"] = []byte(`{"File":{"Guid":"primary"},"Model":"Primary"}`)
	entries["4_net_node/nn_1.json"] = []byte(`{"File":{"Guid":"n"},"SystemConfiguration":{"LANMember":{"CCU":["dc_ccu_1"]},"LANPrimary":{"TEDS":["` + deviceID + `"]},"Radio":{}}}`)
	return entries
}

func assertExtractedPrimaryOutput(t *testing.T, txbzData, packageDirectory, nodeID string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(txbzData, "stale")); !os.IsNotExist(err) {
		t.Fatalf("stale txbz_data content exists: %v", err)
	}
	for _, name := range []string{"0_contacts", "local_node.json"} {
		if _, err := os.Stat(filepath.Join(txbzData, name)); !os.IsNotExist(err) {
			t.Fatalf("business content was written directly below txbz_data: %s: %v", name, err)
		}
	}
	target := filepath.Join(txbzData, packageDirectory)
	if _, err := os.Stat(filepath.Join(target, "0_contacts", "contacts_1.json")); err != nil {
		t.Fatalf("business content missing from txbz_data: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, "local_node.json"))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]string
	if err := json.Unmarshal(data, &value); err != nil || value["LocalNode"] != nodeID {
		t.Fatalf("extracted local_node = %q, %v", data, err)
	}
}

func radioEntries(model, deviceID string, alias bool) map[string][]byte {
	entries := baseEntries(false)
	aliasField := ""
	if alias {
		aliasField = `,"Alias":"A car"`
	}
	entries["1_resource/key.json"] = []byte(`{"Key":"value"}`)
	entries["2_radio_subnet/rs_1.json"] = []byte(`{"File":{"Guid":"rs"}}`)
	entries["3_device_config/"+deviceID+".json"] = []byte(`{"File":{"Guid":"radio"}` + aliasField + `,"Channels":{"01":{"Subnet":"rs_1"}}}`)
	entries["3_device_config/dc_PMR200_other.json"] = []byte(`{"File":{"Guid":"other"},"Alias":"B","Channels":{"01":{"Subnet":"rs_1"}}}`)
	entries["4_net_node/nn_1.json"] = []byte(`{"File":{"Guid":"n"},"SystemConfiguration":{"LANMember":{},"LANPrimary":{},"Radio":{"` + model + `":["` + deviceID + `"]}}}`)
	return entries
}

func readTarNames(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader := tar.NewReader(file)
	var names []string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
	return names
}

func containsAll(values []string, wanted ...string) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range wanted {
		if !seen[value] {
			return false
		}
	}
	return true
}
