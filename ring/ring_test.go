package ring

import (
	"errors"
	"testing"

	"ontology/cursor"
)

func TestConfigErrors(t *testing.T) {
	cases := []struct {
		name    string
		cap     int
		limit   int
		wantErr error
	}{
		{"zero-cap", 0, 2, ErrInvalidCapacity},
		{"neg-cap", -3, 2, ErrInvalidCapacity},
		{"zero-limit", 4, 0, ErrInvalidReaderLimit},
		{"neg-limit", 4, -1, ErrInvalidReaderLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := New(tc.cap, tc.limit)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if b != nil {
				t.Fatal("buffer must be nil on config error")
			}
		})
	}
	if errors.Is(ErrInvalidCapacity, ErrInvalidReaderLimit) {
		t.Fatal("two config errors must be distinguishable")
	}
}

func TestAppendSeqMonotonic(t *testing.T) {
	b, _ := New(2, 1)
	var prev int64
	for i := 0; i < 100; i++ {
		seq := b.Append(i)
		if seq != prev+1 {
			t.Fatalf("seq=%d prev=%d not strictly +1", seq, prev)
		}
		prev = seq
	}
	st := b.Stats()
	if st.Live != 2 || st.Head != 100 || st.Oldest != 99 || st.Overwritten != 98 {
		t.Fatalf("stats after wrap wrong: %+v", st)
	}
	if err := b.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyAndFullDistinct(t *testing.T) {
	cases := []struct {
		name       string
		appends    int
		wantLive   int64
		wantOldest int64
		wantHead   int64
	}{
		{"empty", 0, 0, 0, 0},
		{"partial", 2, 2, 1, 2},
		{"exact-full", 4, 4, 1, 4},
		{"wrapped", 6, 4, 3, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := New(4, 1)
			for i := 0; i < tc.appends; i++ {
				b.Append(i)
			}
			st := b.Stats()
			if st.Live != tc.wantLive || st.Oldest != tc.wantOldest || st.Head != tc.wantHead {
				t.Fatalf("got live=%d oldest=%d head=%d", st.Live, st.Oldest, st.Head)
			}
		})
	}
}

func TestFallBehindBoundaryAndMissed(t *testing.T) {
	// 注册后只由该读者承受覆盖：差 N 可读，差 N+k 掉队且 Missed=k。
	cases := []struct {
		n       int
		appends int
		missed  int64
	}{
		{1, 1, 0},  // 容量 1 写满：差 1（=N），可读
		{1, 2, 1},  // 差 2（=N+1），掉队 1
		{4, 4, 0},  // 刚好写满 N：可读最旧一条
		{4, 5, 1},  // 覆盖 1 条
		{4, 7, 3},  // 覆盖 3 条
		{16, 16, 0},
		{16, 20, 4},
	}
	for _, tc := range cases {
		b, _ := New(tc.n, 1)
		r, _ := b.Register()
		for i := 0; i < tc.appends; i++ {
			b.Append(i)
		}
		seq, _, err := b.Read(r)
		if tc.missed == 0 {
			if err != nil || seq != 1 {
				t.Fatalf("n=%d appends=%d: expected readable seq1, got seq=%d err=%v",
					tc.n, tc.appends, seq, err)
			}
			continue
		}
		var fb *cursor.FellBehindError
		if !errors.As(err, &fb) || fb.Missed != tc.missed {
			t.Fatalf("n=%d appends=%d: want Missed=%d got %v",
				tc.n, tc.appends, tc.missed, err)
		}
		if r.Status() != cursor.Behind || b.Stats().Behind != 1 {
			t.Fatalf("n=%d behind cursor not marked/counted", tc.n)
		}
		// 恢复后立刻不再掉队、计数清零，且第一读是当前最旧记录。
		old, rerr := b.Recover(r)
		if rerr != nil || old != tc.missed+1 {
			t.Fatalf("recover old=%d err=%v", old, rerr)
		}
		if s, _, rerr := b.Read(r); rerr != nil || s != old {
			t.Fatalf("post-recover read s=%d err=%v want %d", s, rerr, old)
		}
		if err := b.SelfCheck(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecoverContinuity(t *testing.T) {
	b, _ := New(3, 1)
	r, _ := b.Register()
	for i := 0; i < 5; i++ {
		b.Append(i)
	}
	_, _, err := b.Read(r)
	var fb *cursor.FellBehindError
	if !errors.As(err, &fb) || fb.Missed != 2 {
		t.Fatalf("missed=%v want 2", err)
	}
	old, err := b.Recover(r)
	if err != nil || old != 3 {
		t.Fatalf("recover old=%d err=%v, want 3", old, err)
	}
	var seq int64
	for i := 0; i < 3; i++ {
		s, v, rerr := b.Read(r)
		if rerr != nil {
			t.Fatalf("post-recover read %d: %v", i, rerr)
		}
		if s != old+int64(i) || v != int(old)+i-1 {
			t.Fatalf("post-recover seq=%d v=%v want %d", s, v, old+int64(i))
		}
		seq = s
	}
	if seq != 5 {
		t.Fatalf("last seq=%d want 5", seq)
	}
	if _, _, err := b.Read(r); !errors.Is(err, ErrNoNewRecord) {
		t.Fatalf("caught-up read err=%v want ErrNoNewRecord", err)
	}
	b.Append(99)
	if s, _, _ := b.Read(r); s != 6 {
		t.Fatal("new append after recovery not read continuously")
	}
	if err := b.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestReaderIsolation(t *testing.T) {
	b, _ := New(2, 3)
	slow, _ := b.Register()
	fast, _ := b.Register()
	for i := 0; i < 5; i++ {
		b.Append(i)
	}
	for {
		if _, _, err := b.Read(fast); err != nil {
			break
		}
	}
	// slow 掉队：fast 与另一个新读者不受影响。
	_, _, err := b.Read(slow)
	var fb *cursor.FellBehindError
	if !errors.As(err, &fb) {
		t.Fatal("slow should be behind")
	}
	other, _ := b.Register()
	b.Append(42)
	if s, v, _ := b.Read(other); s != 6 || v != 42 {
		t.Fatalf("other reader interfered: s=%d v=%v", s, v)
	}
	if _, err := b.Recover(slow); err != nil {
		t.Fatal(err)
	}
	// 容量 2、head=5，恢复点为最旧可读序号 4。
	if s, _, _ := b.Read(slow); s != 4 {
		t.Fatalf("slow recovered at %d want 4", s)
	}
	if err := b.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestUnregisterRecomputes(t *testing.T) {
	b, _ := New(100, 3)
	slow, _ := b.Register()
	r2, _ := b.Register()
	for i := 0; i < 10; i++ {
		b.Append(i)
	}
	if st := b.Stats(); st.SlowestLag != 9 || st.Readers != 2 {
		t.Fatalf("stats=%+v", st)
	}
	if err := b.Unregister(slow); err != nil {
		t.Fatal(err)
	}
	if st := b.Stats(); st.SlowestLag != 9 {
		// r2 也注册在同一点，仍是 10
		t.Fatalf("slowest after remove=%d", st.SlowestLag)
	}
	if err := b.Unregister(r2); err != nil {
		t.Fatal(err)
	}
	if st := b.Stats(); st.SlowestLag != 0 || st.Readers != 0 {
		t.Fatalf("stats after all removed: %+v", st)
	}
	if err := b.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
