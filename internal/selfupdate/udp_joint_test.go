package selfupdate

import (
	"bytes"
	"context"
	"hash/crc32"
	"net"
	"testing"
	"time"
)

// TestRealUDPJointFlow drives the Session over real UDP loopback sockets, the
// same way the CPDS UdpUpdateTransport does: send commands from an ephemeral
// socket to CPDC:39003 and read replies on the CPDS receive port 39004.
func TestRealUDPJointFlow(t *testing.T) {
	cpdsRecv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: serverPort})
	if err != nil {
		t.Skipf("CPDS port %d unavailable: %v", serverPort, err)
	}
	defer cpdsRecv.Close()

	cpdsSend, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer cpdsSend.Close()

	cpdcConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: listenPort})
	if err != nil {
		t.Skipf("CPDC port %d unavailable: %v", listenPort, err)
	}
	defer cpdcConn.Close()

	// 用一个带 SO_BROADCAST 的单播发送 socket 替代真实多网卡枚举，保证回复只发一次、
	// 测试不依赖真实网络接口。
	bcast, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer bcast.Close()
	if err := enableBroadcast(bcast); err != nil {
		t.Fatal(err)
	}

	info := testDevice
	info.RunCommand = []string{"/opt/cpdc/CPDC-HF"}
	s := newSession(info, cpdcConn)
	stubNetwork(t, bcast)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)

	target := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: listenPort}
	send := func(data []byte) {
		if _, err := cpdsSend.WriteToUDP(data, target); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	recv := func() []byte {
		if err := cpdsRecv.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 64*1024)
		n, _, err := cpdsRecv.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("recv (reply routing to port %d failed): %v", serverPort, err)
		}
		return append([]byte(nil), buf[:n]...)
	}
	expect := func(label string, got []byte, prefix, suffix string) {
		if !bytes.HasPrefix(got, []byte(prefix)) {
			t.Fatalf("%s = %q, want prefix %q", label, got, prefix)
		}
		if suffix != "" && !bytes.HasSuffix(got, []byte(suffix)) {
			t.Fatalf("%s = %q, want suffix %q", label, got, suffix)
		}
	}

	send([]byte("Scan"))
	expect("Scan", recv(), "Scan:success", "")

	send([]byte("auth"))
	expect("auth", recv(), "authAck:", "")

	send([]byte("version:2.0.0.0"))
	expect("version ok", recv(), "version_ok:", "")

	send([]byte("version:bad"))
	expect("version fail", recv(), "version_fail:", "format")

	send(fileCommand(0, 1, 3, "x.zip"))
	expect("file", recv(), "fileAck:", "")

	payload := []byte{0, 1, 2}
	send(packetCommand(crc32.ChecksumIEEE(payload), 1, 4, payload))
	expect("packet length", recv(), "packet_fail:", "length")

	send(packetCommand(crc32.ChecksumIEEE(payload)^0xFFFFFFFF, 1, 3, payload))
	expect("packet crc", recv(), "packet_fail:", "crc")

	zipBytes := buildPackageZip(t, map[string]string{"MR9360/CPDC-HF": "NEW-BINARY"})
	crc := crc32.ChecksumIEEE(zipBytes)
	mid := len(zipBytes) / 2
	packets := [][]byte{zipBytes[:mid], zipBytes[mid:]}
	send(fileCommand(crc, uint32(len(packets)), uint32(len(zipBytes)), "install.zip"))
	expect("file real", recv(), "fileAck:", "")
	for i, p := range packets {
		send(packetCommand(crc32.ChecksumIEEE(p), uint32(i+1), uint32(len(p)), p))
		expect("packet ok", recv(), "packet_ok:", "")
	}
	send(validCommand(crc, uint32(len(packets)), uint32(len(zipBytes)), "install.zip"))
	expect("valid ok", recv(), "valid_ok:", "")
}
