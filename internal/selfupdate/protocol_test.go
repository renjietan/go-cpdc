package selfupdate

import (
	"bytes"
	"testing"
)

var (
	testDevice = DeviceInfo{DeviceType: "MR9360", IP: "192.168.1.10", Version: "1.2.3.4"}
	// [type len=6]["MR9360"][ip len=12]["192.168.1.10"]
	testIDBytes = []byte{
		0x00, 0x06, 'M', 'R', '9', '3', '6', '0',
		0x00, 0x0C, '1', '9', '2', '.', '1', '6', '8', '.', '1', '.', '1', '0',
	}
)

func TestEncodeID(t *testing.T) {
	got := encodeID("MR9360", "192.168.1.10")
	if !bytes.Equal(got, testIDBytes) {
		t.Fatalf("encodeID = % x, want % x", got, testIDBytes)
	}
}

func TestEncodeAuthAck(t *testing.T) {
	want := append([]byte("authAck:"), testIDBytes...)
	if got := encodeAuthAck(testDevice); !bytes.Equal(got, want) {
		t.Fatalf("encodeAuthAck = %q, want %q", got, want)
	}
}

func TestEncodeVersionReplies(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		want := append([]byte("version_ok:"), testIDBytes...)
		want = append(want, []byte("1.2.3.4")...)
		if got := encodeVersionOk(testDevice); !bytes.Equal(got, want) {
			t.Fatalf("encodeVersionOk = %q, want %q", got, want)
		}
	})

	t.Run("fail", func(t *testing.T) {
		want := append([]byte("version_fail:"), testIDBytes...)
		want = append(want, []byte("format")...)
		if got := encodeVersionFail(testDevice, "format"); !bytes.Equal(got, want) {
			t.Fatalf("encodeVersionFail = %q, want %q", got, want)
		}
	})
}

func TestEncodeFileAck(t *testing.T) {
	want := append([]byte("fileAck:"), testIDBytes...)
	want = append(want, 0x01, 0x02, 0x03, 0x04) // CRC32
	want = append(want, 0x00, 0x00, 0x00, 0x02) // packet count
	want = append(want, 0x00, 0x00, 0x04, 0x00) // total bytes
	want = append(want, []byte("a.zip")...)
	if got := encodeFileAck(testDevice, 0x01020304, 2, 0x00000400, "a.zip"); !bytes.Equal(got, want) {
		t.Fatalf("encodeFileAck = % x, want % x", got, want)
	}
}

func TestEncodePacketReplies(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		want := append([]byte("packet_ok:"), testIDBytes...)
		want = append(want, 0xDE, 0xAD, 0xBE, 0xEF) // CRC32
		want = append(want, 0x00, 0x00, 0x00, 0x03) // packet number
		want = append(want, 0x00, 0x00, 0x05, 0x65) // packet length (1381)
		if got := encodePacketOk(testDevice, 0xDEADBEEF, 3, 1381); !bytes.Equal(got, want) {
			t.Fatalf("encodePacketOk = % x, want % x", got, want)
		}
	})

	t.Run("fail", func(t *testing.T) {
		want := append([]byte("packet_fail:"), testIDBytes...)
		want = append(want, 0xDE, 0xAD, 0xBE, 0xEF)
		want = append(want, 0x00, 0x00, 0x00, 0x03)
		want = append(want, 0x00, 0x00, 0x05, 0x65)
		want = append(want, []byte("crc")...)
		if got := encodePacketFail(testDevice, 0xDEADBEEF, 3, 1381, "crc"); !bytes.Equal(got, want) {
			t.Fatalf("encodePacketFail = % x, want % x", got, want)
		}
	})
}

func TestEncodeValidReplies(t *testing.T) {
	if got, want := encodeValidOk(testDevice), append([]byte("valid_ok:"), testIDBytes...); !bytes.Equal(got, want) {
		t.Fatalf("encodeValidOk = %q, want %q", got, want)
	}
	want := append([]byte("valid_fail:"), testIDBytes...)
	want = append(want, []byte("size")...)
	if got := encodeValidFail(testDevice, "size"); !bytes.Equal(got, want) {
		t.Fatalf("encodeValidFail = %q, want %q", got, want)
	}
}

func TestEncodeUpdateReplies(t *testing.T) {
	if got, want := encodeUpdateOk(testDevice, "9.9.9.9"), append(append([]byte("update_ok:"), testIDBytes...), []byte("9.9.9.9")...); !bytes.Equal(got, want) {
		t.Fatalf("encodeUpdateOk = %q, want %q", got, want)
	}
	want := append([]byte("update_fail:"), testIDBytes...)
	want = append(want, []byte("boom")...)
	if got := encodeUpdateFail(testDevice, "boom"); !bytes.Equal(got, want) {
		t.Fatalf("encodeUpdateFail = %q, want %q", got, want)
	}
}

func TestParseCommand(t *testing.T) {
	tests := []struct {
		data []byte
		want command
	}{
		{[]byte("Scan"), commandScan},
		{[]byte("auth"), commandAuth},
		{[]byte("version:1.2.3.4"), commandVersion},
		{[]byte("file:x"), commandFile},
		{[]byte("packet:x"), commandPacket},
		{[]byte("Scan:success"), commandUnknown},
		{[]byte("unknown"), commandUnknown},
	}
	for _, tt := range tests {
		if got := parseCommand(tt.data); got != tt.want {
			t.Fatalf("parseCommand(%q) = %v, want %v", tt.data, got, tt.want)
		}
	}
}

func TestParseVersionCommand(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    string
		wantErr bool
	}{
		{name: "valid", data: []byte("version:1.2.3.4"), want: "1.2.3.4"},
		{name: "multi digit", data: []byte("version:10.20.30.40"), want: "10.20.30.40"},
		{name: "not version", data: []byte("auth"), wantErr: true},
		{name: "empty", data: []byte("version:"), wantErr: true},
		{name: "missing segment", data: []byte("version:1.2.3"), wantErr: true},
		{name: "extra segment", data: []byte("version:1.2.3.4.5"), wantErr: true},
		{name: "non numeric", data: []byte("version:1.2.3.a"), wantErr: true},
		{name: "leading zero", data: []byte("version:0.1.1.1"), want: "0.1.1.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVersionCommand(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseVersionCommand(%q) = %q, want error", tt.data, got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("parseVersionCommand(%q) = %q, want %q", tt.data, got, tt.want)
			}
		})
	}
}

func TestParseFileHeader(t *testing.T) {
	data := []byte("file:")
	data = appendUint32(data, 0x01020304)
	data = appendUint32(data, 7)
	data = appendUint32(data, 123456)
	data = append(data, []byte("pkg.zip")...)

	crc, packetCount, totalBytes, fileName, err := parseFileHeader(data)
	if err != nil {
		t.Fatal(err)
	}
	if crc != 0x01020304 || packetCount != 7 || totalBytes != 123456 || fileName != "pkg.zip" {
		t.Fatalf("parsed = crc %x count %d bytes %d name %q", crc, packetCount, totalBytes, fileName)
	}
}

func TestParseFileHeaderRejectsMalformed(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "not file", data: []byte("packet:")},
		{name: "short", data: []byte("file:123")},
		{name: "empty name", data: appendUint32(appendUint32(appendUint32([]byte("file:"), 0), 1), 0)},
		{name: "zero packets", data: func() []byte {
			d := appendUint32(appendUint32(appendUint32([]byte("file:"), 0), 0), 10)
			return append(d, []byte("x.zip")...)
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, _, _, err := parseFileHeader(tt.data); err == nil {
				t.Fatal("parseFileHeader succeeded, want error")
			}
		})
	}
}

func TestParsePacket(t *testing.T) {
	payload := []byte{0x01, 0x02, 0x03}
	data := []byte("packet:")
	data = appendUint32(data, 0xAABBCCDD)
	data = appendUint32(data, 5)
	data = appendUint32(data, uint32(len(payload)))
	data = append(data, payload...)

	crc, packetNumber, packetLength, gotPayload, err := parsePacket(data)
	if err != nil {
		t.Fatal(err)
	}
	if crc != 0xAABBCCDD || packetNumber != 5 || packetLength != 3 || !bytes.Equal(gotPayload, payload) {
		t.Fatalf("parsed = crc %x num %d len %d payload % x", crc, packetNumber, packetLength, gotPayload)
	}
}

func TestParsePacketRejectsMalformed(t *testing.T) {
	for _, data := range [][]byte{
		[]byte("file:1234"),
		[]byte("packet:"),
		[]byte("packet:1234"),
	} {
		if _, _, _, _, err := parsePacket(data); err == nil {
			t.Fatalf("parsePacket(%q) succeeded, want error", data)
		}
	}
}
