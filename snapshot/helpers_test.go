package snapshot

import (
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func rawFingerprint(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	out := ""
	for _, name := range names {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			t.Fatal(err)
		}
		f.Close()
		out += name + ":" + string(h.Sum(nil)) + "\n"
	}
	return out
}

func asLoadErr(t *testing.T, err error) *LoadError {
	t.Helper()
	if err == nil {
		t.Fatal("expected LoadError, got nil")
	}
	le, ok := err.(*LoadError)
	if !ok {
		t.Fatalf("expected *LoadError, got %T: %v", err, err)
	}
	return le
}
