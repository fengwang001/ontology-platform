package view_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"ontology/view"
)

func TestRecoverTruncatedReal(t *testing.T) {
	const total = 50
	dir := t.TempDir()
	path := filepath.Join(dir, "log")

	v, err := view.New(view.Options{JournalPath: path})
	if err != nil {
		t.Fatal(err)
	}
	seedView(t, v, total)
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data[:len(data)-5], 0o600); err != nil {
		t.Fatal(err)
	}

	refPath := filepath.Join(dir, "ref")
	ref, refVer := cleanPrefix(t, refPath, total-1)

	rec, err := view.New(view.Options{JournalPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()
	if rec.MaxVersion() != refVer {
		t.Fatalf("version %d want %d", rec.MaxVersion(), refVer)
	}
	if rec.TailClass() == "" {
		t.Fatal("truncated journal must report a tail class")
	}
	if !reflect.DeepEqual(rec.Groups(), ref) {
		t.Fatal("recovered truncated view differs from intact prefix")
	}
}
