//go:build linux

package netio

const linuxRPFilterRoot = "/proc/sys/net/ipv4/conf"

func newPlatformRPFilterController() rpFilterController {
	return newSysctlRPFilterController(fileRPFilterStore{root: linuxRPFilterRoot})
}
