//go:build linux

package selfupdate

import (
	"bufio"
	"errors"
	"net"
	"os"
	"strings"
)

// defaultRouteIP 从 /proc/net/route 读取默认路由接口，并返回该接口的首个
// 非回环、非链路本地 IPv4 地址；无默认路由或接口无 IPv4 时返回错误。
func defaultRouteIP() (string, error) {
	file, err := os.Open("/proc/net/route")
	if err != nil {
		return "", err
	}
	defer file.Close()

	ifaceName := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 11 {
			continue
		}
		if fields[0] == "Iface" {
			continue
		}
		if fields[1] == "00000000" {
			ifaceName = fields[0]
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if ifaceName == "" {
		return "", errors.New("no default route")
	}

	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return "", err
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 == nil || ip4.IsLoopback() || isLinkLocalIPv4(ip4) {
			continue
		}
		return ip4.String(), nil
	}
	return "", errors.New("default route interface has no IPv4")
}
