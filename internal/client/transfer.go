package client

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"

	cpdv1 "cpdc/gen/cpd/v1"
	"cpdc/internal/fsutil"
)

const (
	ChunkSize       uint32 = 1200
	MaxPackageBytes uint64 = 1 << 20
	MaxExpandedSize uint64 = 64 << 20
)

var (
	ErrInvalidChunk = errors.New("invalid transfer chunk")
	ErrTransferIO   = errors.New("transfer storage I/O")
)

func ValidateTransferStart(start *cpdv1.TransferStartNty, authenticated bool, available uint64) cpdv1.ErrorCode {
	if !authenticated {
		return cpdv1.ErrorCode_ERROR_CODE_AUTH_BINDING_MISSING
	}
	if start == nil || !validPackageName(start.GetFileName()) || len(start.GetFileSha256()) != sha256.Size || start.GetChunkSize() != ChunkSize || start.GetTotalChunks() == 0 {
		return cpdv1.ErrorCode_ERROR_CODE_INVALID_MESSAGE
	}
	if start.GetFileSize() == 0 {
		return cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE
	}
	if start.GetFileSize() > MaxPackageBytes {
		return cpdv1.ErrorCode_ERROR_CODE_PACKAGE_TOO_LARGE
	}
	wantChunks := uint32((start.GetFileSize() + uint64(ChunkSize) - 1) / uint64(ChunkSize))
	if start.GetTotalChunks() != wantChunks {
		return cpdv1.ErrorCode_ERROR_CODE_INVALID_MESSAGE
	}
	if start.GetExpandedSize() == 0 || start.GetExpandedSize() > MaxExpandedSize || start.GetRequiredWorkspace() < start.GetFileSize()+start.GetExpandedSize() {
		return cpdv1.ErrorCode_ERROR_CODE_INVALID_ZIP_SIZE
	}
	if available < start.GetRequiredWorkspace() {
		return cpdv1.ErrorCode_ERROR_CODE_INSUFFICIENT_STORAGE
	}
	return cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED
}

type Transfer struct {
	start    *cpdv1.TransferStartNty
	file     *os.File
	path     string
	received []bool
	count    uint32
	emitted  [3]bool
	closed   bool
}

func OpenTransfer(root string, sessionID []byte, start *cpdv1.TransferStartNty) (*Transfer, error) {
	if start == nil {
		return nil, errors.New("nil transfer start")
	}
	prefix := ".cpdc-transfer-" + hex.EncodeToString(sessionID) + "-"
	file, err := os.CreateTemp(root, prefix)
	if err != nil {
		return nil, err
	}
	path := file.Name()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := file.Truncate(int64(start.GetFileSize())); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return &Transfer{
		start: start, file: file, path: path,
		received: make([]bool, int(start.GetTotalChunks())),
	}, nil
}

func (t *Transfer) Accept(index uint32, payload []byte, checksum uint32) (progress uint32, accepted bool, err error) {
	if t.closed {
		return 0, false, fmt.Errorf("%w: transfer is closed", ErrInvalidChunk)
	}
	if index >= t.start.GetTotalChunks() {
		return 0, false, fmt.Errorf("%w: chunk index %d out of range", ErrInvalidChunk, index)
	}
	offset := uint64(index) * uint64(ChunkSize)
	remaining := t.start.GetFileSize() - offset
	expected := uint64(ChunkSize)
	if remaining < expected {
		expected = remaining
	}
	if uint64(len(payload)) != expected {
		return 0, false, fmt.Errorf("%w: chunk %d payload length %d, want %d", ErrInvalidChunk, index, len(payload), expected)
	}
	if crc32.ChecksumIEEE(payload) != checksum {
		return 0, false, fmt.Errorf("%w: chunk %d CRC32 mismatch", ErrInvalidChunk, index)
	}
	if t.received[index] {
		return 0, false, nil
	}
	written, err := t.file.WriteAt(payload, int64(offset))
	if err != nil {
		return 0, false, fmt.Errorf("%w: %v", ErrTransferIO, err)
	}
	if written != len(payload) {
		return 0, false, fmt.Errorf("%w: %v", ErrTransferIO, io.ErrShortWrite)
	}
	t.received[index] = true
	t.count++
	percent := t.count * 100 / t.start.GetTotalChunks()
	thresholds := [...]uint32{30, 60, 100}
	for i, threshold := range thresholds {
		if percent >= threshold && !t.emitted[i] {
			t.emitted[i] = true
			progress = threshold
		}
	}
	return progress, true, nil
}

func (t *Transfer) MissingRanges() []*cpdv1.MissingRange {
	var ranges []*cpdv1.MissingRange
	for index := uint32(0); index < t.start.GetTotalChunks(); {
		if t.received[index] {
			index++
			continue
		}
		start := index
		for index+1 < t.start.GetTotalChunks() && !t.received[index+1] {
			index++
		}
		ranges = append(ranges, &cpdv1.MissingRange{Start: start, End: index})
		index++
	}
	return ranges
}

func (t *Transfer) ReceivedChunks() uint32 { return t.count }
func (t *Transfer) TotalChunks() uint32    { return t.start.GetTotalChunks() }
func (t *Transfer) Path() string           { return t.path }

func (t *Transfer) Finalize() (string, cpdv1.ErrorCode, error) {
	if t.closed {
		return t.path, cpdv1.ErrorCode_ERROR_CODE_STORAGE_IO_ERROR, errors.New("transfer is closed")
	}
	if len(t.MissingRanges()) != 0 {
		return t.path, cpdv1.ErrorCode_ERROR_CODE_INVALID_MESSAGE, errors.New("transfer has missing chunks")
	}
	if err := t.file.Sync(); err != nil {
		return t.path, cpdv1.ErrorCode_ERROR_CODE_STORAGE_IO_ERROR, err
	}
	if err := t.file.Close(); err != nil {
		return t.path, cpdv1.ErrorCode_ERROR_CODE_STORAGE_IO_ERROR, err
	}
	t.closed = true
	info, err := os.Stat(t.path)
	if err != nil {
		return t.path, cpdv1.ErrorCode_ERROR_CODE_STORAGE_IO_ERROR, err
	}
	if uint64(info.Size()) != t.start.GetFileSize() {
		return t.path, cpdv1.ErrorCode_ERROR_CODE_FILE_SIZE_MISMATCH, errors.New("received file size mismatch")
	}
	hash, err := hashFile(t.path)
	if err != nil {
		return t.path, cpdv1.ErrorCode_ERROR_CODE_STORAGE_IO_ERROR, err
	}
	if !bytes.Equal(hash[:], t.start.GetFileSha256()) {
		return t.path, cpdv1.ErrorCode_ERROR_CODE_FILE_HASH_MISMATCH, errors.New("received file hash mismatch")
	}
	return t.path, cpdv1.ErrorCode_ERROR_CODE_UNSPECIFIED, nil
}

func (t *Transfer) Abort() error {
	if !t.closed {
		t.closed = true
		_ = t.file.Close()
	}
	if err := os.Remove(t.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func CacheMatch(root string, start *cpdv1.TransferStartNty) (bool, string, error) {
	if start == nil || !validPackageName(start.GetFileName()) {
		return false, "", errors.New("invalid transfer metadata")
	}
	path := filepath.Join(root, "txbz", start.GetFileName())
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if !info.Mode().IsRegular() || uint64(info.Size()) != start.GetFileSize() {
		return false, "", nil
	}
	hash, err := hashFile(path)
	if err != nil {
		return false, "", err
	}
	return bytes.Equal(hash[:], start.GetFileSha256()), path, nil
}

func CommitCache(root, temporary string, start *cpdv1.TransferStartNty) (string, error) {
	info, err := os.Stat(temporary)
	if err != nil {
		return "", err
	}
	if uint64(info.Size()) != start.GetFileSize() {
		return "", errors.New("temporary file size mismatch")
	}
	hash, err := hashFile(temporary)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(hash[:], start.GetFileSha256()) {
		return "", errors.New("temporary file hash mismatch")
	}
	dir := filepath.Join(root, "txbz")
	if err := fsutil.ClearDirectRegularFiles(dir); err != nil {
		return "", err
	}
	destination := filepath.Join(dir, start.GetFileName())
	if err := os.Rename(temporary, destination); err != nil {
		return "", err
	}
	return destination, nil
}

func PruneCache(root, keep string) error {
	dir := filepath.Join(root, "txbz")
	absoluteDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	absoluteKeep, err := filepath.Abs(keep)
	if err != nil {
		return err
	}
	if filepath.Dir(absoluteKeep) != absoluteDir {
		return errors.New("cache keep path is outside txbz directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		candidate := filepath.Join(absoluteDir, entry.Name())
		if candidate == absoluteKeep {
			continue
		}
		if err := os.Remove(candidate); err != nil {
			return err
		}
	}
	return nil
}

func hashFile(path string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	file, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return result, err
	}
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func validPackageName(name string) bool {
	return name != "" && len([]byte(name)) <= 255 && name == filepath.Base(name) && strings.EqualFold(filepath.Ext(name), ".zip") &&
		!strings.Contains(name, "..") && !reservedFileName(name) &&
		!strings.ContainsAny(name, "/\\:\x00") && !strings.HasSuffix(name, ".") && !strings.HasSuffix(name, " ")
}

func reservedFileName(name string) bool {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	upper := strings.ToUpper(base)
	return upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" ||
		(len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9')
}
