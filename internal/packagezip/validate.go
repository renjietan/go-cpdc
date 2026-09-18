package packagezip

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	cpdv1 "cpdc/gen/cpd/v1"
)

type Error struct {
	Code  cpdv1.ErrorCode
	Stage cpdv1.ParseStage
	Path  string
	Err   error
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("package validation failed: %v", e.Err)
	}
	return fmt.Sprintf("package validation failed at %s: %v", e.Path, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func AsError(err error) (*Error, bool) {
	var packageError *Error
	ok := errors.As(err, &packageError)
	return packageError, ok
}

type Package struct {
	archive      *zip.ReadCloser
	entries      map[string]*zip.File
	files        []string
	directories  map[string]bool
	RawSize      uint64
	ExpandedSize uint64
	Nodes        map[string]*Node
	Devices      map[string]*Device
}

func Validate(ctx context.Context, filePath string) (*Package, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, invalid("", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return nil, invalid("", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("input is not a non-empty regular file"))
	}
	if info.Size() > MaxRawSize {
		return nil, invalid("", cpdv1.ErrorCode_ERROR_CODE_PACKAGE_TOO_LARGE, errors.New("raw ZIP exceeds limit"))
	}
	archive, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, invalid("", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("invalid ZIP structure"))
	}
	pkg := &Package{
		archive: archive, entries: make(map[string]*zip.File), directories: make(map[string]bool),
		RawSize: uint64(info.Size()), Nodes: make(map[string]*Node), Devices: make(map[string]*Device),
	}
	fail := func(err error) (*Package, error) {
		_ = archive.Close()
		return nil, err
	}
	if len(archive.File) > MaxEntries {
		return fail(invalid("", cpdv1.ErrorCode_ERROR_CODE_INVALID_ZIP_SIZE, errors.New("ZIP entry count exceeds limit")))
	}
	caseFolded := make(map[string]string, len(archive.File))
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		name, directory, err := validateEntryName(entry)
		if err != nil {
			return fail(err)
		}
		folded := strings.ToLower(name)
		if previous, exists := caseFolded[folded]; exists {
			return fail(invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, fmt.Errorf("path conflicts with %s", previous)))
		}
		caseFolded[folded] = name
		if entry.Flags&0x1 != 0 {
			return fail(invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("encrypted ZIP entry")))
		}
		if directory {
			pkg.directories[name] = true
			continue
		}
		if entry.UncompressedSize64 > MaxEntrySize {
			return fail(invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_ZIP_SIZE, errors.New("entry expanded size exceeds limit")))
		}
		if pkg.ExpandedSize > math.MaxUint64-entry.UncompressedSize64 {
			return fail(invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_ZIP_SIZE, errors.New("expanded size overflow")))
		}
		pkg.ExpandedSize += entry.UncompressedSize64
		if pkg.ExpandedSize > MaxExpandedSize {
			return fail(invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_ZIP_SIZE, errors.New("expanded ZIP exceeds limit")))
		}
		pkg.entries[name] = entry
		pkg.files = append(pkg.files, name)
		markParentDirectories(pkg.directories, name)
	}
	if pkg.ExpandedSize > pkg.RawSize*MaxCompressionRatio {
		return fail(invalid("", cpdv1.ErrorCode_ERROR_CODE_INVALID_ZIP_SIZE, errors.New("ZIP compression ratio exceeds limit")))
	}
	sort.Strings(pkg.files)
	if err := pkg.readAll(ctx); err != nil {
		return fail(err)
	}
	if err := pkg.validateBusiness(ctx); err != nil {
		return fail(err)
	}
	return pkg, nil
}

func (p *Package) Close() error { return p.archive.Close() }

func (p *Package) FileNames() []string {
	return append([]string(nil), p.files...)
}

func (p *Package) DirectoryNames() []string {
	result := make([]string, 0, len(p.directories))
	for name := range p.directories {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func (p *Package) Has(name string) bool {
	_, ok := p.entries[name]
	return ok
}

func (p *Package) HasDirectory(name string) bool {
	return p.directories[strings.TrimSuffix(name, "/")]
}

func (p *Package) Open(name string) (io.ReadCloser, error) {
	entry := p.entries[name]
	if entry == nil {
		return nil, os.ErrNotExist
	}
	return entry.Open()
}

func (p *Package) EntrySize(name string) uint64 {
	if entry := p.entries[name]; entry != nil {
		return entry.UncompressedSize64
	}
	return 0
}

func (p *Package) readAll(ctx context.Context) error {
	var actualTotal uint64
	buffer := make([]byte, 32*1024)
	for _, name := range p.files {
		entry := p.entries[name]
		reader, err := entry.Open()
		if err != nil {
			return invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("cannot open ZIP entry"))
		}
		var actual uint64
		for {
			if err := ctx.Err(); err != nil {
				_ = reader.Close()
				return err
			}
			count, readErr := reader.Read(buffer)
			if count > 0 {
				actual += uint64(count)
				actualTotal += uint64(count)
				if actual > entry.UncompressedSize64 || actual > MaxEntrySize || actualTotal > p.ExpandedSize || actualTotal > MaxExpandedSize {
					_ = reader.Close()
					return invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_ZIP_SIZE, errors.New("actual expanded size exceeds declaration"))
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				_ = reader.Close()
				return invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("ZIP entry CRC or stream validation failed"))
			}
		}
		if err := reader.Close(); err != nil {
			return invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("ZIP entry close failed"))
		}
		if actual != entry.UncompressedSize64 {
			return invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_ZIP_SIZE, errors.New("actual expanded size differs from declaration"))
		}
	}
	if actualTotal != p.ExpandedSize {
		return invalid("", cpdv1.ErrorCode_ERROR_CODE_INVALID_ZIP_SIZE, errors.New("actual total differs from declaration"))
	}
	return nil
}

func validateEntryName(entry *zip.File) (string, bool, error) {
	raw := entry.Name
	if raw == "" || !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) || strings.Contains(raw, `\`) {
		return "", false, invalid("", cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("unsafe or invalid UTF-8 ZIP path"))
	}
	directory := entry.FileInfo().IsDir() || strings.HasSuffix(raw, "/")
	name := strings.TrimSuffix(raw, "/")
	if name == "" || strings.HasPrefix(name, "/") || path.IsAbs(name) || path.Clean(name) != name || strings.HasPrefix(name, "../") {
		return "", false, invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("unsafe ZIP path"))
	}
	if len([]byte(name)) > MaxPathBytes {
		return "", false, invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("ZIP path exceeds limit"))
	}
	for _, component := range strings.Split(name, "/") {
		if err := validateComponent(component); err != nil {
			return "", false, invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, err)
		}
	}
	mode := entry.FileInfo().Mode()
	if !directory && !mode.IsRegular() {
		return "", false, invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("special ZIP entry type"))
	}
	if directory && !entry.FileInfo().IsDir() {
		return "", false, invalid(name, cpdv1.ErrorCode_ERROR_CODE_INVALID_PACKAGE, errors.New("directory path has non-directory mode"))
	}
	return name, directory, nil
}

func validateComponent(component string) error {
	if component == "" || component == "." || component == ".." || len([]byte(component)) > MaxComponentBytes {
		return errors.New("invalid ZIP path component")
	}
	if strings.Contains(component, ":") || strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") {
		return errors.New("ZIP path component is not cross-platform safe")
	}
	base := component
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	upper := strings.ToUpper(base)
	if upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" ||
		(len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9') {
		return errors.New("Windows reserved path component")
	}
	return nil
}

func markParentDirectories(directories map[string]bool, name string) {
	for parent := path.Dir(name); parent != "." && parent != "/"; parent = path.Dir(parent) {
		directories[parent] = true
	}
}

func invalid(filePath string, code cpdv1.ErrorCode, err error) error {
	return &Error{Code: code, Stage: cpdv1.ParseStage_PARSE_STAGE_VALIDATE, Path: filePath, Err: err}
}
