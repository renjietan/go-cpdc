package selfupdate

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	receiptSendInterval = 0
	// 自更新测试默认覆盖嵌入式 Linux 流程（安装文件为无后缀 CPDC-* 二进制）；
	// Windows 规则由 TestFindInstallFileWindows / TestIsInstallFileName 单独验证。
	platformIsWindows = false
	os.Exit(m.Run())
}

func TestReadReceiptMarker(t *testing.T) {
	runDir := t.TempDir()
	writeFile(t, filepath.Join(runDir, receiptMarkerName), `{"version":"1.2.3.4","ip":"10.0.0.1","cpds_ip":"10.0.0.20"}`)

	if version, ip, cpdsIP, ok := readReceiptMarker(runDir); !ok || version != "1.2.3.4" || ip != "10.0.0.1" || cpdsIP != "10.0.0.20" {
		t.Fatalf("read = %q, %q, %q, %v", version, ip, cpdsIP, ok)
	}
	if _, _, _, ok := readReceiptMarker(t.TempDir()); ok {
		t.Fatal("missing marker reported present")
	}
	writeFile(t, filepath.Join(runDir, receiptMarkerName), `{`)
	if _, _, _, ok := readReceiptMarker(runDir); ok {
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
	destination := &net.UDPAddr{IP: net.IPv4(10, 0, 0, 20), Port: serverPort}
	t.Run("ok", func(t *testing.T) {
		conn := &fakeConn{reads: make(chan fakeRead, 1)}
		if err := sendUpdateReceipt(conn, destination, testDevice, "1.2.3.4", nil); err != nil {
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
		if err := sendUpdateReceipt(conn, destination, testDevice, "", errVersionMismatch); err != nil {
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
			writeFile(t, filepath.Join(runDir, receiptMarkerName), `{"version":"`+tt.markerVersion+`","cpds_ip":"10.0.0.20"}`)
			if tt.configJSON != "" {
				writeFile(t, filepath.Join(runDir, configFileName), tt.configJSON)
			}

			conn := &fakeConn{reads: make(chan fakeRead, 1)}
			s := newSession(testDevice, conn)
			oldResolver := networkResolver
			networkResolver = func(*net.UDPAddr, net.IP) (string, bool, error) { return "10.0.0.1", true, nil }
			defer func() { networkResolver = oldResolver }()
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

	t.Run("fallback without cpds ip", func(t *testing.T) {
		runDir := t.TempDir()
		writeFile(t, filepath.Join(runDir, receiptMarkerName), `{"version":"1.2.3.4"}`)

		conn := &fakeConn{reads: make(chan fakeRead, 1)}
		s := newSession(testDevice, conn)
		// cpds_ip 为空且 ip 为空：广播兜底因业务 IP 非法而放弃发送，但仍删除 marker。
		if !s.SendStartupReceipt(runDir, "") {
			t.Fatal("SendStartupReceipt reported no receipt")
		}
		if len(conn.snapshotWrites()) != 0 {
			t.Fatal("sent receipt despite invalid business IP")
		}
		if _, err := os.Stat(filepath.Join(runDir, receiptMarkerName)); !os.IsNotExist(err) {
			t.Fatalf("marker not deleted: %v", err)
		}
	})

	t.Run("broadcast when cross subnet", func(t *testing.T) {
		runDir := t.TempDir()
		writeFile(t, filepath.Join(runDir, receiptMarkerName), `{"version":"1.2.3.4","ip":"192.168.2.104","cpds_ip":"10.32.3.4"}`)
		writeFile(t, filepath.Join(runDir, configFileName), `{"version":"1.2.3.4"}`)

		conn := &fakeConn{reads: make(chan fakeRead, 1)}
		s := newSession(testDevice, conn)
		oldResolver := networkResolver
		networkResolver = func(*net.UDPAddr, net.IP) (string, bool, error) { return "192.168.2.104", false, nil }
		defer func() { networkResolver = oldResolver }()
		oldOpen := openBroadcastSocket
		openBroadcastSocket = func(net.IP) (datagramConn, error) { return conn, nil }
		defer func() { openBroadcastSocket = oldOpen }()

		if !s.SendStartupReceipt(runDir, "") {
			t.Fatal("SendStartupReceipt reported no receipt")
		}
		writes := conn.snapshotWrites()
		if len(writes) != 3 || !bytes.HasPrefix(writes[0].data, []byte("update_ok:")) {
			t.Fatalf("writes = %v", writes)
		}
		if writes[0].addr.Port != serverPort || !writes[0].addr.IP.Equal(net.IPv4bcast) {
			t.Fatalf("destination = %v, want broadcast", writes[0].addr)
		}
		if _, err := os.Stat(filepath.Join(runDir, receiptMarkerName)); !os.IsNotExist(err) {
			t.Fatalf("marker not deleted: %v", err)
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
