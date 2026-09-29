package selfupdate

import (
	"strings"
	"testing"
)

func TestHexBytes(t *testing.T) {
	if got := hexBytes([]byte{0x00, 0x06, 0xff}); got != "0x00, 0x06, 0xff" {
		t.Fatalf("hexBytes = %q", got)
	}
}

func TestDumpCommandNames(t *testing.T) {
	tests := []struct {
		data []byte
		name string
	}{
		{[]byte("Scan"), "Scan"},
		{[]byte("auth"), "auth"},
		{[]byte("version:1.2.3.4"), "version"},
		{[]byte("version:bad"), "version"},
		{fileCommand(0, 1, 3, "x.zip"), "file"},
		{packetCommand(1, 1, 1, []byte{1}), "packet"},
		{[]byte("garbage"), "unknown"},
	}
	for _, tt := range tests {
		if name, _ := dumpCommand(tt.data, 0); name != tt.name {
			t.Fatalf("dumpCommand(%q) = %q, want %q", tt.data, name, tt.name)
		}
	}
}

func TestDumpReplyNames(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "scan", data: []byte(scanAck), want: "Scan:success"},
		{name: "auth", data: encodeAuthAck(testDevice), want: "authAck"},
		{name: "version ok", data: encodeVersionOk(testDevice), want: "version_ok"},
		{name: "version fail", data: encodeVersionFail(testDevice, "format"), want: "version_fail"},
		{name: "fileAck", data: encodeFileAck(testDevice, 1, 2, 3, "x.zip"), want: "fileAck"},
		{name: "packet ok", data: encodePacketOk(testDevice, 1, 2, 3), want: "packet_ok"},
		{name: "packet fail", data: encodePacketFail(testDevice, 1, 2, 3, "length"), want: "packet_fail"},
		{name: "valid ok", data: encodeValidOk(testDevice), want: "valid_ok"},
		{name: "valid fail", data: encodeValidFail(testDevice, "zip"), want: "valid_fail"},
		{name: "update ok", data: encodeUpdateOk(testDevice, "9.9.9.9"), want: "update_ok"},
		{name: "update fail", data: encodeUpdateFail(testDevice, "install_error"), want: "update_fail"},
	}
	for _, tt := range tests {
		if name, _ := dumpReply(tt.data, 0); name != tt.want {
			t.Fatalf("dumpReply(%s) = %q, want %q", tt.name, name, tt.want)
		}
	}
}

func TestRenderLogHeaders(t *testing.T) {
	rx := renderLog("RX", "192.168.1.20", []byte("Scan"), 0)
	if !strings.Contains(rx, "接收 - \"192.168.1.20\"") {
		t.Fatalf("RX header title missing:\n%s", rx)
	}
	if !strings.HasPrefix(rx, "==================== ") {
		t.Fatalf("RX header separator missing:\n%s", rx)
	}
	if !strings.Contains(rx, "总码流：0x53, 0x63, 0x61, 0x6e") {
		t.Fatalf("RX 总码流 missing:\n%s", rx)
	}

	tx := renderLog("TX", "192.168.1.20", []byte(scanAck), 0)
	if !strings.Contains(tx, "==================== 发送 =") {
		t.Fatalf("TX header missing:\n%s", tx)
	}
}

func TestDumpPacketTruncatesPayload(t *testing.T) {
	payload := make([]byte, 100)
	for i := range payload {
		payload[i] = byte(i)
	}
	data := packetCommand(123, 2, uint32(len(payload)), payload)

	_, fields := dumpCommand(data, 5)

	var payloadField *logField
	for i := range fields {
		if fields[i].name == "分包数据" {
			payloadField = &fields[i]
		}
	}
	if payloadField == nil {
		t.Fatalf("packet fields missing 分包数据: %v", fields)
	}
	if !strings.Contains(payloadField.value, "....") {
		t.Fatalf("payload not truncated: %q", payloadField.value)
	}
	if strings.Contains(payloadField.value, "0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08") {
		t.Fatalf("payload shows too many bytes: %q", payloadField.value)
	}
	if payloadField.n != 100 {
		t.Fatalf("payload byte count = %d, want 100", payloadField.n)
	}
}

func TestDumpPacketIncludesProgress(t *testing.T) {
	data := packetCommand(123, 2, 3, []byte("abc"))
	_, fields := dumpCommand(data, 5)

	if !hasField(fields, "包号", "2/5") {
		t.Fatalf("packet fields missing 包号 2/5: %v", fields)
	}
}

func TestDumpReplyPacketOkIncludesProgress(t *testing.T) {
	data := encodePacketOk(testDevice, 1, 2, 3)
	_, fields := dumpReply(data, 5)

	if !hasField(fields, "包号", "2/5") {
		t.Fatalf("packet_ok fields missing 包号 2/5: %v", fields)
	}
}

func TestDumpReplyPacketFailIncludesReason(t *testing.T) {
	data := encodePacketFail(testDevice, 1, 2, 3, "sequence")
	_, fields := dumpReply(data, 5)

	if !hasField(fields, "原因", "sequence") {
		t.Fatalf("packet_fail fields missing reason sequence: %v", fields)
	}
	if !hasField(fields, "包号", "2/5") {
		t.Fatalf("packet_fail fields missing 包号 2/5: %v", fields)
	}
}

func TestRenderLogTruncatesTotalStream(t *testing.T) {
	data := make([]byte, 100)
	for i := range data {
		data[i] = byte(i)
	}
	out := renderLog("TX", "192.168.1.20", data, 0)

	if !strings.Contains(out, "....") {
		t.Fatalf("总码流 not truncated:\n%s", out)
	}
	if strings.Contains(out, "0x63") {
		t.Fatalf("总码流 shows too many bytes (0x63 = 99th byte):\n%s", out)
	}
}

func TestDumpReplyAuthIncludesIDFields(t *testing.T) {
	data := encodeAuthAck(testDevice)
	_, fields := dumpReply(data, 0)

	for _, want := range []struct{ name, value string }{
		{"类型 UTF-8", "MR9360"},
		{"IP UTF-8", "192.168.1.10"},
	} {
		if !hasField(fields, want.name, want.value) {
			t.Fatalf("authAck fields missing %s=%s: %v", want.name, want.value, fields)
		}
	}
}

func hasField(fields []logField, name, value string) bool {
	for _, f := range fields {
		if f.name == name && f.value == value {
			return true
		}
	}
	return false
}
