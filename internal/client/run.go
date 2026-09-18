package client

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	cpdv1 "cpdc/gen/cpd/v1"
	"cpdc/internal/netio"
	"cpdc/internal/protocol"
)

func Run(ctx context.Context, transport *netio.Transport, client *Client, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		now := time.Now()
		datagram, source, err := transport.Read(now.Add(protocol.EventLoopPoll))
		if err != nil {
			var networkError net.Error
			if !errors.As(err, &networkError) || !networkError.Timeout() {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		} else {
			packet, decodeErr := protocol.Unmarshal(datagram)
			if decodeErr == nil {
				logger.Debug("received CPD packet", "type", packetType(packet), "source", source.IP.String())
				if err := sendPackets(transport, client.Handle(packet, now)); err != nil {
					return err
				}
			} else if !errors.Is(decodeErr, protocol.ErrForeignPacket) {
				logger.Warn("discarded invalid CPD packet", "source", source.IP.String(), "error_type", "invalid_protocol_message")
			}
		}
		if err := sendPackets(transport, client.Tick(time.Now())); err != nil {
			return err
		}
	}
}

func sendPackets(transport *netio.Transport, packets []*cpdv1.Packet) error {
	for _, packet := range packets {
		datagram, err := protocol.Marshal(packet)
		if err != nil {
			return err
		}
		if err := transport.Send(datagram); err != nil {
			return err
		}
	}
	return nil
}

func packetType(packet *cpdv1.Packet) string {
	switch {
	case packet.GetDiscoverNty() != nil:
		return "DISCOVER_NTY"
	case packet.GetDiscoverRsp() != nil:
		return "DISCOVER_RSP"
	case packet.GetAuthNty() != nil:
		return "AUTH_NTY"
	case packet.GetAuthRsp() != nil:
		return "AUTH_RSP"
	case packet.GetTransferStartNty() != nil:
		return "TRANSFER_START_NTY"
	case packet.GetTransferChunkNty() != nil:
		return "TRANSFER_CHUNK_NTY"
	case packet.GetTransferProgressRsp() != nil:
		return "TRANSFER_PROGRESS_RSP"
	case packet.GetTransferEndNty() != nil:
		return "TRANSFER_END_NTY"
	case packet.GetTransferLosspackReq() != nil:
		return "TRANSFER_LOSSPACK_REQ"
	case packet.GetTransferCompleteRsp() != nil:
		return "TRANSFER_COMPLETE_RSP"
	case packet.GetParseCompleteReq() != nil:
		return "PARSE_COMPLETE_REQ"
	case packet.GetParseCompleteAck() != nil:
		return "PARSE_COMPLETE_ACK"
	default:
		return "UNKNOWN"
	}
}
