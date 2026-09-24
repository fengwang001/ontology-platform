package persist

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ontology/bucket"
	"ontology/hyper"
	"ontology/vec"
)

const (
	testDims   = 4
	testBits   = 3
	testTables = 2
)

var testVecs = []vec.Vec{
	{1, 2, 3, 4}, {4, 3, 2, 1}, {0, 0, 0, 0},
	{-1, -2, -3, -4}, {0.5, 0.5, 0.5, 0.5}, {9, 8, 7, 6},
}

func buildTestIndex() ([]*hyper.Family, *bucket.Multi, int) {
	fams := []*hyper.Family{
		hyper.New(1, testDims, testBits),
		hyper.New(2, testDims, testBits),
	}
	tabs := bucket.NewMulti(testTables)
	for i, v := range testVecs {
		tabs.Add([]uint64{fams[0].Signature(v), fams[1].Signature(v)}, i)
	}
	return fams, tabs, len(testVecs)
}

func saveTestIndex(t *testing.T) (string, []byte) {
	fams, tabs, count := buildTestIndex()
	path := filepath.Join(t.TempDir(), "index.bin")
	if err := Save(path, fams, tabs, count); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, raw
}

func TestRoundTrip(t *testing.T) {
	fams, tabs, count := buildTestIndex()
	path, _ := saveTestIndex(t)
	d, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if d.Dims != testDims || d.Bits != testBits || d.Count != count {
		t.Errorf("header = %d/%d/%d", d.Dims, d.Bits, d.Count)
	}
	if d.Tabs.Buckets() != tabs.Buckets() {
		t.Errorf("buckets = %d want %d", d.Tabs.Buckets(), tabs.Buckets())
	}
	for i, v := range testVecs {
		sigs := []uint64{fams[0].Signature(v), fams[1].Signature(v)}
		found := false
		for _, id := range d.Tabs.Collect(sigs) {
			if id == i {
				found = true
			}
		}
		if !found {
			t.Errorf("vector %d missing from its buckets after reload", i)
		}
	}
}

func TestTruncationClassification(t *testing.T) {
	_, raw := saveTestIndex(t)
	hyperBytes := testTables * testBits * testDims * 8
	seen := map[error]int{}
	for cut := 1; cut < len(raw); cut++ {
		want := ErrCRC
		switch {
		case cut < headerLen:
			want = ErrHeader
		case cut < headerLen+hyperBytes:
			want = ErrHyper
		case cut < len(raw)-4:
			want = ErrBuckets
		}
		p := filepath.Join(t.TempDir(), "trunc.bin")
		if err := os.WriteFile(p, raw[:cut], 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); !errors.Is(err, want) {
			t.Fatalf("cut=%d: err=%v want %v", cut, err, want)
		}
		seen[want]++
	}
	t.Logf("len=%d header=1..%d hyper=%d..%d buckets=%d..%d crc=%d..%d",
		len(raw), headerLen-1, headerLen, headerLen+hyperBytes-1,
		headerLen+hyperBytes, len(raw)-5, len(raw)-4, len(raw)-1)
	for _, e := range []error{ErrHeader, ErrHyper, ErrBuckets, ErrCRC} {
		if seen[e] == 0 {
			t.Errorf("class %v never observed", e)
		}
	}
}

func TestRecoverConsistent(t *testing.T) {
	path, raw := saveTestIndex(t)
	_, tabs, count := buildTestIndex()
	for cut := 1; cut < len(raw); cut++ {
		p := filepath.Join(t.TempDir(), "trunc.bin")
		if err := os.WriteFile(p, raw[:cut], 0o644); err != nil {
			t.Fatal(err)
		}
		d, rec, err := Recover(p)
		if cut < headerLen+testTables*testBits*testDims*8 {
			if err == nil {
				t.Fatalf("cut=%d: expected header/hyper error", cut)
			}
			continue
		}
		if err != nil {
			t.Fatalf("cut=%d: %v", cut, err)
		}
		if rec.Buckets > tabs.Buckets() || rec.Dropped != 0 {
			t.Fatalf("cut=%d: buckets=%d dropped=%d", cut, rec.Buckets, rec.Dropped)
		}
		for _, tb := range d.Tabs.Dump() {
			for _, ids := range tb {
				for _, id := range ids {
					if id >= d.Count {
						t.Fatalf("cut=%d: dangling id %d", cut, id)
					}
				}
			}
		}
	}
	if d, rec, err := Recover(path); err != nil || rec.Buckets != tabs.Buckets() {
		t.Fatalf("full recover: buckets=%d want %d err=%v", rec.Buckets, tabs.Buckets(), err)
	} else if d.Count != count {
		t.Fatalf("full recover: count=%d want %d", d.Count, count)
	} else {
		t.Logf("full recover: buckets=%d count=%d", rec.Buckets, d.Count)
	}
}

func TestCorruption(t *testing.T) {
	cases := []struct {
		name    string
		corrupt func(raw []byte)
		want    error
		msg     string
	}{
		{"degenerate plane", func(raw []byte) {
			off := headerLen + (1*testBits+2)*testDims*8 // table 1 bit 2
			for i := 0; i < testDims*8; i++ {
				raw[off+i] = 0
			}
		}, ErrDegenerate, "table 1 bit 2"},
		{"crc corruption", func(raw []byte) {
			raw[len(raw)-6] ^= 0xff // flip a byte inside the bucket region
		}, ErrCRC, ""},
		{"bad magic", func(raw []byte) {
			raw[0] ^= 0xff
		}, ErrHeader, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := corruptTestCase(t, tc.corrupt)
			_, err := Load(path)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			if tc.msg != "" && !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("err=%q want substring %q", err, tc.msg)
			}
		})
	}
}

func corruptTestCase(t *testing.T, corrupt func([]byte)) string {
	fams, tabs, count := buildTestIndex()
	path := filepath.Join(t.TempDir(), "index.bin")
	if err := Save(path, fams, tabs, count); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	corrupt(raw)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRecoverDangling(t *testing.T) {
	path := corruptTestCase(t, func(raw []byte) {
		binary.LittleEndian.PutUint32(raw[16:20], 2) // shrink count to 2
	})
	d, rec, err := Recover(path)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Dropped == 0 {
		t.Fatal("expected dangling IDs to be dropped")
	}
	for _, tb := range d.Tabs.Dump() {
		for _, ids := range tb {
			for _, id := range ids {
				if id >= d.Count {
					t.Fatalf("dangling id %d with count %d", id, d.Count)
				}
			}
		}
	}
}
