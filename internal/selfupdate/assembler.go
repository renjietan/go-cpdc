package selfupdate

import (
	"errors"
	"hash/crc32"
)

// Validation failure reasons reported through valid_fail. The values are part
// of the wire protocol and must stay "size", "crc", and "zip".
var (
	ErrSizeMismatch = errors.New("size")
	ErrCRCMismatch  = errors.New("crc")
	ErrInvalidZip   = errors.New("zip")
)

// Assembler collects ordered packets into a complete ZIP payload. Packet
// numbers are 1-based; the storage index is packetNumber-1.
type Assembler struct {
	packetCount int
	totalBytes  int
	crc32       uint32
	fileName    string
	chunks      [][]byte
	// received tracks how many packets have been stored in order.
	received int
}

func NewAssembler() *Assembler {
	return &Assembler{}
}

func (a *Assembler) Reset() {
	*a = Assembler{}
}

func (a *Assembler) SetHeader(packetCount, totalBytes int, crc uint32, fileName string) {
	a.packetCount = packetCount
	a.totalBytes = totalBytes
	a.crc32 = crc
	a.fileName = fileName
	a.chunks = make([][]byte, packetCount)
	a.received = 0
}

// Received returns how many packets have been stored in order so far.
func (a *Assembler) Received() int {
	return a.received
}

// RecordReceived advances the in-order packet counter after a packet is stored.
func (a *Assembler) RecordReceived() {
	a.received++
}

// Put stores payload for a 1-based packet number, overwriting any previous
// value. Out-of-range numbers are ignored.
func (a *Assembler) Put(packetNumber int, payload []byte) {
	if packetNumber < 1 || packetNumber > a.packetCount {
		return
	}
	chunk := make([]byte, len(payload))
	copy(chunk, payload)
	a.chunks[packetNumber-1] = chunk
}

func (a *Assembler) Complete() bool {
	return a.packetCount > 0 && a.received == a.packetCount
}

// Bytes returns the assembled payload in packet order. The result is a fresh
// slice and is safe to retain.
func (a *Assembler) Bytes() []byte {
	size := 0
	for _, chunk := range a.chunks {
		size += len(chunk)
	}
	out := make([]byte, 0, size)
	for _, chunk := range a.chunks {
		out = append(out, chunk...)
	}
	return out
}

// Validate checks the assembled byte count and whole-ZIP CRC32.
func (a *Assembler) Validate() error {
	payload := a.Bytes()
	if len(payload) != a.totalBytes {
		return ErrSizeMismatch
	}
	if crc32.ChecksumIEEE(payload) != a.crc32 {
		return ErrCRCMismatch
	}
	return nil
}
