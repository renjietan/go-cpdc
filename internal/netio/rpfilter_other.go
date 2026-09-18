//go:build !linux

package netio

type noopRPFilterController struct{}

func (noopRPFilterController) Apply([]string) error { return nil }

func (noopRPFilterController) Restore() error { return nil }

func newPlatformRPFilterController() rpFilterController { return noopRPFilterController{} }
