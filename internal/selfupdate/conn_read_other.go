//go:build !linux

package selfupdate

import "net"

// enablePacketInfo 在非 Linux 平台为空实现：无 IP_PKTINFO 可用。
func enablePacketInfo(conn *net.UDPConn) error { return nil }

// readPacket 在非 Linux 平台直接用 ReadFromUDP，无接收网卡信息。
func readPacket(u *udpConn, b []byte) (int, *net.UDPAddr, net.IP, error) {
	n, addr, err := u.conn.ReadFromUDP(b)
	return n, addr, nil, err
}
