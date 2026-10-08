package ontology

import (
	"reflect"
	"sync"
	"testing"
)

// TestConcurrentQueriesDeterministic: many goroutines issuing the same
// queries must all observe the serially computed results.
func TestConcurrentQueriesDeterministic(t *testing.T) {
	s := newTestStore(t)
	addTypes(t, s, baseTypes()...)
	addObjs(t, s, "s", "m1", "m2", "e")
	addLinks(t, s,
		[4]string{"l1", "LA", "s", "m1"},
		[4]string{"l2", "LB", "m1", "e"},
		[4]string{"l3", "LA", "s", "m2"},
		[4]string{"l4", "LB", "m2", "e"},
	)
	queries := []Query{
		{Start: "s", End: "e", Principal: "root", Constraint: []ConstraintPos{catPos("a"), catPos("b")}},
		{Start: "s", End: "e", Principal: "root", Constraint: []ConstraintPos{anyStar(), catPos("b")}},
		{Start: "e", End: "s", Principal: "root", Constraint: []ConstraintPos{anyPos()}},
	}
	want := make([]Result, len(queries))
	for i, q := range queries {
		res, err := s.Query(q)
		if err != nil {
			t.Fatalf("serial query %d: %v", i, err)
		}
		want[i] = res
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iter := 0; iter < 200; iter++ {
				for i, q := range queries {
					res, err := s.Query(q)
					if err != nil {
						t.Errorf("query %d: %v", i, err)
						return
					}
					if !reflect.DeepEqual(res, want[i]) {
						t.Errorf("query %d: got %+v, want %+v", i, res, want[i])
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}

// TestConcurrentMutationSnapshot: mutations in a disjoint region must
// never affect queries on a fixed region; every query observes one
// complete snapshot.
func TestConcurrentMutationSnapshot(t *testing.T) {
	s := newTestStore(t)
	addTypes(t, s, baseTypes()...)
	addObjs(t, s, "s", "m", "e")
	addLinks(t, s,
		[4]string{"l1", "LA", "s", "m"},
		[4]string{"l2", "LB", "m", "e"},
	)
	q := Query{Start: "s", End: "e", Principal: "root", Constraint: []ConstraintPos{catPos("a"), catPos("b")}}
	want, err := s.Query(q)
	if err != nil || !want.Found {
		t.Fatalf("baseline: %+v %v", want, err)
	}

	stop := make(chan struct{})
	var mutWg, queryWg sync.WaitGroup
	// Mutators: add/remove links and toggle isolation in a disjoint
	// region of the graph.
	for g := 0; g < 4; g++ {
		mutWg.Add(1)
		go func(g int) {
			defer mutWg.Done()
			obj := ObjectID("junk" + string(rune('a'+g)))
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				_ = s.AddObject(obj, "t0")
				_ = s.SetIsolation(obj, i%2 == 0)
				lid := LinkID("junk-link")
				_ = s.AddLink(lid, "LA", "s", obj)
				_ = s.RemoveLink(lid)
				_ = s.RemoveObject(obj)
			}
		}(g)
	}
	for g := 0; g < 8; g++ {
		queryWg.Add(1)
		go func() {
			defer queryWg.Done()
			for i := 0; i < 500; i++ {
				res, err := s.Query(q)
				if err != nil {
					t.Errorf("query: %v", err)
					return
				}
				if !reflect.DeepEqual(res, want) {
					t.Errorf("got %+v, want %+v", res, want)
					return
				}
			}
		}()
	}
	queryWg.Wait()
	close(stop)
	mutWg.Wait()
}

// TestConcurrentIsolationLinearizable: queries concurrent with isolation
// flips of an intermediate node must always return one of the two
// serially reachable outcomes, never a mixed or corrupted one.
func TestConcurrentIsolationLinearizable(t *testing.T) {
	s := newTestStore(t)
	addTypes(t, s, baseTypes()...)
	addObjs(t, s, "s", "m", "n", "e")
	addLinks(t, s,
		[4]string{"l1", "LA", "s", "m"},
		[4]string{"l2", "LB", "m", "e"},
		[4]string{"l3", "LA5", "s", "n"},
		[4]string{"l4", "LB5", "n", "e"},
	)
	q := Query{Start: "s", End: "e", Principal: "root", Constraint: []ConstraintPos{catPos("a"), catPos("b")}}
	viaM, err := s.Query(q)
	if err != nil || !viaM.Found || viaM.Path.TotalCost != 2 {
		t.Fatalf("baseline viaM: %+v %v", viaM, err)
	}
	if err := s.SetIsolation("m", true); err != nil {
		t.Fatal(err)
	}
	viaN, err := s.Query(q)
	if err != nil || !viaN.Found || viaN.Path.TotalCost != 10 {
		t.Fatalf("baseline viaN: %+v %v", viaN, err)
	}

	stop := make(chan struct{})
	var mutWg, queryWg sync.WaitGroup
	for g := 0; g < 2; g++ {
		mutWg.Add(1)
		go func(g int) {
			defer mutWg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				_ = s.SetIsolation("m", (i+g)%2 == 0)
			}
		}(g)
	}
	for g := 0; g < 8; g++ {
		queryWg.Add(1)
		go func() {
			defer queryWg.Done()
			for i := 0; i < 1000; i++ {
				res, err := s.Query(q)
				if err != nil {
					t.Errorf("query: %v", err)
					return
				}
				if !reflect.DeepEqual(res, viaM) && !reflect.DeepEqual(res, viaN) {
					t.Errorf("result matches no serial outcome: %+v", res)
					return
				}
			}
		}()
	}
	queryWg.Wait()
	close(stop)
	mutWg.Wait()
}
