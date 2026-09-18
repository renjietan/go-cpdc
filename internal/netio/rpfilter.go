package netio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type rpFilterController interface {
	Apply(interfaceNames []string) error
	Restore() error
}

type rpFilterStore interface {
	Read(interfaceName string) (string, error)
	Write(interfaceName, value string) error
}

type fileRPFilterStore struct {
	root string
}

func (s fileRPFilterStore) Read(interfaceName string) (string, error) {
	path, err := s.valuePath(interfaceName)
	if err != nil {
		return "", err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(contents), nil
}

func (s fileRPFilterStore) Write(interfaceName, value string) error {
	path, err := s.valuePath(interfaceName)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(value), 0)
}

func (s fileRPFilterStore) valuePath(interfaceName string) (string, error) {
	if interfaceName == "" || interfaceName == "." || interfaceName == ".." ||
		strings.ContainsAny(interfaceName, `/\`) || strings.TrimSpace(interfaceName) != interfaceName {
		return "", fmt.Errorf("invalid network interface name %q", interfaceName)
	}
	return filepath.Join(s.root, interfaceName, "rp_filter"), nil
}

type sysctlRPFilterController struct {
	store     rpFilterStore
	mu        sync.Mutex
	originals map[string]string
	dirty     map[string]bool
	order     []string
	restored  bool
}

func newSysctlRPFilterController(store rpFilterStore) *sysctlRPFilterController {
	return &sysctlRPFilterController{
		store: store, originals: make(map[string]string), dirty: make(map[string]bool),
	}
}

func (c *sysctlRPFilterController) Apply(interfaceNames []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.restored {
		return errors.New("rp_filter controller is already restored")
	}

	start := len(c.order)
	for _, name := range rpFilterNames(interfaceNames) {
		if err := c.disable(name); err != nil {
			rollbackErr := c.rollbackFrom(start)
			return errors.Join(err, rollbackErr)
		}
	}
	return nil
}

func (c *sysctlRPFilterController) disable(name string) error {
	value, err := c.store.Read(name)
	if err != nil {
		return fmt.Errorf("read rp_filter for %s: %w", name, err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("read rp_filter for %s: empty value", name)
	}
	if _, tracked := c.originals[name]; !tracked {
		c.originals[name] = value
		c.order = append(c.order, name)
	}
	if value == "0" {
		return nil
	}
	if err := c.store.Write(name, "0"); err != nil {
		return fmt.Errorf("disable rp_filter for %s: %w", name, err)
	}
	c.dirty[name] = true
	return nil
}

func (c *sysctlRPFilterController) Restore() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.restored {
		return nil
	}

	var restoreErrors []error
	for i := len(c.order) - 1; i >= 0; i-- {
		name := c.order[i]
		if !c.dirty[name] {
			continue
		}
		if err := c.store.Write(name, c.originals[name]); err != nil {
			restoreErrors = append(restoreErrors, fmt.Errorf("restore rp_filter for %s: %w", name, err))
			continue
		}
		delete(c.dirty, name)
	}
	if len(restoreErrors) != 0 {
		return errors.Join(restoreErrors...)
	}
	c.restored = true
	c.originals = nil
	c.dirty = nil
	c.order = nil
	return nil
}

func (c *sysctlRPFilterController) rollbackFrom(start int) error {
	var rollbackErrors []error
	var retained []string
	for i := len(c.order) - 1; i >= start; i-- {
		name := c.order[i]
		if c.dirty[name] {
			if err := c.store.Write(name, c.originals[name]); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("roll back rp_filter for %s: %w", name, err))
				retained = append(retained, name)
				continue
			}
		}
		delete(c.dirty, name)
		delete(c.originals, name)
	}
	c.order = c.order[:start]
	for i := len(retained) - 1; i >= 0; i-- {
		c.order = append(c.order, retained[i])
	}
	return errors.Join(rollbackErrors...)
}

func rpFilterNames(interfaceNames []string) []string {
	names := make([]string, 0, len(interfaceNames)+1)
	seen := make(map[string]struct{}, len(interfaceNames)+1)
	for _, name := range append([]string{"all"}, interfaceNames...) {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}
