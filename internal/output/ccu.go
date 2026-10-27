package output

import (
	"context"
	"io"
	"os"
	"path/filepath"

	cpdv1 "cpdc/gen/cpd/v1"
	"cpdc/internal/fsutil"
	"cpdc/internal/packagezip"
)

// CCUAbsoluteUpdateDir is the fixed absolute directory CPDC-CCU publishes its
// configuration to. It is a variable so tests can redirect it to a temporary
// directory.
var CCUAbsoluteUpdateDir = "/home/update"

// ccuUpdateDir resolves the published configuration directory: CCU writes to
// the absolute CCUAbsoluteUpdateDir, CCUAudio to ../update relative to the
// executable directory.
func ccuUpdateDir(root string, deviceType cpdv1.DeviceType) string {
	if deviceType == cpdv1.DeviceType_DEVICE_TYPE_CCU_AUDIO {
		return filepath.Join(filepath.Dir(root), "update")
	}
	return CCUAbsoluteUpdateDir
}

func writeCCU(ctx context.Context, root string, pkg *packagezip.Package, device *packagezip.Device, deviceType cpdv1.DeviceType) error {
	temporary, err := os.CreateTemp(root, ".cpdc-ccu-*.json")
	if err != nil {
		return generationError(err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	stream, err := pkg.Open(device.Path)
	if err != nil {
		_ = temporary.Close()
		return generationError(err)
	}
	err = copyWithContext(ctx, temporary, stream)
	closeSourceErr := stream.Close()
	if err == nil {
		err = closeSourceErr
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeTempErr := temporary.Close()
	if err == nil {
		err = closeTempErr
	}
	if err != nil {
		return generationError(err)
	}
	info, err := os.Stat(temporaryPath)
	if err != nil || uint64(info.Size()) != pkg.EntrySize(device.Path) {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return generationError(err)
	}
	target := filepath.Join(root, "txbz_ccu")
	if err := fsutil.ClearDirectoryContents(target); err != nil {
		return writeError(err)
	}
	fileName := filepath.Base(device.Path)
	targetPath := filepath.Join(target, fileName)
	if err := fsutil.AtomicCopy(temporaryPath, targetPath, 0o600); err != nil {
		return writeError(err)
	}
	update := ccuUpdateDir(root, deviceType)
	if err := fsutil.ClearDirectRegularFilesMatching(update, "dc_ccu_*.json"); err != nil {
		return writeError(err)
	}
	if err := fsutil.AtomicCopy(targetPath, filepath.Join(update, fileName), 0o600); err != nil {
		return writeError(err)
	}
	if err := fsutil.ClearDirectoryContents(target); err != nil {
		return writeError(err)
	}
	return nil
}
