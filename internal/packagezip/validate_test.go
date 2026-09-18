package packagezip

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cpdv1 "cpdc/gen/cpd/v1"
)

func TestValidateAcceptsMinimalPackageAndNumericGuid(t *testing.T) {
	path := writeZIP(t, validEntries())
	pkg, err := Validate(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer pkg.Close()
	if _, ok := pkg.Nodes["nn_1"]; !ok {
		t.Fatalf("nodes = %v", pkg.Nodes)
	}
	device, ok := pkg.Devices["dc_ccu_1"]
	if !ok || device.Type != cpdv1.DeviceType_DEVICE_TYPE_CCU {
		t.Fatalf("device = %+v, %v", device, ok)
	}
}

func TestValidateRejectsUnsafeZIPEntries(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string][]byte)
	}{
		{name: "path traversal", mutate: func(entries map[string][]byte) { entries["../escape.json"] = []byte(`{}`) }},
		{name: "backslash", mutate: func(entries map[string][]byte) { entries[`3_device_config\evil.json`] = []byte(`{}`) }},
		{name: "windows reserved", mutate: func(entries map[string][]byte) { entries["1_resource/CON"] = []byte("x") }},
		{name: "trailing dot", mutate: func(entries map[string][]byte) { entries["1_resource/key."] = []byte("x") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := validEntries()
			tt.mutate(entries)
			_, err := Validate(context.Background(), writeZIP(t, entries))
			assertPackageError(t, err, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE)
		})
	}
}

func TestValidateRejectsCaseFoldDuplicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duplicate.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, name := range []string{"0_contacts/A.json", "0_contacts/a.json"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Validate(context.Background(), path)
	assertPackageError(t, err, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE)
}

func TestValidateRejectsSpecialEntryAndOversizedFile(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "symlink.zip")
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		writer := zip.NewWriter(file)
		header := &zip.FileHeader{Name: "link"}
		header.SetMode(os.ModeSymlink | 0o777)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = entry.Write([]byte("target"))
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		_, err = Validate(context.Background(), path)
		assertPackageError(t, err, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE)
	})

	t.Run("single expanded file", func(t *testing.T) {
		entries := validEntries()
		entries["1_resource/large.bin"] = bytes.Repeat([]byte{0}, MaxEntrySize+1)
		_, err := Validate(context.Background(), writeZIP(t, entries))
		assertPackageError(t, err, cpdv1.ErrorCode_ERROR_CODE_INVALID_ZIP_SIZE)
	})
}

func TestValidateRejectsMissingBusinessReference(t *testing.T) {
	entries := validEntries()
	entries["4_net_node/nn_1.json"] = []byte(`{
      "File":{"Guid":"1"},
      "SystemConfiguration":{"LANMember":{"CCU":["dc_ccu_missing"]},"LANPrimary":{},"Radio":{}}
    }`)
	pkg, err := Validate(context.Background(), writeZIP(t, entries))
	if pkg != nil {
		_ = pkg.Close()
	}
	assertPackageError(t, err, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE)
}

func TestValidateRadioDependenciesAndDeduplicatesSubnets(t *testing.T) {
	entries := validEntries()
	entries["1_resource/key.json"] = []byte(`{"Key":"redacted"}`)
	entries["2_radio_subnet/rs_1.json"] = []byte(`{"File":{"Guid":"subnet"}}`)
	entries["3_device_config/dc_MMR200_1.json"] = []byte(`{
      "File":{"Guid":"radio"},"Alias":"A car",
      "Channels":{"01":{"Subnet":"rs_1"},"02":{"Subnet":"rs_1"}}
    }`)
	entries["4_net_node/nn_1.json"] = []byte(`{
      "File":{"Guid":1},
      "SystemConfiguration":{
        "LANMember":{"CCU":["dc_ccu_1"]},"LANPrimary":{},
        "Radio":{"MMR200":["dc_MMR200_1"]}
      }
    }`)
	pkg, err := Validate(context.Background(), writeZIP(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	defer pkg.Close()
	device := pkg.Devices["dc_MMR200_1"]
	if len(device.Subnets) != 1 || device.Subnets[0] != "rs_1" || device.Alias != "A car" {
		t.Fatalf("radio = %+v", device)
	}
}

func TestValidateRejectsRadioMissingSubnet(t *testing.T) {
	entries := validEntries()
	entries["1_resource/key.json"] = []byte(`{}`)
	entries["2_radio_subnet/placeholder.json"] = []byte(`{}`)
	entries["3_device_config/dc_MMR200_1.json"] = []byte(`{"File":{"Guid":"r"},"Alias":"A","Channels":{"01":{"Subnet":"missing"}}}`)
	entries["4_net_node/nn_1.json"] = []byte(`{
      "File":{"Guid":1},
      "SystemConfiguration":{"LANMember":{},"LANPrimary":{},"Radio":{"MMR200":["dc_MMR200_1"]}}
    }`)
	pkg, err := Validate(context.Background(), writeZIP(t, entries))
	if pkg != nil {
		_ = pkg.Close()
	}
	assertPackageError(t, err, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE)
}

func TestValidateRequiresRadioResourceDirectories(t *testing.T) {
	entries := validEntries()
	entries["3_device_config/dc_MMR200_1.json"] = []byte(`{"File":{"Guid":"r"},"Alias":"A","Channels":{}}`)
	entries["4_net_node/nn_1.json"] = []byte(`{
      "File":{"Guid":1},
      "SystemConfiguration":{"LANMember":{},"LANPrimary":{},"Radio":{"MMR200":["dc_MMR200_1"]}}
    }`)
	pkg, err := Validate(context.Background(), writeZIP(t, entries))
	if pkg != nil {
		_ = pkg.Close()
	}
	assertPackageError(t, err, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE)
}

func TestValidateRejectsNonJSONInIndexedBusinessDirectory(t *testing.T) {
	entries := validEntries()
	entries["3_device_config/not_json.bin"] = []byte("data")
	pkg, err := Validate(context.Background(), writeZIP(t, entries))
	if pkg != nil {
		_ = pkg.Close()
	}
	assertPackageError(t, err, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE)
}

func TestValidateChecksKnownOrphanRadioConfiguration(t *testing.T) {
	entries := validEntries()
	entries["1_resource/key.bin"] = []byte("key")
	entries["2_radio_subnet/rs_1.json"] = []byte(`{"File":{"Guid":"rs"}}`)
	entries["3_device_config/dc_MMR200_orphan.json"] = []byte(`{"File":{"Guid":"orphan"},"Alias":"A"}`)
	pkg, err := Validate(context.Background(), writeZIP(t, entries))
	if pkg != nil {
		_ = pkg.Close()
	}
	assertPackageError(t, err, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE)
}

func TestValidateAllowsUnusedJSONWithNonObjectRoot(t *testing.T) {
	entries := validEntries()
	entries["5_user/users.json"] = []byte(`["user-a","user-b"]`)
	pkg, err := Validate(context.Background(), writeZIP(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	_ = pkg.Close()
}

func TestValidateRecognizesVehInterDevice(t *testing.T) {
	entries := validEntries()
	entries["3_device_config/dc_VehInter_1.json"] = []byte(`{"Model":"VehInter"}`)
	entries["4_net_node/nn_1.json"] = []byte(`{
      "File":{"Guid":1001},
      "SystemConfiguration":{
        "LANMember":{"CCU":["dc_ccu_1"],"VehInter":["dc_VehInter_1"]},
        "LANPrimary":{"Server":[]},
        "Radio":{}
      }
    }`)

	pkg, err := Validate(context.Background(), writeZIP(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	defer pkg.Close()

	device, ok := pkg.Devices["dc_VehInter_1"]
	if !ok || device.Type != cpdv1.DeviceType_DEVICE_TYPE_VEH_INTER {
		t.Fatalf("device = %+v, %v", device, ok)
	}
}

func validEntries() map[string][]byte {
	return map[string][]byte{
		"0_contacts/contacts_1.json": []byte(`{
          "File":{"Guid":"contacts"},
          "UnitTree":{"SubUnits":["unit_1"],"NetNodes":["nn_1"]}
        }`),
		// The real package format does not add File metadata to CCU/Server
		// device configuration files.
		"3_device_config/dc_ccu_1.json": []byte(`{"Model":"CCU"}`),
		"4_net_node/nn_1.json": []byte(`{
          "File":{"Guid":1001},
          "SystemConfiguration":{
            "LANMember":{"CCU":["dc_ccu_1"]},
            "LANPrimary":{"Server":[]},
            "Radio":{}
          }
        }`),
		"6_unit/unit_1.json": []byte(`{"File":{"Guid":2001},"UnitName":"Unit"}`),
	}
}

func writeZIP(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "txbz_test.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, data := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertPackageError(t *testing.T, err error, code cpdv1.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatal("Validate succeeded, want error")
	}
	packageError, ok := AsError(err)
	if !ok || packageError.Code != code {
		t.Fatalf("error = %v, want code %v", err, code)
	}
	if strings.Contains(err.Error(), "Key") {
		t.Fatalf("error leaked input content: %v", err)
	}
}
