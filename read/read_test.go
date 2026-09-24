package read

import (
	"fmt"
	"testing"

	"ontology/snap"
)

func buildLog(m int) *snap.Log {
	l := &snap.Log{}
	for i := 0; i < m; i++ {
		l.Append(fmt.Sprintf("k%06d", i), snap.Put, "v")
	}
	return l
}

// 扫描条数不随 m 线性增长：快照命中走 map，增量按 snapSeq 边界 O(1) 切片。
func TestScanCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		l := buildLog(m)
		base, snapSeq := snap.Build(l.Entries(), int64(m))
		l.Append("k000000", snap.Put, "v2")
		entries := l.Entries()
		for i := 0; i < 5; i++ {
			v, ok, err := ReadAt(entries, base, snapSeq, int64(m+1), "k000042")
			if err != nil || !ok || v != "v" {
				t.Fatalf("m=%d read=(%q,%v,%v)", m, v, ok, err)
			}
			if got := lastScan.Load(); got > 2 {
				t.Fatalf("m=%d 扫描 %d 条，随 m 增长", m, got)
			}
		}
	}
}

// 边界：增量含 atSeq、不含 snapSeq；atSeq<snapSeq 拒绝。
func TestBoundary(t *testing.T) {
	cases := []struct {
		name        string
		snapSeq, at int64
		wantV       string
		wantOK      bool
		wantScan    int64
		wantErr     bool
	}{
		{"atSeq=snapSeq 无增量", 2, 2, "1", true, 0, false},
		{"含 atSeq 那条", 2, 3, "5", true, 1, false},
		{"Del 生效", 2, 4, "", false, 2, false},
		{"atSeq<snapSeq 拒绝", 2, 1, "", false, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := &snap.Log{}
			l.Append("a", snap.Put, "1")
			l.Append("b", snap.Put, "2")
			base, ss := snap.Build(l.Entries(), 2)
			l.Append("a", snap.Put, "5")
			l.Append("a", snap.Del, "")
			v, ok, err := ReadAt(l.Entries(), base, ss, c.at, "a")
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			if v != c.wantV || ok != c.wantOK {
				t.Fatalf("got (%q,%v) want (%q,%v)", v, ok, c.wantV, c.wantOK)
			}
			if got := lastScan.Load(); got != c.wantScan {
				t.Fatalf("scan=%d want %d", got, c.wantScan)
			}
		})
	}
}
