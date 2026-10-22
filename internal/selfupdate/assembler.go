package selfupdate

import (
	"errors"
	"hash/crc32"
	"io"
	"os"
)

// Validation failure reasons reported through valid_fail. The values are part
// of the wire protocol and must stay "size", "crc", and "zip".
var (
	ErrSizeMismatch = errors.New("size")
	ErrCRCMismatch  = errors.New("crc")
	ErrInvalidZip   = errors.New("zip")
)

// Assembler collects ordered packets into a complete ZIP payload, streamed
// to a temporary file to keep memory bounded on low-memory devices. Packet
// numbers are 1-based and must arrive in order (enforced by the caller).
type Assembler struct {
	packetCount int
	totalBytes  int
	crc32       uint32
	fileName    string
	file        *os.File
	path        string
	written     int
	// received tracks how many packets have been stored in order.
	received int
}

func NewAssembler() *Assembler {
	return &Assembler{}
}

func (a *Assembler) Reset() {
	a.Close()
	*a = Assembler{}
}

// Close releases and removes the backing temporary file, if any.
func (a *Assembler) Close() {
	if a.file != nil {
		_ = a.file.Close()
		a.file = nil
	}
	if a.path != "" {
		_ = os.Remove(a.path)
		a.path = ""
	}
}

// SetHeader resets any previous assembly and opens a fresh temporary file
// below tempDir to receive the packets.
func (a *Assembler) SetHeader(packetCount, totalBytes int, crc uint32, fileName, tempDir string) error {
	a.Close()
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(tempDir, ".cpdc-assemble-*")
	if err != nil {
		return err
	}
	a.packetCount = packetCount
	a.totalBytes = totalBytes
	a.crc32 = crc
	a.fileName = fileName
	a.file = file
	a.path = file.Name()
	a.written = 0
	a.received = 0
	return nil
}

// Received returns how many packets have been stored in order so far.
func (a *Assembler) Received() int {
	return a.received
}

// RecordReceived advances the in-order packet counter after a packet is stored.
func (a *Assembler) RecordReceived() {
	a.received++
}

// Put appends payload to the temporary file. Packets must arrive in order;
// out-of-range numbers are ignored. A write error is returned so the caller
// can signal packet_fail.
func (a *Assembler) Put(packetNumber int, payload []byte) error {
	if packetNumber < 1 || packetNumber > a.packetCount {
		return nil
	}
	if a.file == nil {
		return errors.New("assembler not initialized")
	}
	n, err := a.file.Write(payload)
	if err != nil {
		return err
	}
	if n != len(payload) {
		return io.ErrShortWrite
	}
	a.written += n
	return nil
}

func (a *Assembler) Complete() bool {
	return a.packetCount > 0 && a.received == a.packetCount
}

// Path returns the path of the assembled temporary file.
func (a *Assembler) Path() string {
	return a.path
}

// Validate checks the assembled byte count and whole-ZIP CRC32 by streaming
// the temporary file.
func (a *Assembler) Validate() error {
	if a.written != a.totalBytes {
		return ErrSizeMismatch
	}
	if a.file == nil {
		return errors.New("assembler not initialized")
	}
	if _, err := a.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h := crc32.NewIEEE()
	if _, err := io.Copy(h, a.file); err != nil {
		return err
	}
	if h.Sum32() != a.crc32 {
		return ErrCRCMismatch
	}
	return nil
}
