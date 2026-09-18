package output

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	cpdv1 "cpdc/gen/cpd/v1"
	"cpdc/internal/fsutil"
	"cpdc/internal/packagezip"
)

func writeRadio(ctx context.Context, root string, pkg *packagezip.Package, device *packagezip.Device, iniPath string, tarPath string, defaultTarDir string) error {
	planFiles := filesWithPrefix(pkg.FileNames(), "1_resource/")
	for _, subnet := range device.Subnets {
		planFiles = append(planFiles, "2_radio_subnet/"+subnet+".json")
	}
	planFiles = append(planFiles, device.Path)
	planFiles = uniqueSorted(planFiles)
	planTemp, err := createTar(ctx, root, pkg, planFiles)
	if err != nil {
		return generationError(err)
	}
	defer os.Remove(planTemp)

	fullRadio := device.Type == cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_RADIO || device.Type == cpdv1.DeviceType_DEVICE_TYPE_MULTI_BAND_HANDHELD
	missionTemp := ""
	if fullRadio {
		missionFiles := filesWithPrefix(pkg.FileNames(), "1_resource/", "2_radio_subnet/")
		for _, name := range pkg.FileNames() {
			base := path.Base(name)
			if strings.HasPrefix(name, "3_device_config/") && (strings.HasPrefix(base, "dc_MMR200_") || strings.HasPrefix(base, "dc_PMR200_")) {
				missionFiles = append(missionFiles, name)
			}
		}
		missionTemp, err = createTar(ctx, root, pkg, uniqueSorted(missionFiles))
		if err != nil {
			return generationError(err)
		}
		defer os.Remove(missionTemp)
	}

	tarDir := tarPath
	if tarDir == "" {
		tarDir = defaultTarDir
	}

	existing, err := os.ReadFile(iniPath)
	if err != nil && !os.IsNotExist(err) {
		return generationError(err)
	}
	updates := []iniUpdate{{section: "ReReadVal", key: "Val", value: "1"}}
	if fullRadio {
		updates = append(updates,
			iniUpdate{section: "MissionVersion", key: "Val", value: "3"},
			iniUpdate{section: "PlanLocalUser", key: "Val", value: device.Alias},
		)
	}
	iniData := updateINI(existing, updates)

	if err := fsutil.AtomicCopy(planTemp, filepath.Join(tarDir, "plan_local.tar"), 0o600); err != nil {
		return writeError(err)
	}
	if fullRadio {
		if err := fsutil.AtomicCopy(missionTemp, filepath.Join(tarDir, "mission.tar.gz"), 0o600); err != nil {
			return writeError(err)
		}
	}
	if err := fsutil.AtomicWriteFile(iniPath, iniData, 0o600); err != nil {
		return writeError(err)
	}
	return nil
}

func createTar(ctx context.Context, root string, pkg *packagezip.Package, files []string) (string, error) {
	temporary, err := os.CreateTemp(root, ".cpdc-output-*.tar")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	clean := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	writer := tar.NewWriter(temporary)
	items := tarItems(files)
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			_ = writer.Close()
			clean()
			return "", err
		}
		if strings.HasSuffix(item, "/") {
			if err := writer.WriteHeader(&tar.Header{Name: item, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
				_ = writer.Close()
				clean()
				return "", err
			}
			continue
		}
		stream, err := pkg.Open(item)
		if err != nil {
			_ = writer.Close()
			clean()
			return "", err
		}
		header := &tar.Header{Name: item, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(pkg.EntrySize(item))}
		if err := writer.WriteHeader(header); err != nil {
			_ = stream.Close()
			_ = writer.Close()
			clean()
			return "", err
		}
		if err := copyWithContext(ctx, writer, stream); err != nil {
			_ = stream.Close()
			_ = writer.Close()
			clean()
			return "", err
		}
		if err := stream.Close(); err != nil {
			_ = writer.Close()
			clean()
			return "", err
		}
	}
	if err := writer.Close(); err != nil {
		clean()
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		clean()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return "", err
	}
	return temporaryPath, nil
}

func filesWithPrefix(files []string, prefixes ...string) []string {
	var result []string
	for _, name := range files {
		for _, prefix := range prefixes {
			if strings.HasPrefix(name, prefix) {
				result = append(result, name)
				break
			}
		}
	}
	return result
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func tarItems(files []string) []string {
	items := make(map[string]bool)
	for _, file := range files {
		items[file] = true
		for parent := path.Dir(file); parent != "." && parent != "/"; parent = path.Dir(parent) {
			items[parent+"/"] = true
		}
	}
	result := make([]string, 0, len(items))
	for item := range items {
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader) error {
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			written, writeErr := destination.Write(buffer[:count])
			if writeErr != nil {
				return writeErr
			}
			if written != count {
				return io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		if count == 0 {
			return errors.New("reader made no progress")
		}
	}
}
