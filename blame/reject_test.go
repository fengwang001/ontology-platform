package blame

import (
	"errors"
	"testing"

	"ontology/dag"
)

func TestAddDatasetRejectionOrder(t *testing.T) {
	// 参数非法 > ErrFrozen > 已存在 > 父不存在 > 超限。
	a, _ := New(100)
	if err := a.AddDataset("", 1, 0, nil); !errors.Is(err, dag.ErrInvalid) {
		t.Fatalf("empty name: %v", err)
	}
	if err := a.AddDataset("x", 0, 0, nil); !errors.Is(err, dag.ErrInvalid) {
		t.Fatalf("off<1: %v", err)
	}
	if err := a.AddDataset("x", 101, 0, nil); !errors.Is(err, dag.ErrInvalid) {
		t.Fatalf("off>T: %v", err)
	}
	if err := a.AddDataset("x", 1, -1, nil); !errors.Is(err, dag.ErrInvalid) {
		t.Fatalf("dur<0: %v", err)
	}
	if err := a.AddDataset("x", 1, 101, nil); !errors.Is(err, dag.ErrInvalid) {
		t.Fatalf("dur>T: %v", err)
	}
	mustAdd(t, a, "x", 1, 0)
	if err := a.AddDataset("y", 1, 0, []string{"x", "x"}); !errors.Is(err, dag.ErrInvalid) {
		t.Fatalf("duplicate parent: %v", err)
	}
	if err := a.AddDataset("y", 1, 0, make([]string, 9)); err == nil {
		t.Fatalf("9 parents must be invalid")
	}

	// 冻结后：参数非法仍优先于 ErrFrozen。
	if _, err := a.Evaluate(0); err != nil {
		t.Fatal(err)
	}
	if err := a.AddDataset("", 1, 0, nil); !errors.Is(err, dag.ErrInvalid) {
		t.Fatalf("invalid must beat frozen: %v", err)
	}
	if err := a.AddDataset("y", 1, 0, nil); !errors.Is(err, dag.ErrFrozen) {
		t.Fatalf("frozen: %v", err)
	}

	// 未冻结时：已存在 > 父不存在。
	b, _ := New(100)
	mustAdd(t, b, "x", 1, 0)
	if err := b.AddDataset("x", 1, 0, []string{"ghost"}); !errors.Is(err, dag.ErrExists) {
		t.Fatalf("exists must beat missing parent: %v", err)
	}
	if err := b.AddDataset("y", 1, 0, []string{"ghost"}); !errors.Is(err, dag.ErrParentNotFound) {
		t.Fatalf("missing parent: %v", err)
	}
}

func TestAddDatasetLimit(t *testing.T) {
	a, _ := New(100)
	for i := 0; i < dag.MaxDatasets; i++ {
		name := "d" + itoa64(int64(i))
		if err := a.AddDataset(name, 1, 0, nil); err != nil {
			t.Fatalf("dataset %d: %v", i, err)
		}
	}
	if err := a.AddDataset("overflow", 1, 0, nil); !errors.Is(err, dag.ErrTooManyDatasets) {
		t.Fatalf("limit: %v", err)
	}
}

func TestNewGraphInvalidT(t *testing.T) {
	for _, T := range []int64{0, -1, 1_000_001} {
		if _, err := New(T); !errors.Is(err, dag.ErrInvalid) {
			t.Fatalf("T=%d: %v", T, err)
		}
	}
}

func TestLandRejectionOrdering(t *testing.T) {
	setup := func() *Attributor {
		a, _ := New(100)
		mustAdd(t, a, "a", 20, 0)
		mustAdd(t, a, "b", 60, 10, "a")
		mustLand(t, a, "a", 0, 20)
		mustLand(t, a, "b", 0, 60)
		return a
	}

	// 参数非法最先。
	a := setup()
	if err := a.Land("", 0, 61); !errors.Is(err, dag.ErrInvalid) {
		t.Fatalf("empty name: %v", err)
	}
	if err := a.Land("a", -1, 61); !errors.Is(err, dag.ErrInvalid) {
		t.Fatalf("k<0: %v", err)
	}
	if err := a.Land("a", 0, -1); !errors.Is(err, dag.ErrInvalid) {
		t.Fatalf("now<0: %v", err)
	}

	// 时钟回退 > 数据集不存在。
	if _, err := a.Evaluate(80); err != nil {
		t.Fatal(err)
	}
	if err := a.Land("ghost", 5, 70); !errors.Is(err, dag.ErrClockRewind) {
		t.Fatalf("rewind must beat no dataset: %v", err)
	}
	// 时钟不回退但数据集不存在。
	if err := a.Land("ghost", 5, 90); !errors.Is(err, dag.ErrNoDataset) {
		t.Fatalf("no dataset: %v", err)
	}
	// 被拒绝的操作不推进时钟。
	if a.Now() != 80 {
		t.Fatalf("clock advanced on rejected land: %d", a.Now())
	}

	// ErrAlready（k<next）。
	if err := a.Land("a", 0, 90); !errors.Is(err, dag.ErrAlready) {
		t.Fatalf("already: %v", err)
	}
	// ErrOutOfOrder（k>next）优先于 TooEarly/UpstreamMissing。
	if err := a.Land("a", 5, 90); !errors.Is(err, dag.ErrOutOfOrder) {
		t.Fatalf("out of order: %v", err)
	}
	// ErrTooEarly 优先于上游缺失。
	e, _ := New(100)
	mustAdd(t, e, "r", 10, 0)
	mustLand(t, e, "r", 0, 0)
	if err := e.Land("r", 1, 50); !errors.Is(err, dag.ErrTooEarly) {
		t.Fatalf("too early: %v", err)
	}

	// ErrUpstreamMissing：b 的父 a 第 1 期未落地，且 now 合法、顺序正确。
	if err := a.Land("b", 1, 120); !errors.Is(err, dag.ErrUpstreamMissing) {
		t.Fatalf("upstream missing: %v", err)
	} else if !errors.Is(err, dag.ErrUpstreamMissing) || errMissingName(err) != "a" {
		t.Fatalf("upstream error must name parent a, got %v", err)
	}
}

func errMissingName(err error) string {
	s := err.Error()
	i := len(s)
	for j := len(s) - 1; j >= 0; j-- {
		if s[j] == ' ' {
			i = j + 1
			break
		}
	}
	return s[i:]
}

func TestEvaluateRejectionKeepsClock(t *testing.T) {
	a, _ := New(100)
	mustAdd(t, a, "a", 20, 0)
	if _, err := a.Evaluate(80); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Evaluate(70); !errors.Is(err, dag.ErrClockRewind) {
		t.Fatalf("rewind: %v", err)
	}
	if a.Now() != 80 {
		t.Fatalf("clock changed on rejected eval: %d", a.Now())
	}
}
