package batch

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		b       *Batch
		wantErr error
		dupPos  [2]int
	}{
		{"empty id", &Batch{ID: "", Records: nil}, ErrEmptyID, [2]int{}},
		{"empty batch ok", &Batch{ID: "b", Records: nil}, nil, [2]int{}},
		{"empty key legal", &Batch{ID: "b", Records: []Record{{Key: "", Value: []byte("v")}}}, nil, [2]int{}},
		{"dup key", &Batch{ID: "b", Records: []Record{{Key: "k"}, {Key: "x"}, {Key: "k"}}},
			errDup(), [2]int{0, 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.b)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			var de *DupError
			if errors.As(err, &de) && (de.FirstPos != tc.dupPos[0] || de.SecondPos != tc.dupPos[1]) {
				t.Fatalf("dup pos = (%d,%d), want %v", de.FirstPos, de.SecondPos, tc.dupPos)
			}
		})
	}
}

func errDup() error { return &DupError{Key: "k"} }

func TestManifestRoundTrip(t *testing.T) {
	records := []Record{
		{Key: "", Value: []byte("empty-key")},
		{Key: "a", Value: []byte{0, 1, 2}},
		{Key: "中文键", Value: []byte("")},
	}
	src := &sliceSource{recs: records}
	dir := t.TempDir()
	path := filepath.Join(dir, "m.bin")
	if err := WriteManifest(path, "batch-1", src); err != nil {
		t.Fatal(err)
	}
	fs, err := OpenManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	if fs.ID() != "batch-1" || fs.Count() != len(records) {
		t.Fatalf("header = %q %d", fs.ID(), fs.Count())
	}
	for i, want := range records {
		got, err := fs.At(i)
		if err != nil {
			t.Fatal(err)
		}
		if got.Key != want.Key || !bytes.Equal(got.Value, want.Value) {
			t.Fatalf("record %d = %q/%x, want %q/%x", i, got.Key, got.Value, want.Key, want.Value)
		}
	}
}

type sliceSource struct{ recs []Record }

func (s *sliceSource) Count() int { return len(s.recs) }
func (s *sliceSource) At(i int) (Record, error) { return s.recs[i], nil }
func (s *sliceSource) Close() error { return nil }
