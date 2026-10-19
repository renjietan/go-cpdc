//go:build linux

package selfupdate

import (
	"net"

	"golang.org/x/sys/unix"
)

// enablePacketInfo 开启 IP_PKTINFO，使 recvmsg 附带接收网卡信息。
func enablePacketInfo(conn *net.UDPConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		sockErr = unix.SetsockoptInt(int(fd), unix.SOL_IP, unix.IP_PKTINFO, 1)
	}); err != nil {
		return err
	}
	return sockErr
}

// readPacket 用 ReadMsgUDP 读包并解析 IP_PKTINFO，返回接收网卡的本地 IP。
// 未开启或未解析出 pktinfo 时返回 nil。
func readPacket(u *udpConn, b []byte) (int, *net.UDPAddr, net.IP, error) {
	oob := make([]byte, unix.CmsgSpace(12))
	n, oobn, _, addr, err := u.conn.ReadMsgUDP(b, oob)
	if err != nil {
		return n, addr, nil, err
	}
	cmsgs, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return n, addr, nil, nil
	}
	for _, m := range cmsgs {
		if m.Header.Level == unix.SOL_IP && m.Header.Type == unix.IP_PKTINFO && len(m.Data) >= 12 {
			// in_pktinfo 布局：ifindex(4) + spec_dst(4) + addr(4)；spec_dst 即接收网卡本地 IP。
			return n, addr, net.IPv4(m.Data[4], m.Data[5], m.Data[6], m.Data[7]), nil
		}
	}
	return n, addr, nil, nil
}
