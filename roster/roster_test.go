package roster_test

import (
	"errors"
	"testing"

	"ontology/roster"
)

func bs(s string) []byte { return []byte(s) }

func TestAddTenant(t *testing.T) {
	r := roster.New()
	cases := []struct {
		name string
		n    int
		err  error
	}{
		{"t1", 1, nil},
		{"", 1, roster.ErrInvalid},
		{"t2", 0, roster.ErrInvalid},
		{"t3", 1_000_001, roster.ErrInvalid},
		{"t1", 1, roster.ErrInvalid}, // 重复租户
	}
	for _, c := range cases {
		if got := r.AddTenant(c.name, c.n); !errors.Is(got, c.err) {
			t.Fatalf("AddTenant(%q,%d)=%v want %v", c.name, c.n, got, c.err)
		}
	}
}

func TestRegisterBatchValidation(t *testing.T) {
	r := roster.New()
	if err := r.AddTenant("t", 3); err != nil {
		t.Fatal(err)
	}
	mk := func(n int) [][]byte {
		out := make([][]byte, n)
		for i := range out {
			out[i] = bs("sn" + string(rune('a'+i%26)))
		}
		return out
	}
	cases := []struct {
		name   string
		tenant string
		until  int64
		sns    [][]byte
		now    int64
		err    error
	}{
		{"empty", "t", 100, nil, 0, roster.ErrInvalid},
		{"too many", "t", 100, mk(10_001), 0, roster.ErrInvalid},
		{"bad until", "t", 1_000_000_000_001, mk(1), 0, roster.ErrInvalid},
		{"bad now", "t", 100, mk(1), -1, roster.ErrInvalid},
		{"empty sn", "t", 100, [][]byte{bs("")}, 0, roster.ErrInvalid},
		{"long sn", "t", 100, [][]byte{make([]byte, 65)}, 0, roster.ErrInvalid},
		{"unknown tenant", "nope", 100, mk(1), 0, roster.ErrUnknownTenant},
	}
	for _, c := range cases {
		if got := r.RegisterBatch(c.name, c.tenant, c.until, c.sns, c.now); !errors.Is(got, c.err) {
			t.Fatalf("%s: got %v want %v", c.name, got, c.err)
		}
	}
}

func TestRegisterBatchDuplicates(t *testing.T) {
	t.Run("in-batch later index", func(t *testing.T) {
		r := roster.New()
		_ = r.AddTenant("t", 10)
		// A,B,A：批内重复以后一次出现的下标计 -> 2
		err := r.RegisterBatch("b", "t", 100, [][]byte{bs("A"), bs("B"), bs("A")}, 0)
		var dup *roster.DupSnError
		if !errors.As(err, &dup) || dup.Idx != 2 {
			t.Fatalf("got %v want DupSn{2}", err)
		}
		if r.Get(bs("A")) != nil || r.Get(bs("B")) != nil {
			t.Fatal("rejected batch left traces")
		}
	})
	t.Run("min index across causes", func(t *testing.T) {
		r := roster.New()
		_ = r.AddTenant("t", 10)
		if err := r.RegisterBatch("b1", "t", 100, [][]byte{bs("X")}, 0); err != nil {
			t.Fatal(err)
		}
		// A,A(重复下标1),X(与已登记重复下标2)：最小下标=1
		err := r.RegisterBatch("b2", "t", 100, [][]byte{bs("A"), bs("A"), bs("X")}, 5)
		var dup *roster.DupSnError
		if !errors.As(err, &dup) || dup.Idx != 1 {
			t.Fatalf("got %v want DupSn{1}", err)
		}
		if r.Get(bs("A")) != nil {
			t.Fatal("rejected batch left traces")
		}
	})
	t.Run("existing sn index", func(t *testing.T) {
		r := roster.New()
		_ = r.AddTenant("t", 10)
		_ = r.RegisterBatch("b1", "t", 100, [][]byte{bs("A"), bs("B")}, 0)
		err := r.RegisterBatch("b2", "t", 100, [][]byte{bs("C"), bs("A")}, 5)
		var dup *roster.DupSnError
		if !errors.As(err, &dup) || dup.Idx != 1 {
			t.Fatalf("got %v want DupSn{1}", err)
		}
		if r.Get(bs("C")) != nil {
			t.Fatal("rejected batch left traces")
		}
	})
	t.Run("no trace clock", func(t *testing.T) {
		r := roster.New()
		_ = r.AddTenant("t", 10)
		_ = r.RegisterBatch("b1", "t", 100, [][]byte{bs("A")}, 7)
		// 重复拒绝不应推进时钟，也不应留痕迹；用 now=8（不回退）仍得到 DupSn。
		if err := r.RegisterBatch("b2", "t", 100, [][]byte{bs("A"), bs("A")}, 8); !errors.Is(err, roster.ErrDupSn) {
			t.Fatalf("got %v want DupSn", err)
		}
	})
}

func TestRegisterBatchProbeBound(t *testing.T) {
	r := roster.New()
	_ = r.AddTenant("t", 1_000_000)
	sns := make([][]byte, 50)
	for i := range sns {
		sns[i] = bs(string(rune('a'+i)) + "-x")
	}
	if err := r.RegisterBatch("b", "t", 100, sns, 0); err != nil {
		t.Fatal(err)
	}
	if p := r.Probes(); p > 2*len(sns) {
		t.Fatalf("RegisterBatch probes=%d > %d", p, 2*len(sns))
	}
}
