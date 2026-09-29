package selfupdate

import (
	"bytes"
	"errors"
	"hash/crc32"
	"testing"
)

func TestAssemblerCompleteAndBytes(t *testing.T) {
	a := NewAssembler()
	if a.Complete() {
		t.Fatal("fresh assembler is complete")
	}

	payload := []byte("hello world")
	a.SetHeader(3, len(payload), crc32.ChecksumIEEE(payload), "pkg.zip")
	if a.Complete() {
		t.Fatal("assembler complete before all packets")
	}
	a.Put(1, []byte("hello "))
	a.RecordReceived()
	if a.Complete() {
		t.Fatal("assembler complete after only one packet")
	}
	a.Put(2, []byte(""))
	a.RecordReceived()
	if a.Complete() {
		t.Fatal("assembler complete with a missing packet")
	}
	a.Put(3, []byte("world"))
	a.RecordReceived()
	if !a.Complete() {
		t.Fatal("assembler not complete after all packets")
	}
	if got := a.Bytes(); !bytes.Equal(got, []byte("hello world")) {
		t.Fatalf("assembled = %q, want %q", got, "hello world")
	}
}

func TestAssemblerReceivedCounter(t *testing.T) {
	a := NewAssembler()
	if a.Received() != 0 {
		t.Fatalf("fresh Received = %d, want 0", a.Received())
	}
	a.SetHeader(2, 2, 0, "x.zip")
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

func TestAssemblerPutOverwrites(t *testing.T) {
	a := NewAssembler()
	a.SetHeader(2, 3, 0, "x.zip")
	a.Put(1, []byte("old"))
	a.Put(1, []byte("new"))
	if got := a.Bytes(); !bytes.Equal(got, []byte("new")) {
		t.Fatalf("assembled = %q, want new", got)
	}
}

func TestAssemblerPutIgnoresOutOfRange(t *testing.T) {
	a := NewAssembler()
	a.SetHeader(1, 1, 0, "x.zip")
	a.Put(0, []byte("zero"))
	a.Put(2, []byte("over"))
	if got := a.Bytes(); len(got) != 0 {
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
		a.SetHeader(1, len(payload), crc32.ChecksumIEEE(payload), "x.zip")
		a.Put(1, payload)
		if err := a.Validate(); err != nil {
			t.Fatalf("Validate = %v, want nil", err)
		}
	})

	t.Run("size mismatch", func(t *testing.T) {
		a := NewAssembler()
		a.SetHeader(1, len(payload)+1, crc32.ChecksumIEEE(payload), "x.zip")
		a.Put(1, payload)
		if err := a.Validate(); !errors.Is(err, ErrSizeMismatch) {
			t.Fatalf("Validate = %v, want ErrSizeMismatch", err)
		}
	})

	t.Run("crc mismatch", func(t *testing.T) {
		a := NewAssembler()
		a.SetHeader(1, len(payload), crc32.ChecksumIEEE(payload)^0xFFFFFFFF, "x.zip")
		a.Put(1, payload)
		if err := a.Validate(); !errors.Is(err, ErrCRCMismatch) {
			t.Fatalf("Validate = %v, want ErrCRCMismatch", err)
		}
	})
}

func TestAssemblerBytesIsIndependentCopy(t *testing.T) {
	a := NewAssembler()
	a.SetHeader(1, 5, 0, "x.zip")
	a.Put(1, []byte("abcde"))
	got := a.Bytes()
	got[0] = 'z'
	if want := a.Bytes(); !bytes.Equal(want, []byte("abcde")) {
		t.Fatalf("mutating Bytes() result changed assembler state: %q", want)
	}
}

func TestAssemblerReset(t *testing.T) {
	a := NewAssembler()
	a.SetHeader(1, 1, 0, "x.zip")
	a.Put(1, []byte("x"))
	a.Reset()
	if a.packetCount != 0 || a.Complete() {
		t.Fatalf("Reset left state: %+v", a)
	}
}
