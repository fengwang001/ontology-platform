package ontology

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestValidationAndLimitOrder(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("", 0, 0, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty name error = %v", err)
	}
	if err := r.Register(strings.Repeat("n", maxNameBytes+1), 0, 0, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("long name error = %v", err)
	}
	if err := r.Register("k", -1, 0, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative k error = %v", err)
	}
	if err := r.Register("k", maxParameters+1, maxParameters+1, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("large k error = %v", err)
	}
	if err := r.Register("v", 1, 0, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("v<k error = %v", err)
	}
	if err := r.Register("v", 0, maxVariables+1, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("large v error = %v", err)
	}
	if err := r.Register("badkind", 0, 0, []Statement{{Kind: StmtKind(99)}}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad kind error = %v", err)
	}
	if err := r.Register("badd", 0, 1, []Statement{{Kind: NewStmt, D: -1}}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("new d=-1 error = %v", err)
	}
	if err := r.Register("badvar", 0, 1, []Statement{{Kind: CopyStmt, D: 0, S: 1}}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("variable out of range error = %v", err)
	}
	if err := r.Register("badargs", 0, 1, []Statement{{Kind: CallStmt, D: -1, G: "x", Args: make([]int, maxCallArgs+1)}}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("too many args error = %v", err)
	}

	mustRegister(t, r, "exists", 0, 0, nil)
	if err := r.Register("exists", 0, 0, nil); !errors.Is(err, ErrNameRegistered) {
		t.Fatalf("duplicate error = %v", err)
	}

	for i := len(r.order); i < maxFunctions; i++ {
		mustRegister(t, r, fmt.Sprintf("f%03d", i), 0, 0, nil)
	}
	if err := r.Register("over", 0, 0, nil); !errors.Is(err, ErrFunctionLimit) {
		t.Fatalf("limit error = %v, want ErrFunctionLimit", err)
	}
}

func TestSemanticErrorOrderAndNoSiteGap(t *testing.T) {
	r := NewRegistry()
	mustRegister(t, r, "one", 0, 1, []Statement{{Kind: NewStmt, D: 0}})

	err := r.Register("bad", 0, 1, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: CallStmt, D: -1, G: "missing", Args: nil},
	})
	if !errors.Is(err, ErrUnknownCallee) {
		t.Fatalf("unknown callee error = %v", err)
	}
	if r.siteCount != 1 {
		t.Fatalf("rejected register changed site count to %d", r.siteCount)
	}

	mustRegister(t, r, "twoarg", 2, 2, nil)
	err = r.Register("badcount", 0, 2, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: CallStmt, D: -1, G: "twoarg", Args: []int{0}},
	})
	if !errors.Is(err, ErrArgumentCount) {
		t.Fatalf("argument count error = %v", err)
	}
	if r.siteCount != 1 {
		t.Fatalf("argument-count rejection changed site count to %d", r.siteCount)
	}

	err = r.Register("ordered", 0, 2, []Statement{
		{Kind: CallStmt, D: -1, G: "twoarg", Args: []int{0}},
		{Kind: CallStmt, D: -1, G: "missing", Args: nil},
	})
	if !errors.Is(err, ErrArgumentCount) {
		t.Fatalf("earlier argument-count error = %v, want ErrArgumentCount", err)
	}

	mustRegister(t, r, "ok", 0, 1, []Statement{{Kind: NewStmt, D: 0}})
	sites, err := r.Sites("ok")
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 || sites[0].ID != 2 {
		t.Fatalf("sites after rejections = %#v, want contiguous ID 2", sites)
	}
}

func TestAllStatementAndCallLimits(t *testing.T) {
	r := NewRegistry()
	stmts := make([]Statement, maxStatements)
	for i := range stmts {
		stmts[i] = Statement{Kind: NewStmt, D: 0}
	}
	mustRegister(t, r, "maxstmts", 0, 1, stmts)

	args := make([]int, maxCallArgs)
	for i := range args {
		args[i] = i
	}
	mustRegister(t, r, "maxcallee", maxParameters, maxVariables, nil)
	mustRegister(t, r, "maxcall", 0, maxVariables, []Statement{
		{Kind: CallStmt, D: -1, G: "maxcallee", Args: args},
	})

	err := r.Register("toomanystmts", 0, 1, append(stmts, Statement{Kind: NewStmt, D: 0}))
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("too many statements error = %v", err)
	}
	err = r.Register("toomanycallargs", maxParameters+1, maxParameters+1, []Statement{
		{Kind: CallStmt, D: -1, G: "maxcallee", Args: append(args, 0)},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("too many call args error = %v", err)
	}
}

func TestAnalysisRunCounterMatchesRounds(t *testing.T) {
	r := NewRegistry()
	before := r.analysisRuns
	mustRegister(t, r, "plain", 0, 1, []Statement{
		{Kind: NewStmt, D: 0},
		{Kind: RetStmt, S: 0},
	})
	if r.analysisRuns-before != 1 {
		t.Fatalf("non-recursive runs = %d, want 1", r.analysisRuns-before)
	}

	before = r.analysisRuns
	mustRegister(t, r, "rec", 2, 2, []Statement{
		{Kind: CallStmt, D: -1, G: "rec", Args: []int{1, 0}},
		{Kind: GlobalStmt, S: 0},
	})
	summary, err := r.Summary("rec")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.analysisRuns - before; got != int64(summary.Rounds) {
		t.Fatalf("analysis runs = %d, Summary.Rounds = %d", got, summary.Rounds)
	}
	if summary.Rounds != 3 {
		t.Fatalf("rec rounds = %d, want 3", summary.Rounds)
	}

	k := maxParameters
	limit := 2*k + k*k + 2
	if summary.Rounds > limit {
		t.Fatalf("rounds %d exceed bound %d", summary.Rounds, limit)
	}
}

func TestConcurrentRegistrationAndQueries(t *testing.T) {
	r := NewRegistry()
	const workers = 32
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			name := fmt.Sprintf("worker-%02d", worker)
			if err := r.Register(name, 0, 1, []Statement{
				{Kind: NewStmt, D: 0},
				{Kind: RetStmt, S: 0},
			}); err != nil {
				t.Errorf("Register(%s): %v", name, err)
				return
			}
			if _, err := r.Summary(name); err != nil {
				t.Errorf("Summary(%s): %v", name, err)
			}
			if _, err := r.Sites(name); err != nil {
				t.Errorf("Sites(%s): %v", name, err)
			}
		}(worker)
	}
	wg.Wait()

	if len(r.order) != workers {
		t.Fatalf("registered %d functions, want %d", len(r.order), workers)
	}
	if r.siteCount != workers {
		t.Fatalf("site count = %d, want %d", r.siteCount, workers)
	}
	seen := make(map[int]bool)
	for _, fn := range r.order {
		sites, err := r.Sites(fn.name)
		if err != nil {
			t.Fatal(err)
		}
		if len(sites) != 1 || sites[0].ID < 1 || sites[0].ID > workers || seen[sites[0].ID] {
			t.Fatalf("invalid sites for %s: %#v", fn.name, sites)
		}
		seen[sites[0].ID] = true
	}
}
