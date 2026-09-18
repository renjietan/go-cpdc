package client

import (
	"errors"
	"fmt"

	cpdv1 "cpdc/gen/cpd/v1"
	"cpdc/internal/protocol"
	"google.golang.org/protobuf/proto"
)

type IDFactory func() ([]byte, error)

func BuildLossPackets(sessionID []byte, identity *cpdv1.ClientIdentity, ranges []*cpdv1.MissingRange, newID IDFactory) ([]*cpdv1.Packet, error) {
	if !protocol.ValidID(sessionID) || identity == nil || newID == nil {
		return nil, errors.New("invalid loss packet parameters")
	}
	if len(ranges) == 0 {
		return nil, nil
	}
	placeholder := make([]byte, 16)
	placeholder[6] = 0x40
	placeholder[8] = 0x80
	var packets []*cpdv1.Packet
	current := make([]*cpdv1.MissingRange, 0, len(ranges))
	flush := func() error {
		messageID, err := newID()
		if err != nil {
			return err
		}
		if !protocol.ValidID(messageID) {
			return errors.New("ID factory returned invalid UUID v4")
		}
		packet := lossPacket(sessionID, messageID, identity, current)
		if proto.Size(packet) > protocol.MaxPacketBytes {
			return fmt.Errorf("loss packet size %d exceeds protocol limit", proto.Size(packet))
		}
		packets = append(packets, packet)
		current = nil
		return nil
	}
	for _, missing := range ranges {
		if missing == nil || missing.GetStart() > missing.GetEnd() {
			return nil, errors.New("invalid missing range")
		}
		candidate := append(current, &cpdv1.MissingRange{Start: missing.GetStart(), End: missing.GetEnd()})
		if proto.Size(lossPacket(sessionID, placeholder, identity, candidate)) <= protocol.MaxPacketBytes {
			current = candidate
			continue
		}
		if len(current) == 0 {
			return nil, errors.New("single missing range exceeds protocol limit")
		}
		if err := flush(); err != nil {
			return nil, err
		}
		current = []*cpdv1.MissingRange{{Start: missing.GetStart(), End: missing.GetEnd()}}
	}
	if len(current) != 0 {
		if err := flush(); err != nil {
			return nil, err
		}
	}
	return packets, nil
}

func lossPacket(sessionID, messageID []byte, identity *cpdv1.ClientIdentity, ranges []*cpdv1.MissingRange) *cpdv1.Packet {
	clonedRanges := make([]*cpdv1.MissingRange, len(ranges))
	for i, missing := range ranges {
		clonedRanges[i] = &cpdv1.MissingRange{Start: missing.GetStart(), End: missing.GetEnd()}
	}
	return &cpdv1.Packet{
		SessionId: protocol.CloneID(sessionID), MessageId: protocol.CloneID(messageID),
		Body: &cpdv1.Packet_TransferLosspackReq{TransferLosspackReq: &cpdv1.TransferLossPackReq{
			Client: proto.Clone(identity).(*cpdv1.ClientIdentity), MissingRanges: clonedRanges,
		}},
	}
}
