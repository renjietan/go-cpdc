package selfupdate

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// packetPayloadPreviewBytes is how many leading payload bytes are shown in the
// packet RX log before the "...." truncation marker.
const packetPayloadPreviewBytes = 8

// totalStreamPreviewBytes is how many leading bytes of the whole datagram are
// shown in the 总码流 line before the "...." truncation marker.
const totalStreamPreviewBytes = 30

// hexBytes renders bytes as a comma-separated "0x00, 0x06" style string.
func hexBytes(b []byte) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("0x%02x", x)
	}
	return strings.Join(parts, ", ")
}

// truncatedHex renders at most max leading bytes, followed by a "...." marker
// when the input is longer.
func truncatedHex(b []byte, max int) string {
	if len(b) <= max {
		return hexBytes(b)
	}
	return hexBytes(b[:max]) + ", ...."
}

// logField is one line of a protocol console log.
type logField struct {
	name  string
	value string
	// n is the field's byte length in the datagram.
	n int
}

func (f logField) String() string {
	return fmt.Sprintf("【%s】%s（%d个字节）", f.name, f.value, f.n)
}

// uintField builds a 4-byte unsigned integer field.
func uintField(name string, v uint32) logField {
	return logField{name: name, value: strconv.FormatUint(uint64(v), 10), n: 4}
}

// idFields decodes the ID portion starting at offset and returns its fields
// plus the offset just past the ID. On malformed data it returns no fields and
// the original offset.
func idFields(data []byte, offset int) ([]logField, int) {
	if offset+2 > len(data) {
		return nil, offset
	}
	typeLen := int(binary.BigEndian.Uint16(data[offset : offset+2]))
	typeStart := offset + 2
	typeEnd := typeStart + typeLen
	if typeEnd+2 > len(data) {
		return nil, offset
	}
	ipLen := int(binary.BigEndian.Uint16(data[typeEnd : typeEnd+2]))
	ipStart := typeEnd + 2
	ipEnd := ipStart + ipLen
	if ipEnd > len(data) {
		return nil, offset
	}
	fields := []logField{
		{name: "类型字节长度 uint16BE", value: strconv.Itoa(typeLen), n: 2},
		{name: "类型 UTF-8", value: string(data[typeStart:typeEnd]), n: typeLen},
		{name: "IP字节长度 uint16BE", value: strconv.Itoa(ipLen), n: 2},
		{name: "IP UTF-8", value: string(data[ipStart:ipEnd]), n: ipLen},
	}
	return fields, ipEnd
}

// dumpCommand renders a downlink command into its named fields.
func dumpCommand(data []byte, totalPackets int) (string, []logField) {
	switch parseCommand(data) {
	case commandScan:
		return "Scan", []logField{{name: "指令", value: "Scan", n: len(data)}}
	case commandAuth:
		return "auth", []logField{{name: "指令", value: "auth", n: len(data)}}
	case commandVersion:
		target, err := parseVersionCommand(data)
		fields := []logField{{name: "指令", value: "version:", n: len(versionPrefix)}}
		if err != nil {
			fields = append(fields, logField{name: "目标版本号", value: "非法(invalid)", n: len(data) - len(versionPrefix)})
		} else {
			fields = append(fields, logField{name: "目标版本号", value: target, n: len(target)})
		}
		return "version", fields
	case commandFile:
		crc, count, total, name, err := parseFileHeader(data)
		fields := []logField{{name: "指令", value: "file:", n: len(filePrefix)}}
		if err != nil {
			return "file", fields
		}
		fields = append(fields,
			uintField("CRC32(整ZIP)", crc),
			uintField("包数量", count),
			uintField("文件总字节", total),
			logField{name: "文件名称", value: name, n: len(name)},
		)
		return "file", fields
	case commandPacket:
		crc, num, length, payload, err := parsePacket(data)
		fields := []logField{{name: "指令", value: "packet:", n: len(packetPrefix)}}
		if err != nil {
			return "packet", fields
		}
		fields = append(fields,
			uintField("CRC32(本次分包)", crc),
			logField{name: "包号", value: fmt.Sprintf("%d/%d", num, totalPackets), n: 4},
			uintField("分包长度", length),
			logField{name: "分包数据", value: truncatedHex(payload, packetPayloadPreviewBytes), n: len(payload)},
		)
		return "packet", fields
	default:
		return "unknown", nil
	}
}

// dumpReply renders an uplink reply into its named fields.
func dumpReply(data []byte, totalPackets int) (string, []logField) {
	switch {
	case bytes.HasPrefix(data, []byte(scanAck)):
		return "Scan:success", []logField{{name: "指令", value: scanAck, n: len(data)}}
	case bytes.HasPrefix(data, []byte(authAckPrefix)):
		fields := []logField{{name: "指令", value: authAckPrefix, n: len(authAckPrefix)}}
		idf, _ := idFields(data, len(authAckPrefix))
		return "authAck", append(fields, idf...)
	case bytes.HasPrefix(data, []byte(versionOkPrefix)):
		fields := []logField{{name: "指令", value: versionOkPrefix, n: len(versionOkPrefix)}}
		idf, next := idFields(data, len(versionOkPrefix))
		fields = append(fields, idf...)
		fields = append(fields, logField{name: "当前版本", value: string(data[next:]), n: len(data) - next})
		return "version_ok", fields
	case bytes.HasPrefix(data, []byte(versionFailPrefix)):
		fields := []logField{{name: "指令", value: versionFailPrefix, n: len(versionFailPrefix)}}
		idf, next := idFields(data, len(versionFailPrefix))
		fields = append(fields, idf...)
		fields = append(fields, logField{name: "原因", value: string(data[next:]), n: len(data) - next})
		return "version_fail", fields
	case bytes.HasPrefix(data, []byte(fileAckPrefix)):
		fields := []logField{{name: "指令", value: fileAckPrefix, n: len(fileAckPrefix)}}
		idf, next := idFields(data, len(fileAckPrefix))
		fields = append(fields, idf...)
		if next+12 <= len(data) {
			fields = append(fields,
				uintField("CRC32(整ZIP)", binary.BigEndian.Uint32(data[next:next+4])),
				uintField("包数量", binary.BigEndian.Uint32(data[next+4:next+8])),
				uintField("文件总字节", binary.BigEndian.Uint32(data[next+8:next+12])),
				logField{name: "文件名称", value: string(data[next+12:]), n: len(data) - next - 12},
			)
		}
		return "fileAck", fields
	case bytes.HasPrefix(data, []byte(packetOkPrefix)):
		fields := []logField{{name: "指令", value: packetOkPrefix, n: len(packetOkPrefix)}}
		idf, next := idFields(data, len(packetOkPrefix))
		fields = append(fields, idf...)
		if next+12 <= len(data) {
			pn := binary.BigEndian.Uint32(data[next+4 : next+8])
			fields = append(fields,
				uintField("CRC32(本次分包)", binary.BigEndian.Uint32(data[next:next+4])),
				logField{name: "包号", value: fmt.Sprintf("%d/%d", pn, totalPackets), n: 4},
				uintField("分包长度", binary.BigEndian.Uint32(data[next+8:next+12])),
			)
		}
		return "packet_ok", fields
	case bytes.HasPrefix(data, []byte(packetFailPrefix)):
		fields := []logField{{name: "指令", value: packetFailPrefix, n: len(packetFailPrefix)}}
		idf, next := idFields(data, len(packetFailPrefix))
		fields = append(fields, idf...)
		if next+12 <= len(data) {
			pn := binary.BigEndian.Uint32(data[next+4 : next+8])
			fields = append(fields,
				uintField("CRC32(本次分包)", binary.BigEndian.Uint32(data[next:next+4])),
				logField{name: "包号", value: fmt.Sprintf("%d/%d", pn, totalPackets), n: 4},
				uintField("分包长度", binary.BigEndian.Uint32(data[next+8:next+12])),
				logField{name: "原因", value: string(data[next+12:]), n: len(data) - next - 12},
			)
		}
		return "packet_fail", fields
	case bytes.HasPrefix(data, []byte(validOkPrefix)):
		fields := []logField{{name: "指令", value: validOkPrefix, n: len(validOkPrefix)}}
		idf, _ := idFields(data, len(validOkPrefix))
		return "valid_ok", append(fields, idf...)
	case bytes.HasPrefix(data, []byte(validFailPrefix)):
		fields := []logField{{name: "指令", value: validFailPrefix, n: len(validFailPrefix)}}
		idf, next := idFields(data, len(validFailPrefix))
		fields = append(fields, idf...)
		fields = append(fields, logField{name: "原因", value: string(data[next:]), n: len(data) - next})
		return "valid_fail", fields
	case bytes.HasPrefix(data, []byte(writeOkPrefix)):
		fields := []logField{{name: "指令", value: writeOkPrefix, n: len(writeOkPrefix)}}
		idf, _ := idFields(data, len(writeOkPrefix))
		return "write_ok", append(fields, idf...)
	case bytes.HasPrefix(data, []byte(writeFailPrefix)):
		fields := []logField{{name: "指令", value: writeFailPrefix, n: len(writeFailPrefix)}}
		idf, next := idFields(data, len(writeFailPrefix))
		fields = append(fields, idf...)
		fields = append(fields, logField{name: "原因", value: string(data[next:]), n: len(data) - next})
		return "write_fail", fields
	case bytes.HasPrefix(data, []byte(updateOkPrefix)):
		fields := []logField{{name: "指令", value: updateOkPrefix, n: len(updateOkPrefix)}}
		idf, next := idFields(data, len(updateOkPrefix))
		fields = append(fields, idf...)
		fields = append(fields, logField{name: "新版本", value: string(data[next:]), n: len(data) - next})
		return "update_ok", fields
	case bytes.HasPrefix(data, []byte(updateFailPrefix)):
		fields := []logField{{name: "指令", value: updateFailPrefix, n: len(updateFailPrefix)}}
		idf, next := idFields(data, len(updateFailPrefix))
		fields = append(fields, idf...)
		fields = append(fields, logField{name: "原因", value: string(data[next:]), n: len(data) - next})
		return "update_fail", fields
	default:
		return "unknown", nil
	}
}

// renderLog builds the complete console log entry for one datagram.
func renderLog(direction, ip string, data []byte, totalPackets int) string {
	var fields []logField
	if parseCommand(data) != commandUnknown {
		_, fields = dumpCommand(data, totalPackets)
	} else {
		_, fields = dumpReply(data, totalPackets)
	}

	title := "发送"
	if direction == "RX" {
		title = fmt.Sprintf("接收 - \"%s\"", ip)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "==================== %s ============================\n", title)
	fmt.Fprintf(&b, "总码流：%s\n", truncatedHex(data, totalStreamPreviewBytes))
	for _, f := range fields {
		b.WriteString(f.String())
		b.WriteString("\n")
	}
	return b.String()
}

// logDump prints one downlink command or uplink reply to stdout.
func logDump(direction, ip string, data []byte, totalPackets int) {
	fmt.Fprint(os.Stdout, renderLog(direction, ip, data, totalPackets))
}

// logConnOpen prints the self-update UDP socket binding and device identity.
func logConnOpen(localAddr net.Addr, d DeviceInfo) {
	fmt.Fprintf(os.Stdout, "[OPEN] self-update UDP 监听 %s | device_type=%s ip=%s version=%s\n",
		localAddr.String(), d.DeviceType, d.IP, d.Version)
}

// logConnClose prints when the self-update UDP socket is closed.
func logConnClose(d DeviceInfo) {
	fmt.Fprintf(os.Stdout, "[CLOSE] self-update UDP 连接关闭 | device_type=%s ip=%s\n",
		d.DeviceType, d.IP)
}
