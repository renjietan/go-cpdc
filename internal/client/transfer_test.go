package client

import (
	"bytes"
	"crypto/sha256"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cpdv1 "cpdc/gen/cpd/v1"
	"cpdc/internal/protocol"
	"google.golang.org/protobuf/proto"
)

func TestValidateTransferStart(t *testing.T) {
	data := bytes.Repeat([]byte("a"), 2401)
	hash := sha256.Sum256(data)
	start := &cpdv1.TransferStartNty{
		FileName: "txbz_demo.zip", FileSize: uint64(len(data)), FileSha256: hash[:],
		ExpandedSize: 4096, RequiredWorkspace: 8192, ChunkSize: ChunkSize, TotalChunks: 3,
	}
	if code := ValidateTransferStart(start, true, 8192); code != cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED {
		t.Fatalf("valid start rejected: %v", code)
	}
	if code := ValidateTransferStart(start, false, 8192); code != cpdv1.ErrorCode_ERROR_CODE_AUTH_BINDING_MISSING {
		t.Fatalf("missing auth code = %v", code)
	}
	start.TotalChunks = 2
	if code := ValidateTransferStart(start, true, 8192); code != cpdv1.ErrorCode_ERROR_CODE_INVALID_MESSAGE {
		t.Fatalf("wrong chunk count code = %v", code)
	}
	start.TotalChunks = 3
	if code := ValidateTransferStart(start, true, 8191); code != cpdv1.ErrorCode_ERROR_CODE_INSUFFICIENT_STORAGE {
		t.Fatalf("space code = %v", code)
	}
	start.FileName = "txbz_demo.bin"
	if code := ValidateTransferStart(start, true, 8192); code != cpdv1.ErrorCode_ERROR_CODE_INVALID_MESSAGE {
		t.Fatalf("non-ZIP filename code = %v", code)
	}
	start.FileName = "txbz_.._demo.zip"
	if code := ValidateTransferStart(start, true, 8192); code != cpdv1.ErrorCode_ERROR_CODE_INVALID_MESSAGE {
		t.Fatalf("unsafe filename code = %v", code)
	}
	start.FileName = strings.Repeat("a", 252) + ".zip"
	if code := ValidateTransferStart(start, true, 8192); code != cpdv1.ErrorCode_ERROR_CODE_INVALID_MESSAGE {
		t.Fatalf("long filename code = %v", code)
	}
	start.FileName = "CON.zip"
	if code := ValidateTransferStart(start, true, 8192); code != cpdv1.ErrorCode_ERROR_CODE_INVALID_MESSAGE {
		t.Fatalf("reserved filename code = %v", code)
	}
}

func TestTransferReceiverOutOfOrderDuplicateAndProgress(t *testing.T) {
	root := t.TempDir()
	data := bytes.Repeat([]byte("0123456789"), 250) // 2500 bytes, 3 chunks.
	start := makeStart("txbz_demo.zip", data)
	receiver, err := OpenTransfer(root, []byte{1, 2, 3}, start)
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Abort()

	progress, accepted, err := receiver.Accept(2, data[2400:], crc32.ChecksumIEEE(data[2400:]))
	if err != nil || !accepted || progress != 30 {
		t.Fatalf("chunk 2: progress=%d accepted=%v err=%v", progress, accepted, err)
	}
	progress, accepted, err = receiver.Accept(2, data[2400:], crc32.ChecksumIEEE(data[2400:]))
	if err != nil || accepted || progress != 0 {
		t.Fatalf("duplicate: progress=%d accepted=%v err=%v", progress, accepted, err)
	}
	progress, accepted, err = receiver.Accept(0, data[:1200], crc32.ChecksumIEEE(data[:1200]))
	if err != nil || !accepted || progress != 60 {
		t.Fatalf("chunk 0: progress=%d accepted=%v err=%v", progress, accepted, err)
	}
	progress, accepted, err = receiver.Accept(1, data[1200:2400], crc32.ChecksumIEEE(data[1200:2400]))
	if err != nil || !accepted || progress != 100 {
		t.Fatalf("chunk 1: progress=%d accepted=%v err=%v", progress, accepted, err)
	}
	path, code, err := receiver.Finalize()
	if err != nil || code != cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED {
		t.Fatalf("Finalize = %q, %v, %v", path, code, err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("received file differs")
	}
}

func TestTransferReceiverMissingRanges(t *testing.T) {
	data := bytes.Repeat([]byte{1}, 1200*8)
	receiver, err := OpenTransfer(t.TempDir(), []byte{7}, makeStart("txbz.zip", data))
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Abort()
	for _, index := range []uint32{0, 3, 4, 7} {
		chunk := data[int(index)*1200 : int(index+1)*1200]
		if _, _, err := receiver.Accept(index, chunk, crc32.ChecksumIEEE(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	ranges := receiver.MissingRanges()
	want := []*cpdv1.MissingRange{{Start: 1, End: 2}, {Start: 5, End: 6}}
	if !proto.Equal(&cpdv1.TransferLossPackReq{MissingRanges: ranges}, &cpdv1.TransferLossPackReq{MissingRanges: want}) {
		t.Fatalf("ranges = %v, want %v", ranges, want)
	}
}

func TestTransferProgressThresholdIsReportedOnce(t *testing.T) {
	data := bytes.Repeat([]byte{4}, 1200*10)
	receiver, err := OpenTransfer(t.TempDir(), []byte{9}, makeStart("txbz.zip", data))
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Abort()
	var progress []uint32
	for index := uint32(0); index < 4; index++ {
		chunk := data[int(index)*1200 : int(index+1)*1200]
		value, _, err := receiver.Accept(index, chunk, crc32.ChecksumIEEE(chunk))
		if err != nil {
			t.Fatal(err)
		}
		progress = append(progress, value)
	}
	want := []uint32{0, 0, 30, 0}
	if !bytes.Equal(uint32Bytes(progress), uint32Bytes(want)) {
		t.Fatalf("progress = %v, want %v", progress, want)
	}
}

func TestBuildLossPacketsSplitsAtProtocolLimit(t *testing.T) {
	ranges := make([]*cpdv1.MissingRange, 500)
	for i := range ranges {
		ranges[i] = &cpdv1.MissingRange{Start: uint32(i * 2), End: uint32(i * 2)}
	}
	identity := &cpdv1.ClientIdentity{
		Esn: "123456789012345678901234567890123456789",
		DeviceTypes: []cpdv1.DeviceType{
			cpdv1.DeviceType_DEVICE_TYPE_CCU,
			cpdv1.DeviceType_DEVICE_TYPE_SERVER,
		},
	}
	counter := byte(1)
	newID := func() ([]byte, error) {
		counter++
		return testID(counter), nil
	}
	packets, err := BuildLossPackets(testID(1), identity, ranges, newID)
	if err != nil {
		t.Fatal(err)
	}
	if len(packets) < 2 {
		t.Fatalf("packet count = %d, want split", len(packets))
	}
	var flattened []*cpdv1.MissingRange
	for _, packet := range packets {
		if proto.Size(packet) > protocol.MaxPacketBytes {
			t.Fatalf("packet size = %d", proto.Size(packet))
		}
		flattened = append(flattened, packet.GetTransferLosspackReq().GetMissingRanges()...)
	}
	if !proto.Equal(&cpdv1.TransferLossPackReq{MissingRanges: flattened}, &cpdv1.TransferLossPackReq{MissingRanges: ranges}) {
		t.Fatal("split changed missing ranges")
	}
}

func TestTransferRejectsBadCRCAndPayloadLength(t *testing.T) {
	data := bytes.Repeat([]byte{2}, 1300)
	receiver, err := OpenTransfer(t.TempDir(), []byte{8}, makeStart("txbz.zip", data))
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Abort()
	if _, _, err := receiver.Accept(0, data[:1200], 0); err == nil {
		t.Fatal("bad CRC accepted")
	}
	if _, _, err := receiver.Accept(0, data[:1199], crc32.ChecksumIEEE(data[:1199])); err == nil {
		t.Fatal("short non-final payload accepted")
	}
}

func TestCommitAndVerifyCache(t *testing.T) {
	root := t.TempDir()
	data := []byte("verified zip bytes")
	start := makeStart("txbz_demo.zip", data)
	temporary := filepath.Join(root, "temporary")
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := CommitCache(root, temporary, start)
	if err != nil {
		t.Fatal(err)
	}
	hit, gotPath, err := CacheMatch(root, start)
	if err != nil || !hit || gotPath != path {
		t.Fatalf("cache match = %v, %q, %v", hit, gotPath, err)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	hit, _, err = CacheMatch(root, start)
	if err != nil || hit {
		t.Fatalf("corrupt cache matched: %v, %v", hit, err)
	}
}

func TestPruneCacheKeepsOnlyVerifiedPackage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "txbz")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "keep.zip")
	stale := filepath.Join(dir, "stale.zip")
	for _, path := range []string{keep, stale} {
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := PruneCache(root, keep); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("verified package removed: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale package remains: %v", err)
	}
}

func makeStart(name string, data []byte) *cpdv1.TransferStartNty {
	hash := sha256.Sum256(data)
	return &cpdv1.TransferStartNty{
		FileName: name, FileSize: uint64(len(data)), FileSha256: hash[:], ExpandedSize: uint64(len(data)),
		RequiredWorkspace: uint64(len(data) * 3), ChunkSize: ChunkSize,
		TotalChunks: uint32((len(data) + int(ChunkSize) - 1) / int(ChunkSize)),
	}
}

func testID(seed byte) []byte {
	id := bytes.Repeat([]byte{seed}, 16)
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}

func uint32Bytes(values []uint32) []byte {
	result := make([]byte, len(values))
	for i, value := range values {
		result[i] = byte(value)
	}
	return result
}
