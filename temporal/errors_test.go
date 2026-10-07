package temporal

import "testing"

func buildErrorFixture(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	tx := s.Begin()
	tx.CreateObjectType("T", []Property{{Name: "p", Type: "int"}})
	tx.CreateLinkType("l", Cardinality{MaxOut: -1})
	mustCommit(t, tx)
	tx = s.Begin()
	tx.CreateObject("root", "T", PropertyValues{"p": 1})
	tx.CreateObject("mid", "T", PropertyValues{"p": 2})
	tx.CreateObject("leaf", "T", PropertyValues{"p": 3})
	mustCommit(t, tx)
	tx = s.Begin()
	mustOK(t, tx.CreateLink("l", "root", "mid"))
	mustCommit(t, tx)
	tx = s.Begin()
	mustOK(t, tx.CreateLink("l", "mid", "leaf"))
	mustCommit(t, tx)
	return s
}

func TestErrorStartNotFound(t *testing.T) {
	s := buildErrorFixture(t)

	// Object never created.
	_, err := s.Traverse(TraversalConfig{Start: "ghost", At: 4,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}}, nil)
	if errCode(err) != ErrStartNotFound {
		t.Fatalf("want ErrStartNotFound, got %v", err)
	}

	// Object created later: absent at instant 1.
	_, err = s.Traverse(TraversalConfig{Start: "root", At: 1,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}}, nil)
	if errCode(err) != ErrStartNotFound {
		t.Fatalf("want ErrStartNotFound for future object, got %v", err)
	}

	// Object deleted before the baseline.
	tx := s.Begin()
	tx.DeleteObject("leaf")
	mustCommit(t, tx)
	_, err = s.Traverse(TraversalConfig{Start: "leaf", At: 5,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}}, nil)
	if errCode(err) != ErrStartNotFound {
		t.Fatalf("want ErrStartNotFound after delete, got %v", err)
	}
}

func TestErrorBeforeHorizon(t *testing.T) {
	s := buildErrorFixture(t)
	s.SetRetainHorizon(3)

	_, err := s.Traverse(TraversalConfig{Start: "root", At: 2,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}}, nil)
	if errCode(err) != ErrBeforeHorizon {
		t.Fatalf("want ErrBeforeHorizon, got %v", err)
	}

	// Horizon coexists with an absent start: horizon wins because the
	// snapshot cannot even be pinned.
	_, err = s.Traverse(TraversalConfig{Start: "ghost", At: 1,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}}, nil)
	if errCode(err) != ErrBeforeHorizon {
		t.Fatalf("horizon must take priority over start-not-found, got %v", err)
	}

	// At the horizon boundary the snapshot is replayable.
	res, err := s.Traverse(TraversalConfig{Start: "root", At: 3,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}}, nil)
	if err != nil || len(res.Objects) != 2 {
		t.Fatalf("at-boundary traversal wrong: res=%v err=%v", res, err)
	}

	// Failed traversals must not mutate history: head is unchanged and an
	// older timeline below horizon is simply unreadable, not rewritten.
	if s.Head() != 4 {
		t.Fatalf("head changed after failed reads: %d", s.Head())
	}
}

func TestErrorLimitExceeded(t *testing.T) {
	s := buildErrorFixture(t)

	_, err := s.Traverse(TraversalConfig{Start: "root", At: 4,
		Limits: TraversalLimits{MaxDepth: 0, MaxVisited: -1}}, nil)
	if errCode(err) != ErrLimitExceeded {
		t.Fatalf("want depth limit error, got %v", err)
	}

	_, err = s.Traverse(TraversalConfig{Start: "root", At: 4,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: 2}}, nil)
	if errCode(err) != ErrLimitExceeded {
		t.Fatalf("want visited limit error (3 objects > 2), got %v", err)
	}

	// Exactly at the budget is allowed.
	res, err := s.Traverse(TraversalConfig{Start: "root", At: 4,
		Limits: TraversalLimits{MaxDepth: 2, MaxVisited: 3}}, nil)
	if err != nil || len(res.Objects) != 3 {
		t.Fatalf("boundary budget traversal wrong: %v %v", res, err)
	}

	// MaxDepth=1 reaches root+mid only.
	res, err = s.Traverse(TraversalConfig{Start: "root", At: 4,
		Limits: TraversalLimits{MaxDepth: 1, MaxVisited: -1}}, nil)
	if err != nil || len(res.Objects) != 2 {
		t.Fatalf("MaxDepth=1 traversal wrong: %v %v", res, err)
	}

	// MaxVisited=0: start itself exceeds the budget; start existence is
	// confirmed first, then the limit class is reported.
	_, err = s.Traverse(TraversalConfig{Start: "root", At: 4,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: 0}}, nil)
	if errCode(err) != ErrLimitExceeded {
		t.Fatalf("want limit error for MaxVisited=0, got %v", err)
	}
}

func TestErrorMissingHistory(t *testing.T) {
	s := buildErrorFixture(t)

	// Gap on the mid object's timeline covering baseline 4: the expansion
	// reaches mid and cannot determine its state.
	s.AddHistoryGap(Gap{From: 4, To: 4, Kind: GapObject, Key: "mid"})
	_, err := s.Traverse(TraversalConfig{Start: "root", At: 4,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}}, nil)
	if errCode(err) != ErrMissingHistory {
		t.Fatalf("want ErrMissingHistory, got %v", err)
	}

	// Gap on the edge decision itself.
	s2 := buildErrorFixture(t)
	s2.AddHistoryGap(Gap{From: 4, To: 4, Kind: GapEdge})
	_, err = s2.Traverse(TraversalConfig{Start: "root", At: 4,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}}, nil)
	if errCode(err) != ErrMissingHistory {
		t.Fatalf("want ErrMissingHistory for edge gap, got %v", err)
	}

	// Gap on adjacency index.
	s3 := buildErrorFixture(t)
	s3.AddHistoryGap(Gap{From: 4, To: 4, Kind: GapAdjacency, Key: "root"})
	_, err = s3.Traverse(TraversalConfig{Start: "root", At: 4,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}}, nil)
	if errCode(err) != ErrMissingHistory {
		t.Fatalf("want ErrMissingHistory for adjacency gap, got %v", err)
	}

	// Missing history and limit exceeded coexisting: the budget is checked
	// as soon as the offending hop is decided, before any further history is
	// touched, so the limit class wins deterministically.
	s4 := buildErrorFixture(t)
	s4.AddHistoryGap(Gap{From: 4, To: 4, Kind: GapObject, Key: "leaf"})
	_, err = s4.Traverse(TraversalConfig{Start: "root", At: 4,
		Limits: TraversalLimits{MaxDepth: 0, MaxVisited: -1}}, nil)
	if errCode(err) != ErrLimitExceeded {
		t.Fatalf("limit must be reported before a deeper missing slab, got %v", err)
	}

	// None of the failed traversals mutated history.
	if s.Head() != 4 || s2.Head() != 4 || s3.Head() != 4 || s4.Head() != 4 {
		t.Fatalf("failed traversal changed head")
	}
}

func TestNoPartialResultOnError(t *testing.T) {
	s := buildErrorFixture(t)
	s.AddHistoryGap(Gap{From: 4, To: 4, Kind: GapObject, Key: "leaf"})
	res, err := s.Traverse(TraversalConfig{Start: "root", At: 4,
		Limits: TraversalLimits{MaxDepth: -1, MaxVisited: -1}}, nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if res != nil {
		t.Fatalf("must not return a partial snapshot, got %+v", res)
	}
}
