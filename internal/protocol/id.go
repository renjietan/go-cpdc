package protocol

import (
	"bytes"
	"crypto/rand"
	"io"
)

func NewRandomID() ([]byte, error) {
	return NewID(rand.Reader)
}

func NewID(random io.Reader) ([]byte, error) {
	id := make([]byte, 16)
	if _, err := io.ReadFull(random, id); err != nil {
		return nil, err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id, nil
}

func ValidID(id []byte) bool {
	return len(id) == 16 && id[6]>>4 == 4 && id[8]&0xc0 == 0x80
}

func SameID(a, b []byte) bool {
	return bytes.Equal(a, b)
}

func CloneID(id []byte) []byte {
	return append([]byte(nil), id...)
}
