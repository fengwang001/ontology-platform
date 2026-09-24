package progress

import (
	"errors"
	"os"
	"testing"
	"time"
)

func sampleFile(t *testing.T) []byte {
	t.Helper()
	return Marshal(&Progress{BatchID: "b1", Total: 30, Intervals: [][2]int{{0, 10}, {10, 20}, {20, 30}}})
}

// lineEnds returns the end offsets (exclusive) of each line in data.
func lineEnds(data []byte) []int {
	var ends []int
	for i, b := range data {
		if b == '\n' {
			ends = append(ends, i+1)
		}
	}
	return ends
}

func TestTruncateEveryByte(t *testing.T) {
	data := sampleFile(t)
	ends := lineEnds(data)
	classOf := func(m int) error {
		switch {
		case m < ends[0]:
			return ErrHeader
		case m > ends[0] && m < ends[len(ends)-1]:
			for i := 1; i < len(ends)-1; i++ {
				if m > ends[i-1] && m < ends[i] {
					return ErrInterval
				}
			}
			return ErrCRC // exact line boundary
		default:
			return ErrCRC // inside/after the CRC trailer line
		}
	}
	frontierOf := func(m int) int {
		f := 0
		for i := 1; i < len(ends)-1; i++ {
			if ends[i] <= m {
				f = i * 10
			}
		}
		return f
	}
	counts := map[error]int{}
	for m := 1; m < len(data); m++ {
		p, err := Parse(data[:m])
		want := classOf(m)
		if !errors.Is(err, want) {
			t.Fatalf("m=%d: got %v, want class %v", m, err, want)
		}
		if p.Frontier() != frontierOf(m) {
			t.Fatalf("m=%d: recovered frontier %d, want %d", m, p.Frontier(), frontierOf(m))
		}
		counts[want]++
	}
	for _, e := range []error{ErrHeader, ErrInterval, ErrCRC} {
		if counts[e] == 0 {
			t.Fatalf("class %v never observed", e)
		}
	}
}

func TestFileLifecycle(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T, dir string)
		wantFront int
		wantErr   error
	}{
		{"missing file is empty start", func(t *testing.T, dir string) {}, 0, nil},
		{"round trip", func(t *testing.T, dir string) {
			if err := Save(dir, &Progress{BatchID: "b1", Total: 30, Intervals: [][2]int{{0, 10}, {10, 20}}}); err != nil {
				t.Fatal(err)
			}
		}, 20, nil},
		{"truncated recovers prefix", func(t *testing.T, dir string) {
			data := sampleFile(t)
			if err := Save(dir, &Progress{BatchID: "b1", Total: 30, Intervals: [][2]int{{0, 10}, {10, 20}, {20, 30}}}); err != nil {
				t.Fatal(err)
			}
			ends := lineEnds(data)
			full, err := Load(dir, "b1")
			if err != nil || full.Frontier() != 30 {
				t.Fatalf("precondition: %v %d", err, full.Frontier())
			}
			if err := writeFile(dir, data[:ends[1]+3]); err != nil { // cut inside 2nd interval line
				t.Fatal(err)
			}
		}, 10, ErrInterval},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			c.setup(t, dir)
			p, err := Load(dir, "b1")
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err=%v, want %v", err, c.wantErr)
			}
			if p.Frontier() != c.wantFront {
				t.Fatalf("frontier=%d, want %d", p.Frontier(), c.wantFront)
			}
		})
	}
}

func writeFile(dir string, data []byte) error {
	return os.WriteFile(path(dir, "b1", "progress"), data, 0o644)
}

func TestLockAndCommit(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name    string
		holder  string
		lockAt  time.Time
		keep    bool // leave a stale lock behind
		wantErr error
	}{
		{"active holder blocks", "other", now, true, ErrBusy},
		{"expired holder is taken over", "other", now.Add(-2 * time.Hour), true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			rel, err := Acquire(dir, "b1", c.holder, c.lockAt, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if !c.keep {
				rel()
			}
			_, err = Acquire(dir, "b1", "me", now, time.Hour)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err=%v, want %v", err, c.wantErr)
			}
		})
	}
	dir := t.TempDir()
	if Committed(dir, "b1") {
		t.Fatal("unexpected commit marker")
	}
	if err := MarkCommitted(dir, "b1"); err != nil {
		t.Fatal(err)
	}
	if !Committed(dir, "b1") {
		t.Fatal("commit marker missing")
	}
}
