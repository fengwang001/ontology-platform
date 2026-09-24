package progress

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"
)

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		p    *Progress
	}{
		{"empty", &Progress{ID: "a", Total: 0}},
		{"intervals", &Progress{ID: "b", Total: 10, Ivls: [][2]int{{0, 5}, {7, 9}}}},
		{"committed", &Progress{ID: "c", Total: 3, Committed: true, Ivls: [][2]int{{0, 3}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := Save(dir, tc.p); err != nil {
				t.Fatal(err)
			}
			got, err := Load(dir, tc.p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != tc.p.ID || got.Total != tc.p.Total || got.Committed != tc.p.Committed ||
				len(got.Ivls) != len(tc.p.Ivls) {
				t.Fatalf("roundtrip mismatch: %+v vs %+v", got, tc.p)
			}
		})
	}
	// 进度文件不存在：视为从头开始，不是错误
	p, err := Load(dir, "ghost")
	if err != nil || len(p.Ivls) != 0 || p.Committed {
		t.Fatalf("missing file should be fresh start, got %+v err=%v", p, err)
	}
}

// TestTruncate 逐字节截断进度文件，每个截断点都要被正确分类，
// 且恢复进度等于「最大可恢复前缀」。
func TestTruncate(t *testing.T) {
	dir := t.TempDir()
	full := &Progress{ID: "b", Total: 10, Ivls: [][2]int{{0, 5}, {7, 9}}}
	if err := Save(dir, full); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(Path(dir, "b"))
	if err != nil {
		t.Fatal(err)
	}
	hdrLen := bytes.IndexByte(data, '\n') + 1
	crcStart := bytes.LastIndex(data, []byte("CRC "))
	counts := map[ErrKind]int{}
	for cut := 1; cut < len(data); cut++ {
		if err := os.WriteFile(Path(dir, "b"), data[:cut], 0o644); err != nil {
			t.Fatal(err)
		}
		got, gerr := Load(dir, "b")
		var le *LoadError
		if !errors.As(gerr, &le) {
			t.Fatalf("cut=%d: want LoadError, got %v", cut, gerr)
		}
		var want ErrKind
		switch {
		case cut < hdrLen:
			want = ErrHeader
		case cut < crcStart && data[cut-1] != '\n':
			want = ErrInterval
		default:
			want = ErrCRC
		}
		if le.Kind != want {
			t.Fatalf("cut=%d: kind=%d want %d", cut, le.Kind, want)
		}
		counts[le.Kind]++
		// 期望恢复区间数：完整落在 cut 之前的 IVL 行数（头部损坏则为 0）
		wantIvls := 0
		if want != ErrHeader {
			for off := hdrLen; off < crcStart; {
				end := off + bytes.IndexByte(data[off:], '\n') + 1
				if end <= cut {
					wantIvls++
				}
				off = end
			}
		}
		if len(got.Ivls) != wantIvls {
			t.Fatalf("cut=%d: recovered %d intervals, want %d", cut, len(got.Ivls), wantIvls)
		}
	}
	for _, k := range []ErrKind{ErrHeader, ErrInterval, ErrCRC} {
		if counts[k] == 0 {
			t.Fatalf("class %d never produced", k)
		}
	}
	t.Logf("file=%dB hdr=%dB ivl=[%d,%d) crc=[%d,%d) counts=%v",
		len(data), hdrLen, hdrLen, crcStart, crcStart, len(data), counts)
}

func TestLock(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Unix(1000, 0)
	ttl := time.Hour
	cases := []struct {
		name string
		at   time.Time
		want error
	}{
		{"holder active", t0.Add(time.Minute), ErrBusy},
		{"holder expired", t0.Add(2 * time.Hour), nil},
	}
	release, err := Acquire(dir, "b", t0, ttl)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel, err := Acquire(dir, "b", tc.at, ttl)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if rel != nil {
				rel()
			}
		})
	}
}
