package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplacePrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	for _, value := range []string{"before", "after"} {
		if err := Write(path, []byte(value)); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != value {
			t.Fatal("replacement failed")
		}
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".sleep-state-*"))
	if len(files) != 0 {
		t.Fatal("temporary file left behind")
	}
	if err := RefuseLink(filepath.Dir(path)); err == nil {
		t.Fatal("directory accepted as a file")
	}
}

func TestCreateNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Create(path, []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := Create(path, []byte("replacement")); err == nil {
		t.Fatal("existing file replaced")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "original" {
		t.Fatal("original bytes changed")
	}
}
