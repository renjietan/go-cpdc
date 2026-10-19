package selfupdate

import (
	"net"
	"time"
)

// udpConn 包装 *net.UDPConn，在读包时额外解析出接收网卡的本地 IP（通过平台相关
// 的 readPacket），供会话确定业务网卡与回复出口。
type udpConn struct {
	conn      *net.UDPConn
	lastLocal net.IP
}

func (u *udpConn) ReadFromUDP(b []byte) (int, *net.UDPAddr, error) {
	n, addr, local, err := readPacket(u, b)
	u.lastLocal = local
	return n, addr, err
}

func (u *udpConn) WriteToUDP(b []byte, addr *net.UDPAddr) (int, error) {
	return u.conn.WriteToUDP(b, addr)
}

func (u *udpConn) SetReadDeadline(t time.Time) error {
	return u.conn.SetReadDeadline(t)
}

func (u *udpConn) Close() error {
	return u.conn.Close()
}

func (u *udpConn) receivedLocalIP() net.IP {
	return u.lastLocal
}
