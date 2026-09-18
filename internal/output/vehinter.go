package output

import (
	"context"
	"os"
	"path/filepath"

	"cpdc/internal/fsutil"
	"cpdc/internal/packagezip"
)

func writeVehInter(ctx context.Context, root string, pkg *packagezip.Package, device *packagezip.Device, nodeID string, iniPath string, tarPath string, defaultTarDir string) error {
	files := filesWithPrefix(pkg.FileNames(), "0_contacts/", "6_unit/")
	files = append(files, device.Path, "4_net_node/"+nodeID+".json")
	files = uniqueSorted(files)

	tarTemp, err := createTar(ctx, root, pkg, files)
	if err != nil {
		return generationError(err)
	}
	defer os.Remove(tarTemp)

	tarDir := tarPath
	if tarDir == "" {
		tarDir = defaultTarDir
	}
	if err := fsutil.AtomicCopy(tarTemp, filepath.Join(tarDir, "vehinte.tar"), 0o600); err != nil {
		return writeError(err)
	}

	existing, err := os.ReadFile(iniPath)
	if err != nil && !os.IsNotExist(err) {
		return generationError(err)
	}
	iniData := updateINI(existing, []iniUpdate{{section: "ReReadVal", key: "Val", value: "1"}})
	if err := fsutil.AtomicWriteFile(iniPath, iniData, 0o600); err != nil {
		return writeError(err)
	}
	return nil
}
