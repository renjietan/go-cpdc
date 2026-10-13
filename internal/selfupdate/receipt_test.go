package selfupdate

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	receiptSendInterval = 0
	os.Exit(m.Run())
}

func TestReadReceiptMarker(t *testing.T) {
	runDir := t.TempDir()
	writeFile(t, filepath.Join(runDir, receiptMarkerName), `{"version":"1.2.3.4","ip":"10.0.0.1"}`)

	if version, ip, ok := readReceiptMarker(runDir); !ok || version != "1.2.3.4" || ip != "10.0.0.1" {
		t.Fatalf("read = %q, %q, %v", version, ip, ok)
	}
	if _, _, ok := readReceiptMarker(t.TempDir()); ok {
		t.Fatal("missing marker reported present")
	}
	writeFile(t, filepath.Join(runDir, receiptMarkerName), `{`)
	if _, _, ok := readReceiptMarker(runDir); ok {
		t.Fatal("invalid marker reported present")
	}
}

func TestReceiptError(t *testing.T) {
	if err := receiptError("1.0.0.0", "1.0.0.0", nil); err != nil {
		t.Fatalf("equal versions = %v", err)
	}
	if err := receiptError("1.0.0.0", "0.9.9.9", nil); !errors.Is(err, errVersionMismatch) {
		t.Fatalf("mismatch = %v", err)
	}
	if err := receiptError("1.0.0.0", "", errors.New("read failed")); !errors.Is(err, errVersionReadError) {
		t.Fatalf("read error = %v", err)
	}
}

func TestSendUpdateReceipt(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		conn := &fakeConn{reads: make(chan fakeRead, 1)}
		if err := sendUpdateReceipt(conn, testDevice, "1.2.3.4", nil); err != nil {
			t.Fatal(err)
		}
		writes := conn.snapshotWrites()
		if len(writes) != 3 || !bytes.HasPrefix(writes[0].data, []byte("update_ok:")) {
			t.Fatalf("writes = %v", writes)
		}
		if !bytes.HasSuffix(writes[0].data, []byte("1.2.3.4")) {
			t.Fatalf("version = %q", writes[0].data)
		}
	})

	t.Run("fail", func(t *testing.T) {
		conn := &fakeConn{reads: make(chan fakeRead, 1)}
		if err := sendUpdateReceipt(conn, testDevice, "", errVersionMismatch); err != nil {
			t.Fatal(err)
		}
		writes := conn.snapshotWrites()
		if len(writes) != 3 || !bytes.HasPrefix(writes[0].data, []byte("update_fail:")) {
			t.Fatalf("writes = %v", writes)
		}
		if !bytes.HasSuffix(writes[0].data, []byte("version_mismatch")) {
			t.Fatalf("reason = %q", writes[0].data)
		}
	})
}

func TestSendStartupReceipt(t *testing.T) {
	tests := []struct {
		name          string
		markerVersion string
		configJSON    string
		wantPrefix    string
		wantSuffix    string
	}{
		{name: "ok", markerVersion: "1.2.3.4", configJSON: `{"version":"1.2.3.4"}`, wantPrefix: "update_ok:", wantSuffix: "1.2.3.4"},
		{name: "mismatch", markerVersion: "1.2.3.4", configJSON: `{"version":"0.9.9.9"}`, wantPrefix: "update_fail:", wantSuffix: "version_mismatch"},
		{name: "read error", markerVersion: "1.2.3.4", configJSON: "", wantPrefix: "update_fail:", wantSuffix: "version_read_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runDir := t.TempDir()
			writeFile(t, filepath.Join(runDir, receiptMarkerName), `{"version":"`+tt.markerVersion+`"}`)
			if tt.configJSON != "" {
				writeFile(t, filepath.Join(runDir, configFileName), tt.configJSON)
			}

			conn := &fakeConn{reads: make(chan fakeRead, 1)}
			s := newSession(testDevice, conn)
			if !s.SendStartupReceipt(runDir, "") {
				t.Fatal("SendStartupReceipt reported no receipt")
			}

			writes := conn.snapshotWrites()
			if len(writes) != 3 || !bytes.HasPrefix(writes[0].data, []byte(tt.wantPrefix)) {
				t.Fatalf("writes = %v", writes)
			}
			if !bytes.HasSuffix(writes[0].data, []byte(tt.wantSuffix)) {
				t.Fatalf("suffix = %q", writes[0].data)
			}
			if _, err := os.Stat(filepath.Join(runDir, receiptMarkerName)); !os.IsNotExist(err) {
				t.Fatalf("marker not deleted: %v", err)
			}
		})
	}

	t.Run("no marker", func(t *testing.T) {
		conn := &fakeConn{reads: make(chan fakeRead, 1)}
		s := newSession(testDevice, conn)
		if s.SendStartupReceipt(t.TempDir(), "") {
			t.Fatal("reported receipt without marker")
		}
		if len(conn.snapshotWrites()) != 0 {
			t.Fatal("sent receipt without marker")
		}
	})
}

func TestRecoverFailedFinalizeRestoresOldVersion(t *testing.T) {
	runDir := t.TempDir()
	cacheDir := t.TempDir()
	writeFile(t, filepath.Join(runDir, configFileName), `{"version":"2.0.0.0","esn":"123"}`)
	writeFile(t, filepath.Join(cacheDir, finalizeFailName), "boom")
	writeFile(t, filepath.Join(cacheDir, finalizeParamsName), `{"old_version":"0.9.9.9","config_path":""}`)

	RecoverFailedFinalize(runDir, "", cacheDir)

	cfg := readJSON(t, filepath.Join(runDir, configFileName))
	if cfg["version"] != "0.9.9.9" {
		t.Fatalf("version = %v, want 0.9.9.9", cfg["version"])
	}
	if cfg["esn"] != "123" {
		t.Fatalf("esn lost: %v", cfg["esn"])
	}
}
