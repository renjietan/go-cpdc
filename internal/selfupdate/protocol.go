package selfupdate

import (
	"bytes"
	"encoding/binary"
	"errors"
	"regexp"
)

// Self-update wire prefixes. These are deliberately separate from the
// cpd.proto flow and match the CPDS UpdateProtocol constants byte-for-byte.
const (
	scanCommand       = "Scan"
	scanAck           = "Scan:success"
	authCommand       = "auth"
	versionPrefix     = "version:"
	filePrefix        = "file:"
	packetPrefix      = "packet:"
	validPrefix       = "valid:"
	writePrefix       = "write:"
	authAckPrefix     = "authAck:"
	versionOkPrefix   = "version_ok:"
	versionFailPrefix = "version_fail:"
	fileAckPrefix     = "fileAck:"
	packetOkPrefix    = "packet_ok:"
	packetFailPrefix  = "packet_fail:"
	validOkPrefix     = "valid_ok:"
	validFailPrefix   = "valid_fail:"
	writeOkPrefix     = "write_ok:"
	writeFailPrefix   = "write_fail:"
	updateOkPrefix    = "update_ok:"
	updateFailPrefix  = "update_fail:"
)

// versionPattern matches a four-segment numeric version such as 0.1.1.1.
var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)

// command identifies a downlink command received from CPDS.
type command int

const (
	commandUnknown command = iota
	commandScan
	commandAuth
	commandVersion
	commandFile
	commandPacket
	commandValid
	commandWrite
)

// encodeID encodes the device identity carried by every uplink reply except
// Scan:success:
//
//	[type length uint16BE][type UTF-8][ip length uint16BE][ip UTF-8]
func encodeID(deviceType, ip string) []byte {
	typeBytes := []byte(deviceType)
	ipBytes := []byte(ip)
	out := make([]byte, 2+len(typeBytes)+2+len(ipBytes))
	binary.BigEndian.PutUint16(out[0:2], uint16(len(typeBytes)))
	copy(out[2:], typeBytes)
	binary.BigEndian.PutUint16(out[2+len(typeBytes):], uint16(len(ipBytes)))
	copy(out[4+len(typeBytes):], ipBytes)
	return out
}

func encodeAuthAck(d DeviceInfo) []byte {
	return append([]byte(authAckPrefix), encodeID(d.DeviceType, d.IP)...)
}

func encodeVersionOk(d DeviceInfo) []byte {
	out := append([]byte(versionOkPrefix), encodeID(d.DeviceType, d.IP)...)
	return append(out, []byte(d.Version)...)
}

func encodeVersionFail(d DeviceInfo, reason string) []byte {
	out := append([]byte(versionFailPrefix), encodeID(d.DeviceType, d.IP)...)
	return append(out, []byte(reason)...)
}

func encodeFileAck(d DeviceInfo, crc uint32, packetCount, totalBytes uint32, fileName string) []byte {
	out := append([]byte(fileAckPrefix), encodeID(d.DeviceType, d.IP)...)
	out = appendUint32(out, crc)
	out = appendUint32(out, packetCount)
	out = appendUint32(out, totalBytes)
	return append(out, []byte(fileName)...)
}

func encodePacketOk(d DeviceInfo, crc uint32, packetNumber, packetLength uint32) []byte {
	out := append([]byte(packetOkPrefix), encodeID(d.DeviceType, d.IP)...)
	out = appendUint32(out, crc)
	out = appendUint32(out, packetNumber)
	out = appendUint32(out, packetLength)
	return out
}

func encodePacketFail(d DeviceInfo, crc uint32, packetNumber, packetLength uint32, reason string) []byte {
	out := append([]byte(packetFailPrefix), encodeID(d.DeviceType, d.IP)...)
	out = appendUint32(out, crc)
	out = appendUint32(out, packetNumber)
	out = appendUint32(out, packetLength)
	return append(out, []byte(reason)...)
}

func encodeValidOk(d DeviceInfo) []byte {
	return append([]byte(validOkPrefix), encodeID(d.DeviceType, d.IP)...)
}

func encodeValidFail(d DeviceInfo, reason string) []byte {
	out := append([]byte(validFailPrefix), encodeID(d.DeviceType, d.IP)...)
	return append(out, []byte(reason)...)
}

func encodeWriteOk(d DeviceInfo) []byte {
	return append([]byte(writeOkPrefix), encodeID(d.DeviceType, d.IP)...)
}

func encodeWriteFail(d DeviceInfo, reason string) []byte {
	out := append([]byte(writeFailPrefix), encodeID(d.DeviceType, d.IP)...)
	return append(out, []byte(reason)...)
}

func encodeUpdateOk(d DeviceInfo, newVersion string) []byte {
	out := append([]byte(updateOkPrefix), encodeID(d.DeviceType, d.IP)...)
	return append(out, []byte(newVersion)...)
}

func encodeUpdateFail(d DeviceInfo, reason string) []byte {
	out := append([]byte(updateFailPrefix), encodeID(d.DeviceType, d.IP)...)
	return append(out, []byte(reason)...)
}

// parseCommand classifies an inbound downlink command.
func parseCommand(data []byte) command {
	switch string(data) {
	case scanCommand:
		return commandScan
	case authCommand:
		return commandAuth
	}
	switch {
	case bytes.HasPrefix(data, []byte(versionPrefix)):
		return commandVersion
	case bytes.HasPrefix(data, []byte(filePrefix)):
		return commandFile
	case bytes.HasPrefix(data, []byte(packetPrefix)):
		return commandPacket
	case bytes.HasPrefix(data, []byte(validPrefix)):
		return commandValid
	case bytes.HasPrefix(data, []byte(writePrefix)):
		return commandWrite
	default:
		return commandUnknown
	}
}

// parseVersionCommand decodes a "version:" downlink command and validates the
// target version format. A non-empty target is required.
func parseVersionCommand(data []byte) (string, error) {
	if !bytes.HasPrefix(data, []byte(versionPrefix)) {
		return "", errors.New("not a version command")
	}
	target := string(data[len(versionPrefix):])
	if !versionPattern.MatchString(target) {
		return "", errors.New("invalid version format")
	}
	return target, nil
}

// parseFileHeader decodes a "file:" downlink command.
func parseFileHeader(data []byte) (crc, packetCount, totalBytes uint32, fileName string, err error) {
	return parseHeaderLike(filePrefix, data)
}

// parseValidHeader decodes a "valid:" downlink command. It reuses the same
// binary header layout as "file:".
func parseValidHeader(data []byte) (crc, packetCount, totalBytes uint32, fileName string, err error) {
	return parseHeaderLike(validPrefix, data)
}

// parseWriteHeader decodes a "write:" downlink command. It reuses the same
// binary header layout as "file:".
func parseWriteHeader(data []byte) (crc, packetCount, totalBytes uint32, fileName string, err error) {
	return parseHeaderLike(writePrefix, data)
}

func parseHeaderLike(prefix string, data []byte) (crc, packetCount, totalBytes uint32, fileName string, err error) {
	if !bytes.HasPrefix(data, []byte(prefix)) {
		return 0, 0, 0, "", errors.New("not a file-like command")
	}
	offset := len(prefix)
	if len(data) < offset+12 {
		return 0, 0, 0, "", errors.New("file-like command too short")
	}
	crc = binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4
	packetCount = binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4
	totalBytes = binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4
	fileName = string(data[offset:])
	if fileName == "" {
		return 0, 0, 0, "", errors.New("file name is empty")
	}
	if packetCount == 0 {
		return 0, 0, 0, "", errors.New("packet count must be positive")
	}
	return crc, packetCount, totalBytes, fileName, nil
}

// parsePacket decodes a "packet:" downlink command. The returned payload is a
// subslice of data; callers that retain it must copy it.
func parsePacket(data []byte) (crc, packetNumber, packetLength uint32, payload []byte, err error) {
	if !bytes.HasPrefix(data, []byte(packetPrefix)) {
		return 0, 0, 0, nil, errors.New("not a packet command")
	}
	offset := len(packetPrefix)
	if len(data) < offset+12 {
		return 0, 0, 0, nil, errors.New("packet command too short")
	}
	crc = binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4
	packetNumber = binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4
	packetLength = binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4
	payload = data[offset:]
	return crc, packetNumber, packetLength, payload, nil
}

func appendUint32(dst []byte, value uint32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], value)
	return append(dst, buf[:]...)
}