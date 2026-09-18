package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteFileReplacesContent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("content = %q, want new", got)
	}
}

func TestClearDirectRegularFilesPreservesDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "old.zip"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ClearDirectRegularFiles(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "old.zip")); !os.IsNotExist(err) {
		t.Fatalf("direct file still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(nested, "keep")); err != nil {
		t.Fatalf("nested file was removed: %v", err)
	}
}

func TestDestructiveClearHelpersRejectSymlinkRoot(t *testing.T) {
	for _, tt := range []struct {
		name  string
		clear func(string) error
	}{
		{name: "direct", clear: ClearDirectRegularFiles},
		{name: "recursive", clear: ClearDirectoryContents},
		{name: "matching", clear: func(path string) error {
			return ClearDirectRegularFilesMatching(path, "dc_ccu_*.json")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			target := filepath.Join(base, "target")
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(target, "dc_ccu_keep.json")
			if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(base, "link")
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("directory symlinks unavailable: %v", err)
			}
			if err := tt.clear(link); err == nil {
				t.Fatal("clear accepted a symlink root")
			}
			if _, err := os.Stat(sentinel); err != nil {
				t.Fatalf("symlink target was modified: %v", err)
			}
		})
	}
}

func TestCleanupKnownTempsRemovesOnlyOwnedTemporaryFiles(t *testing.T) {
	root := t.TempDir()
	remove := []string{
		".cpdc-transfer-session-1",
		".cpdc-output-123.tar",
		".cpdc-ccu-123.json",
		".cpdc-server-123.zip",
		".cpdc-write-123",
		".cpdc-copy-123",
	}
	for _, name := range append(remove, "plan_local.tar", "txbz.zip") {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	nested := filepath.Join(root, ".cpdc-output-directory")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CleanupKnownTemps(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range remove {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("temporary file %q still exists: %v", name, err)
		}
	}
	for _, name := range []string{"plan_local.tar", "txbz.zip", ".cpdc-output-directory"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("non-temporary path %q was removed: %v", name, err)
		}
	}
}

func TestRequiredSpaceIncludesMarginAndFactor(t *testing.T) {
	got, ok := RequiredSpace(100, 200, 300, 2)
	if !ok {
		t.Fatal("unexpected overflow")
	}
	// (raw + expanded + outputs) * factor * 120%
	if got != 1440 {
		t.Fatalf("RequiredSpace = %d, want 1440", got)
	}
}
