package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"

	cpdv1 "cpdc/gen/cpd/v1"
	"google.golang.org/protobuf/proto"
)

func TestPacketRoundTrip(t *testing.T) {
	packet := &cpdv1.Packet{
		SessionId: validID(1),
		MessageId: validID(2),
		Body:      &cpdv1.Packet_DiscoverNty{DiscoverNty: &cpdv1.DiscoverNty{}},
	}
	datagram, err := Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint32(datagram[:4]) != Magic {
		t.Fatalf("magic = %x", datagram[:4])
	}
	got, err := Unmarshal(datagram)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(got, packet) {
		t.Fatalf("round trip mismatch: got %v want %v", got, packet)
	}
}

func TestUnmarshalRejectsForeignAndMalformedPackets(t *testing.T) {
	packet := &cpdv1.Packet{
		SessionId: validID(1),
		MessageId: validID(2),
		Body:      &cpdv1.Packet_DiscoverNty{DiscoverNty: &cpdv1.DiscoverNty{}},
	}
	valid, err := Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{
		"too short":     {1, 2, 3},
		"foreign magic": append([]byte{0, 0, 0, 0}, valid[4:]...),
		"bad protobuf":  append([]byte{0xEE, 0xDD, 0xCC, 0xBB}, 0xff),
		"too large":     append(valid, bytes.Repeat([]byte{0}, MaxDatagramBytes-len(valid)+1)...),
	}
	for name, datagram := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Unmarshal(datagram); err == nil {
				t.Fatal("Unmarshal succeeded, want error")
			}
		})
	}
}

func TestMarshalRejectsInvalidEnvelope(t *testing.T) {
	tests := map[string]*cpdv1.Packet{
		"nil body": {
			SessionId: validID(1), MessageId: validID(2),
		},
		"short session": {
			SessionId: []byte{1}, MessageId: validID(2),
			Body: &cpdv1.Packet_DiscoverNty{DiscoverNty: &cpdv1.DiscoverNty{}},
		},
		"not uuid v4": {
			SessionId: bytes.Repeat([]byte{1}, 16), MessageId: validID(2),
			Body: &cpdv1.Packet_DiscoverNty{DiscoverNty: &cpdv1.DiscoverNty{}},
		},
		"oversized body": {
			SessionId: validID(1), MessageId: validID(2),
			Body: &cpdv1.Packet_TransferChunkNty{TransferChunkNty: &cpdv1.TransferChunkNty{Payload: bytes.Repeat([]byte{3}, 1400)}},
		},
	}
	for name, packet := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Marshal(packet); err == nil {
				t.Fatal("Marshal succeeded, want error")
			}
		})
	}
}

func TestNewIDIsUUIDv4(t *testing.T) {
	id, err := NewID(bytes.NewReader(bytes.Repeat([]byte{0xFF}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 16 || id[6]>>4 != 4 || id[8]&0xC0 != 0x80 {
		t.Fatalf("invalid UUID v4 bytes: %x", id)
	}
}

func validID(seed byte) []byte {
	id := bytes.Repeat([]byte{seed}, 16)
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}
