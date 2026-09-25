package journal

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"ontology/change"
)

func gk(i int) string {
	s := "g"
	for n := i; n < 100; n *= 10 {
		s += "x"
	}
	return s + itoa3(i)
}

func itoa3(i int) string {
	return string(rune('0'+i/100)) + string(rune('0'+(i/10)%10)) + string(rune('0'+i%10))
}

func makeChanges(n int) []change.Change {
	cs := make([]change.Change, n)
	for i := range cs {
		g := gk(i)
		cs[i] = change.Change{
			Ver: uint64(i + 1), Op: change.Insert, ID: uint64(i + 1),
			Group: &g, Val: float64(i + 1),
		}
	}
	return cs
}

// TestTruncateEveryByte：200 条等长记录，从 1 字节截断到 len-1，
// 每个截断点必须落入四类之一，且重放只装载完整前缀。
func TestTruncateEveryByte(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "j")
	cs := makeChanges(200)
	j, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if err := j.Append(c); err != nil {
			t.Fatal(err)
		}
	}
	j.Close()
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	recLen := len(cs[0].Encode()) + 8
	first := cs[0].Encode()
	_ = first
	classes := map[error]int{}
	for cut := 1; cut < len(full); cut++ {
		tp := filepath.Join(dir, "cut")
		if err := os.WriteFile(tp, full[:cut], 0o600); err != nil {
			t.Fatal(err)
		}
		got, rerr := Replay(tp)
		wantClass := classify(cut, recLen)
		if !errors.Is(rerr, wantClass) {
			t.Fatalf("cut=%d class=%v want %v", cut, rerr, wantClass)
		}
		classes[wantClass]++
		// 已完整记录数：头后完整记录边界。
		wantN := prefixCount(cut, recLen)
		if len(got) != wantN {
			t.Fatalf("cut=%d replayed=%d want %d", cut, len(got), wantN)
		}
		for k := range got {
			if got[k].Ver != cs[k].Ver || *got[k].Group != *cs[k].Group ||
				got[k].Val != cs[k].Val {
				t.Fatalf("cut=%d record %d corrupted", cut, k)
			}
		}
	}
	for _, e := range []error{ErrHeaderIncomplete, ErrLenIncomplete, ErrBodyIncomplete, ErrCRC} {
		if classes[e] == 0 {
			t.Fatalf("class %v never observed", e)
		}
	}
	t.Logf("classes=%v recLen=%d totalLen=%d", classes, recLen, len(full))
}

// classify 返回截断点 cut 应处的分类。记录从偏移 5 开始，每记录 recLen。
func classify(cut, recLen int) error {
	if cut < 5 {
		return ErrHeaderIncomplete
	}
	off := cut - 5
	pos := off % recLen
	switch {
	case pos < 4:
		return ErrLenIncomplete
	case pos < recLen-4:
		return ErrBodyIncomplete
	default:
		return ErrCRC
	}
}

func prefixCount(cut, recLen int) int {
	if cut < 5 {
		return 0
	}
	return (cut - 5) / recLen
}

func TestAppendAndCleanReplay(t *testing.T) {
	cases := []struct {
		name string
		n    int
	}{
		{"empty", 0}, {"one", 1}, {"many", 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "j")
			j, err := Create(path)
			if err != nil {
				t.Fatal(err)
			}
			cs := makeChanges(tc.n)
			for _, c := range cs {
				if err := j.Append(c); err != nil {
					t.Fatal(err)
				}
			}
			j.Close()
			got, err := Replay(path)
			if tc.n == 0 {
				if !errors.Is(err, io.EOF) || len(got) != 0 {
					t.Fatalf("empty replay err=%v n=%d", err, len(got))
				}
				return
			}
			if err != nil || len(got) != tc.n {
				t.Fatalf("replay err=%v n=%d want %d", err, len(got), tc.n)
			}
		})
	}
}

func TestBadMagicAndCorruptCRC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j")
	j, _ := Create(path)
	for _, c := range makeChanges(3) {
		_ = j.Append(c)
	}
	j.Close()
	data, _ := os.ReadFile(path)

	bad := bytes.Clone(data)
	bad[2] ^= 0xFF
	os.WriteFile(path, bad, 0o600)
	if _, err := Replay(path); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("magic err=%v", err)
	}

	recLen := len(makeChanges(1)[0].Encode()) + 8
	corrupt := bytes.Clone(data)
	corrupt[5+recLen/2] ^= 0xFF // 翻转首条记录中部字节
	os.WriteFile(path, corrupt, 0o600)
	got, err := Replay(path)
	if !errors.Is(err, ErrCRC) || len(got) != 0 {
		t.Fatalf("crc err=%v n=%d", err, len(got))
	}
}
