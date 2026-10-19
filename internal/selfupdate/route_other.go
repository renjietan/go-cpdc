//go:build !linux

package selfupdate

import "errors"

// defaultRouteIP 在非 Linux 平台（如 Windows）不实现：直接返回错误，
// 由 resolveBusinessIP 兜底到 detectLocalIP（candidates[0]）。
func defaultRouteIP() (string, error) {
	return "", errors.New("default route lookup is not supported on this platform")
}
