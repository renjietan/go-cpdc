package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"

	cpdv1 "cpdc/gen/cpd/v1"
	"google.golang.org/protobuf/proto"
)

const (
	Magic            uint32 = 0xEEDDCCBB
	MagicBytes              = 4
	MaxPacketBytes          = 1396
	MaxDatagramBytes        = MagicBytes + MaxPacketBytes
)

var (
	ErrForeignPacket   = errors.New("foreign packet magic")
	ErrInvalidEnvelope = errors.New("invalid packet envelope")
)

func Marshal(packet *cpdv1.Packet) ([]byte, error) {
	if err := ValidateEnvelope(packet); err != nil {
		return nil, err
	}
	if proto.Size(packet) > MaxPacketBytes {
		return nil, fmt.Errorf("%w: protobuf packet exceeds %d bytes", ErrInvalidEnvelope, MaxPacketBytes)
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(packet)
	if err != nil {
		return nil, fmt.Errorf("marshal protobuf: %w", err)
	}
	if len(payload) > MaxPacketBytes {
		return nil, fmt.Errorf("%w: encoded packet exceeds %d bytes", ErrInvalidEnvelope, MaxPacketBytes)
	}
	datagram := make([]byte, MagicBytes+len(payload))
	binary.BigEndian.PutUint32(datagram[:MagicBytes], Magic)
	copy(datagram[MagicBytes:], payload)
	return datagram, nil
}

func Unmarshal(datagram []byte) (*cpdv1.Packet, error) {
	if len(datagram) < MagicBytes || len(datagram) > MaxDatagramBytes {
		return nil, fmt.Errorf("%w: datagram length %d", ErrInvalidEnvelope, len(datagram))
	}
	if binary.BigEndian.Uint32(datagram[:MagicBytes]) != Magic {
		return nil, ErrForeignPacket
	}
	packet := new(cpdv1.Packet)
	if err := proto.Unmarshal(datagram[MagicBytes:], packet); err != nil {
		return nil, fmt.Errorf("decode protobuf: %w", err)
	}
	if err := ValidateEnvelope(packet); err != nil {
		return nil, err
	}
	return packet, nil
}

func ValidateEnvelope(packet *cpdv1.Packet) error {
	if packet == nil {
		return fmt.Errorf("%w: nil packet", ErrInvalidEnvelope)
	}
	if !ValidID(packet.GetSessionId()) {
		return fmt.Errorf("%w: session_id is not UUID v4", ErrInvalidEnvelope)
	}
	if !ValidID(packet.GetMessageId()) {
		return fmt.Errorf("%w: message_id is not UUID v4", ErrInvalidEnvelope)
	}
	if packet.GetBody() == nil {
		return fmt.Errorf("%w: missing body", ErrInvalidEnvelope)
	}
	return nil
}
