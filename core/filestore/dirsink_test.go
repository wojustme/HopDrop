package filestore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xurenhe/hopdrop/core/protocol"
)

func TestDirSinkRejectsSymlinkParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	sink, err := NewDirSink(root)
	if err != nil {
		t.Fatal(err)
	}
	meta := protocol.FileMeta{ID: "1", Name: "file.txt", RelPath: "escape/file.txt", Size: 4}
	if err := sink.Create(meta, strings.NewReader("data")); err == nil {
		t.Fatal("symlink parent was accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "file.txt")); !os.IsNotExist(err) {
		t.Fatal("file escaped the download directory")
	}
}

func TestDirSinkDoesNotFinalizeShortContent(t *testing.T) {
	root := t.TempDir()
	sink, err := NewDirSink(root)
	if err != nil {
		t.Fatal(err)
	}
	meta := protocol.FileMeta{ID: "1", Name: "file.txt", RelPath: "file.txt", Size: 10}
	if err := sink.Create(meta, strings.NewReader("short")); err == nil {
		t.Fatal("short content was accepted")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("short transfer left %d files", len(entries))
	}
}
