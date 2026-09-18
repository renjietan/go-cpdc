package output

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cpdc/internal/fsutil"
	"cpdc/internal/packagezip"
)

func writeServer(ctx context.Context, root string, pkg *packagezip.Package, packageName, nodeID string) error {
	temporary, err := os.CreateTemp(root, ".cpdc-server-*.zip")
	if err != nil {
		return generationError(err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	writer := zip.NewWriter(temporary)
	for _, directory := range pkg.DirectoryNames() {
		if _, err := writer.Create(directory + "/"); err != nil {
			_ = writer.Close()
			_ = temporary.Close()
			return generationError(err)
		}
	}
	for _, name := range pkg.FileNames() {
		if name == "local_node.json" {
			continue
		}
		if err := ctx.Err(); err != nil {
			_ = writer.Close()
			_ = temporary.Close()
			return generationError(err)
		}
		destination, err := writer.Create(name)
		if err != nil {
			_ = writer.Close()
			_ = temporary.Close()
			return generationError(err)
		}
		source, err := pkg.Open(name)
		if err != nil {
			_ = writer.Close()
			_ = temporary.Close()
			return generationError(err)
		}
		err = copyWithContext(ctx, destination, source)
		closeErr := source.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = writer.Close()
			_ = temporary.Close()
			return generationError(err)
		}
	}
	localNode, err := writer.Create("local_node.json")
	if err != nil {
		_ = writer.Close()
		_ = temporary.Close()
		return generationError(err)
	}
	data, err := json.MarshalIndent(struct {
		LocalNode string `json:"LocalNode"`
	}{LocalNode: nodeID}, "", "  ")
	if err != nil {
		_ = writer.Close()
		_ = temporary.Close()
		return generationError(err)
	}
	data = append(data, '\n')
	if _, err := localNode.Write(data); err != nil {
		_ = writer.Close()
		_ = temporary.Close()
		return generationError(err)
	}
	if err := writer.Close(); err != nil {
		_ = temporary.Close()
		return generationError(err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return generationError(err)
	}
	if err := temporary.Close(); err != nil {
		return generationError(err)
	}
	if err := verifyZIP(temporaryPath); err != nil {
		return generationError(err)
	}
	target := filepath.Join(root, "txbz_server")
	if err := fsutil.ClearDirectRegularFiles(target); err != nil {
		return writeError(err)
	}
	if err := fsutil.AtomicCopy(temporaryPath, filepath.Join(target, packageName), 0o600); err != nil {
		return writeError(err)
	}
	if err := extractZIP(ctx, temporaryPath, filepath.Join(filepath.Dir(root), "txbz_data"), packageName); err != nil {
		return writeError(err)
	}
	return nil
}

func extractZIP(ctx context.Context, filePath, targetRoot, packageName string) error {
	packageDirectory := strings.TrimSuffix(packageName, filepath.Ext(packageName))
	if packageDirectory == "" || packageDirectory == "." || packageDirectory == ".." {
		return errors.New("generated ZIP has invalid package directory name")
	}
	if err := fsutil.ClearDirectoryContents(targetRoot); err != nil {
		return err
	}
	target := filepath.Join(targetRoot, packageDirectory)
	if err := os.Mkdir(target, 0o755); err != nil {
		return err
	}
	archive, err := zip.OpenReader(filePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := strings.TrimSuffix(entry.Name, "/")
		if name == "" {
			continue
		}
		destination := filepath.Join(target, filepath.FromSlash(name))
		relative, err := filepath.Rel(target, destination)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("generated ZIP contains unsafe output path")
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			_ = source.Close()
			return err
		}
		copyErr := copyWithContext(ctx, output, source)
		sourceCloseErr := source.Close()
		outputCloseErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if sourceCloseErr != nil {
			return sourceCloseErr
		}
		if outputCloseErr != nil {
			return outputCloseErr
		}
	}
	return nil
}

func verifyZIP(filePath string) error {
	archive, err := zip.OpenReader(filePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	seenLocalNode := 0
	for _, file := range archive.File {
		if file.Name == "local_node.json" {
			seenLocalNode++
		}
		reader, err := file.Open()
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(io.Discard, reader)
		closeErr := reader.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if seenLocalNode != 1 {
		return errors.New("generated ZIP must contain exactly one local_node.json")
	}
	return nil
}
