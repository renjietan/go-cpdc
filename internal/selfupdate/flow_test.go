package selfupdate

import (
	"bytes"
	"context"
	"hash/crc32"
	"path/filepath"
	"strings"
	"testing"
)

// TestFullUpdateFlow drives a Session through the complete downlink flow:
// Scan -> auth -> version -> file -> packets -> valid -> install -> restart.
func TestFullUpdateFlow(t *testing.T) {
	runDir := t.TempDir()
	exePath := filepath.Join(runDir, "CPDC-HF")
	writeFile(t, filepath.Join(runDir, configFileName), `{"deviceTypes":["HF"],"esn":"123456"}`)
	zipBytes := buildPackageZip(t, map[string]string{
		"MR9360/CPDC-HF":          "NEW-BINARY",
		"MR9360/cpdc_config.json": `{"version":"2.0.0.0"}`,
	})

	oldExe := executablePath
	oldSpawn := spawnProcess
	oldExit := exitNow
	oldInterval := writeOkSendInterval
	executablePath = func() (string, error) { return exePath, nil }
	spawnProcess = func(string, []string) error { return nil }
	exitNow = func(int) {}
	writeOkSendInterval = 0
	defer func() {
		executablePath = oldExe
		spawnProcess = oldSpawn
		exitNow = oldExit
		writeOkSendInterval = oldInterval
	}()

	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	info := testDevice
	info.RunCommand = []string{"/opt/cpdc/CPDC-HF", "--config-path", "/data/user"}
	s := newSession(info, conn)
	s.write = writeFiles

	mid := len(zipBytes) / 2
	crc := crc32.ChecksumIEEE(zipBytes)
	commands := [][]byte{
		[]byte("Scan"),
		[]byte("auth"),
		[]byte("version:2.0.0.0"),
		fileCommand(crc, 2, uint32(len(zipBytes)), "install.zip"),
		packetCommand(crc32.ChecksumIEEE(zipBytes[:mid]), 1, uint32(mid), zipBytes[:mid]),
		packetCommand(crc32.ChecksumIEEE(zipBytes[mid:]), 2, uint32(len(zipBytes)-mid), zipBytes[mid:]),
		validCommand(crc, 2, uint32(len(zipBytes)), "install.zip"),
		writeCommand(crc, 2, uint32(len(zipBytes)), "install.zip"),
	}
	for _, cmd := range commands {
		s.handle(testAddr, cmd)
	}

	wantPrefixes := []string{"Scan:success", "authAck:", "version_ok:", "fileAck:", "packet_ok:", "packet_ok:", "valid_ok:", "write_ok:", "write_ok:", "write_ok:"}
	writes := conn.snapshotWrites()
	if len(writes) != len(wantPrefixes) {
		t.Fatalf("writes = %d, want %d: %v", len(writes), len(wantPrefixes), writes)
	}
	for i, prefix := range wantPrefixes {
		if !bytes.HasPrefix(writes[i].data, []byte(prefix)) {
			t.Fatalf("write[%d] = %q, want prefix %q", i, writes[i].data, prefix)
		}
	}


	if got := readFile(t, filepath.Join(runDir, installDirName, "new_exe")); got != "NEW-BINARY" {
		t.Fatalf("staged binary = %q", got)
	}
	if got := readJSON(t, filepath.Join(runDir, configFileName))["version"]; got != "2.0.0.0" {
		t.Fatalf("config version = %v", got)
	}
	marker := readJSON(t, filepath.Join(runDir, receiptMarkerName))
	if marker["version"] != "2.0.0.0" || marker["ip"] != "192.168.1.10" {
		t.Fatalf("marker = %v", marker)
	}
}

// TestSessionDropsMalformedDownlinkCommands verifies malformed commands are
// discarded without any reply.
func TestSessionDropsMalformedDownlinkCommands(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "unknown", data: []byte("unknown")},
		{name: "file too short", data: []byte("file:123")},
		{name: "file empty name", data: appendUint32(appendUint32(appendUint32([]byte("file:"), 0), 1), 0)},
		{name: "file zero packets", data: append(appendUint32(appendUint32(appendUint32([]byte("file:"), 0), 0), 10), []byte("x.zip")...)},
		{name: "packet too short", data: []byte("packet:123")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := &fakeConn{reads: make(chan fakeRead, 1)}
			s := newSession(testDevice, conn)
			s.handle(testAddr, tt.data)
			if writes := conn.snapshotWrites(); len(writes) != 0 {
				t.Fatalf("writes = %v, want none", writes)
			}
		})
	}
}

// TestSessionWriteFailReasonCodes verifies a write-phase failure flows back
// through write_fail with the specific reason code.
func TestSessionWriteFailReasonCodes(t *testing.T) {
	zipBytes := buildZip(t)
	conn := &fakeConn{reads: make(chan fakeRead, 1)}
	s := newSession(testDevice, conn)
	s.write = func(_ context.Context, _ []byte, _ DeviceInfo, _ string) (string, string, error) {
		return "", "", &writeError{code: "unzip_fail"}
	}

	mid := len(zipBytes) / 2
	packets := [][]byte{zipBytes[:mid], zipBytes[mid:]}
	crc := crc32.ChecksumIEEE(zipBytes)
	s.handle(testAddr, fileCommand(crc, uint32(len(packets)), uint32(len(zipBytes)), "install.zip"))
	for i, payload := range packets {
		s.handle(testAddr, packetCommand(crc32.ChecksumIEEE(payload), uint32(i+1), uint32(len(payload)), payload))
	}
	s.handle(testAddr, validCommand(crc, uint32(len(packets)), uint32(len(zipBytes)), "install.zip"))
	s.handle(testAddr, writeCommand(crc, uint32(len(packets)), uint32(len(zipBytes)), "install.zip"))

	last := conn.snapshotWrites()[len(conn.snapshotWrites())-1].data
	if !bytes.HasPrefix(last, []byte("write_fail:")) || !bytes.HasSuffix(last, []byte("unzip_fail")) {
		t.Fatalf("last write = %q, want write_fail:unzip_fail", last)
	}
}


// TestReceiptDestinations verifies the startup receipt broadcast targets.
func TestReceiptDestinations(t *testing.T) {
	destinations := ReceiptDestinations()
	if len(destinations) != 2 {
		t.Fatalf("destinations = %v", destinations)
	}
	if destinations[0].String() != "255.255.255.255:39004" {
		t.Fatalf("broadcast = %s", destinations[0])
	}
	if destinations[1].String() != "127.0.0.1:39004" {
		t.Fatalf("loopback = %s", destinations[1])
	}
	if strings.Contains(destinations[0].String(), "39003") {
		t.Fatal("broadcast target wrong port")
	}
}
