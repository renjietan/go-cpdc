package netio

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type fakeRPFilterStore struct {
	values    map[string]string
	writes    []string
	failWrite string
}

func (s *fakeRPFilterStore) Read(name string) (string, error) {
	value, ok := s.values[name]
	if !ok {
		return "", errors.New("missing rp_filter value for " + name)
	}
	return value, nil
}

func (s *fakeRPFilterStore) Write(name, value string) error {
	if name == s.failWrite {
		return errors.New("write failed for " + name)
	}
	s.values[name] = value
	s.writes = append(s.writes, name+"="+value)
	return nil
}

func TestRPFilterControllerDisablesAndRestoresSelectedInterfaces(t *testing.T) {
	store := &fakeRPFilterStore{values: map[string]string{"all": "1", "eth0": "1", "eth1": "2"}}
	controller := newSysctlRPFilterController(store)

	if err := controller.Apply([]string{"eth0", "eth1", "eth0"}); err != nil {
		t.Fatal(err)
	}
	if got := store.values; !reflect.DeepEqual(got, map[string]string{"all": "0", "eth0": "0", "eth1": "0"}) {
		t.Fatalf("disabled values = %v", got)
	}
	if err := controller.Restore(); err != nil {
		t.Fatal(err)
	}
	if got := store.values; !reflect.DeepEqual(got, map[string]string{"all": "1", "eth0": "1", "eth1": "2"}) {
		t.Fatalf("restored values = %v", got)
	}
}

func TestRPFilterControllerKeepsFirstOriginalValueAcrossRepeatedApply(t *testing.T) {
	store := &fakeRPFilterStore{values: map[string]string{"all": "1", "eth0": "1", "eth1": "1"}}
	controller := newSysctlRPFilterController(store)

	if err := controller.Apply([]string{"eth0"}); err != nil {
		t.Fatal(err)
	}
	if err := controller.Apply([]string{"eth0", "eth1"}); err != nil {
		t.Fatal(err)
	}
	if err := controller.Restore(); err != nil {
		t.Fatal(err)
	}
	if got := store.values; !reflect.DeepEqual(got, map[string]string{"all": "1", "eth0": "1", "eth1": "1"}) {
		t.Fatalf("restored values = %v", got)
	}
}

func TestRPFilterControllerRollsBackOnlyNewValuesAfterApplyFailure(t *testing.T) {
	store := &fakeRPFilterStore{values: map[string]string{"all": "1", "eth0": "1", "eth1": "1", "eth2": "1"}}
	controller := newSysctlRPFilterController(store)
	if err := controller.Apply([]string{"eth0"}); err != nil {
		t.Fatal(err)
	}

	store.failWrite = "eth2"
	if err := controller.Apply([]string{"eth1", "eth2"}); err == nil {
		t.Fatal("Apply succeeded despite injected write failure")
	}
	if store.values["all"] != "0" || store.values["eth0"] != "0" {
		t.Fatalf("previously managed values changed: %v", store.values)
	}
	if store.values["eth1"] != "1" || store.values["eth2"] != "1" {
		t.Fatalf("new values were not rolled back: %v", store.values)
	}

	store.failWrite = ""
	if err := controller.Restore(); err != nil {
		t.Fatal(err)
	}
	if store.values["all"] != "1" || store.values["eth0"] != "1" {
		t.Fatalf("tracked values were not restored: %v", store.values)
	}
}

func TestRPFilterControllerRestoreIsIdempotent(t *testing.T) {
	store := &fakeRPFilterStore{values: map[string]string{"all": "0", "eth0": "0"}}
	controller := newSysctlRPFilterController(store)
	if err := controller.Apply([]string{"eth0"}); err != nil {
		t.Fatal(err)
	}
	if err := controller.Restore(); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) != 0 {
		t.Fatalf("unchanged zero values were written during restore: %v", store.writes)
	}
	writes := len(store.writes)
	if err := controller.Restore(); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) != writes {
		t.Fatalf("second Restore wrote values: %v", store.writes[writes:])
	}
}

func TestFileRPFilterStoreReadsAndWritesValue(t *testing.T) {
	root := t.TempDir()
	interfaceDir := filepath.Join(root, "eth0")
	if err := os.Mkdir(interfaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	valuePath := filepath.Join(interfaceDir, "rp_filter")
	if err := os.WriteFile(valuePath, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := fileRPFilterStore{root: root}

	value, err := store.Read("eth0")
	if err != nil {
		t.Fatal(err)
	}
	if value != "1\n" {
		t.Fatalf("Read value = %q", value)
	}
	if err := store.Write("eth0", "0"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(valuePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "0" {
		t.Fatalf("written value = %q", contents)
	}
}

func TestFileRPFilterStoreRejectsInvalidInterfaceName(t *testing.T) {
	store := fileRPFilterStore{root: t.TempDir()}
	if _, err := store.Read("../all"); err == nil {
		t.Fatal("Read accepted path traversal")
	}
}
