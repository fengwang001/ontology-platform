package coord

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"ontology/ledger"
	"ontology/tcc"
)

func setup(t *testing.T, ttl, N int64, dep map[string]int64) (*Coordinator, *tcc.TCC, *ledger.Ledger, *bytes.Buffer) {
	t.Helper()
	lg := ledger.New()
	for a, x := range dep {
		if err := lg.Deposit(a, x); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	rm, err := tcc.New(lg, ttl, N, tcc.WithLogger(&buf))
	if err != nil {
		t.Fatal(err)
	}
	return New(rm, WithLogger(&buf)), rm, lg, &buf
}

// 题目协调例：a=100,b=30；1=(a,60) 成功，2=(b,50) 不足 -> Abort。
func TestBeginErrors(t *testing.T) {
	co, _, _, _ := setup(t, 10, 100, map[string]int64{"a": 100})
	if err := co.Begin("", []BranchSpec{{BR: "1", Acct: "a", Amount: 1}}, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty xid: %v", err)
	}
	brs := []BranchSpec{{BR: "1", Acct: "a", Amount: 1}}
	if err := co.Begin("x", brs, 0); err != nil {
		t.Fatal(err)
	}
	if err := co.Begin("x", brs, 0); !errors.Is(err, ErrExists) {
		t.Fatalf("dup begin: %v", err)
	}
	if err := co.Begin("y", brs, -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad now: %v", err)
	}
	if _, err := co.Run("nope", 1); !errors.Is(err, ErrNoTxn) {
		t.Fatalf("run missing: %v", err)
	}
	if _, err := co.Status("nope"); !errors.Is(err, ErrNoTxn) {
		t.Fatalf("status missing: %v", err)
	}
}

func TestClockRejected(t *testing.T) {
	co, _, _, _ := setup(t, 10, 100, map[string]int64{"a": 100})
	brs := []BranchSpec{{BR: "1", Acct: "a", Amount: 10}}
	if err := co.Begin("x", brs, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := co.Run("x", 4); !errors.Is(err, ErrClock) {
		t.Fatalf("backward run: %v", err)
	}
}

func TestLogsContainBasis(t *testing.T) {
	co, _, _, buf := setup(t, 10, 100, map[string]int64{"a": 100})
	brs := []BranchSpec{{BR: "1", Acct: "a", Amount: 60}}
	if err := co.Begin("x", brs, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := co.Run("x", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := co.Execute("x", 0); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Begin", "Run", "Commit", "exec step"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}
}
