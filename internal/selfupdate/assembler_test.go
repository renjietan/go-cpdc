package selfupdate

import (
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
)

func TestAssemblerCompleteAndStream(t *testing.T) {
	a := NewAssembler()
	defer a.Close()
	if a.Complete() {
		t.Fatal("fresh assembler is complete")
	}

	payload := []byte("hello world")
	if err := a.SetHeader(3, len(payload), crc32.ChecksumIEEE(payload), "pkg.zip", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if a.Complete() {
		t.Fatal("assembler complete before all packets")
	}
	if err := a.Put(1, []byte("hello ")); err != nil {
		t.Fatal(err)
	}
	a.RecordReceived()
	if a.Complete() {
		t.Fatal("assembler complete after only one packet")
	}
	if err := a.Put(2, []byte("")); err != nil {
		t.Fatal(err)
	}
	a.RecordReceived()
	if a.Complete() {
		t.Fatal("assembler complete with a missing packet")
	}
	if err := a.Put(3, []byte("world")); err != nil {
		t.Fatal(err)
	}
	a.RecordReceived()
	if !a.Complete() {
		t.Fatal("assembler not complete after all packets")
	}
	got, err := os.ReadFile(a.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Fatalf("assembled = %q, want %q", got, "hello world")
	}
}

func TestAssemblerReceivedCounter(t *testing.T) {
	a := NewAssembler()
	defer a.Close()
	if a.Received() != 0 {
		t.Fatalf("fresh Received = %d, want 0", a.Received())
	}
	if err := a.SetHeader(2, 2, 0, "x.zip", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if a.Received() != 0 {
		t.Fatalf("Received after SetHeader = %d, want 0", a.Received())
	}
	a.RecordReceived()
	a.RecordReceived()
	if a.Received() != 2 {
		t.Fatalf("Received = %d, want 2", a.Received())
	}
	a.Reset()
	if a.Received() != 0 {
		t.Fatalf("Received after Reset = %d, want 0", a.Received())
	}
}

func TestAssemblerPutIgnoresOutOfRange(t *testing.T) {
	a := NewAssembler()
	defer a.Close()
	if err := a.SetHeader(1, 1, 0, "x.zip", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(0, []byte("zero")); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(2, []byte("over")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(a.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("assembled = %q, want empty", got)
	}
	if a.Complete() {
		t.Fatal("assembler complete with only out-of-range writes")
	}
}

func TestAssemblerValidate(t *testing.T) {
	payload := []byte("0123456789")

	t.Run("ok", func(t *testing.T) {
		a := NewAssembler()
		defer a.Close()
		if err := a.SetHeader(1, len(payload), crc32.ChecksumIEEE(payload), "x.zip", t.TempDir()); err != nil {
			t.Fatal(err)
		}
		if err := a.Put(1, payload); err != nil {
			t.Fatal(err)
		}
		if err := a.Validate(); err != nil {
			t.Fatalf("Validate = %v, want nil", err)
		}
	})

	t.Run("size mismatch", func(t *testing.T) {
		a := NewAssembler()
		defer a.Close()
		if err := a.SetHeader(1, len(payload)+1, crc32.ChecksumIEEE(payload), "x.zip", t.TempDir()); err != nil {
			t.Fatal(err)
		}
		if err := a.Put(1, payload); err != nil {
			t.Fatal(err)
		}
		if err := a.Validate(); !errors.Is(err, ErrSizeMismatch) {
			t.Fatalf("Validate = %v, want ErrSizeMismatch", err)
		}
	})

	t.Run("crc mismatch", func(t *testing.T) {
		a := NewAssembler()
		defer a.Close()
		if err := a.SetHeader(1, len(payload), crc32.ChecksumIEEE(payload)^0xFFFFFFFF, "x.zip", t.TempDir()); err != nil {
			t.Fatal(err)
		}
		if err := a.Put(1, payload); err != nil {
			t.Fatal(err)
		}
		if err := a.Validate(); !errors.Is(err, ErrCRCMismatch) {
			t.Fatalf("Validate = %v, want ErrCRCMismatch", err)
		}
	})
}

func TestAssemblerResetDeletesFile(t *testing.T) {
	dir := t.TempDir()
	a := NewAssembler()
	defer a.Close()
	if err := a.SetHeader(1, 1, 0, "x.zip", dir); err != nil {
		t.Fatal(err)
	}
	path := a.Path()
	if err := a.Put(1, []byte("x")); err != nil {
		t.Fatal(err)
	}
	a.Reset()
	if a.packetCount != 0 || a.Complete() {
		t.Fatalf("Reset left state: %+v", a)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temp file still exists after Reset: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("temp dir missing: %v", err)
	}
}
