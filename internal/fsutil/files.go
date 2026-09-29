package fsutil

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// Copy-phase sentinel errors so callers can distinguish which stage of an
// atomic copy failed.
var (
	ErrOpenSource = errors.New("open source")
	ErrTempWrite  = errors.New("temp write")
	ErrRename     = errors.New("rename")
)

var temporaryPrefixes = []string{
	".cpdc-transfer-",
	".cpdc-output-",
	".cpdc-ccu-",
	".cpdc-server-",
	".cpdc-write-",
	".cpdc-copy-",
}

// AtomicWriteFile writes data to a temporary file beside path and replaces path.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".cpdc-write-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmpName, path)
}

// AtomicCopy copies src into a temporary file beside dst and replaces dst.
func AtomicCopy(src, dst string, perm os.FileMode) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOpenSource, err)
	}
	defer in.Close()
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%w: %v", ErrTempWrite, err)
	}
	out, err := os.CreateTemp(dir, ".cpdc-copy-*")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTempWrite, err)
	}
	tmpName := out.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := out.Chmod(perm); err != nil {
		_ = out.Close()
		return fmt.Errorf("%w: %v", ErrTempWrite, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("%w: %v", ErrTempWrite, err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("%w: %v", ErrTempWrite, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("%w: %v", ErrTempWrite, err)
	}
	if err := replaceFile(tmpName, dst); err != nil {
		return fmt.Errorf("%w: %v", ErrRename, err)
	}
	return nil
}

// ClearDirectRegularFiles removes regular files directly below dir only.
func ClearDirectRegularFiles(dir string) error {
	if err := ensureRealDirectory(dir); err != nil {
		return err
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
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// ClearDirectRegularFilesMatching removes matching regular files directly
// below dir. Subdirectories and non-matching entries are left untouched.
func ClearDirectRegularFilesMatching(dir, pattern string) error {
	if err := ensureRealDirectory(dir); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		matched, err := filepath.Match(pattern, entry.Name())
		if err != nil {
			return err
		}
		if !matched {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// ClearDirectoryContents removes every entry below dir while preserving dir.
// Paths are removed one at a time so a failure leaves the completed work in
// place, matching CPDC's no-rollback output semantics.
func ClearDirectoryContents(dir string) error {
	if err := ensureRealDirectory(dir); err != nil {
		return err
	}
	return clearDirectoryContents(dir)
}

func ensureRealDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return &os.PathError{
			Op:   "clear",
			Path: dir,
			Err:  errors.New("refusing symbolic-link directory root"),
		}
	}
	if !info.IsDir() {
		return &os.PathError{
			Op:   "clear",
			Path: dir,
			Err:  errors.New("path is not a directory"),
		}
	}
	return nil
}

func clearDirectoryContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		entryPath := filepath.Join(dir, entry.Name())
		if entry.Type()&os.ModeSymlink == 0 && entry.IsDir() {
			if err := clearDirectoryContents(entryPath); err != nil {
				return err
			}
		}
		if err := os.Remove(entryPath); err != nil {
			return err
		}
	}
	return nil
}

// CleanupKnownTemps removes only CPDC-owned regular temporary files directly
// below dir. Completed packages, output files, and subdirectories are kept.
func CleanupKnownTemps(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !hasAnyPrefix(entry.Name(), temporaryPrefixes) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// RequiredSpace returns (raw + expanded + temporary outputs) * factor + 20%.
func RequiredSpace(raw, expanded, outputs uint64, factor uint64) (uint64, bool) {
	if factor == 0 {
		return 0, false
	}
	total, ok := add(raw, expanded)
	if !ok {
		return 0, false
	}
	total, ok = add(total, outputs)
	if !ok || total > math.MaxUint64/factor {
		return 0, false
	}
	total *= factor
	if total > math.MaxUint64/120 {
		return 0, false
	}
	return (total*120 + 99) / 100, true
}

func add(a, b uint64) (uint64, bool) {
	if a > math.MaxUint64-b {
		return 0, false
	}
	return a + b, true
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

var ErrInsufficientSpace = errors.New("insufficient storage")

func EnsureSpace(path string, required uint64) error {
	available, err := AvailableSpace(path)
	if err != nil {
		return err
	}
	if available < required {
		return ErrInsufficientSpace
	}
	return nil
}
