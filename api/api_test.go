package api_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/api"
	"ontology/ast"
)

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentRoundTrip(t *testing.T) {
	batch := []*ast.Expr{
		ast.Bin(ast.OpAdd, ast.Int(258), ast.Name("ab")),
		ast.IfElse(ast.Bin(ast.OpGe, ast.Name("x"), ast.Int(3)),
			ast.NotOf(ast.Bool(false)), ast.NegOf(ast.Int(-7))),
		ast.Name("a\x00b\xff"),
		ast.Int(4294967296),
	}
	const goroutines = 32
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				n := batch[(seed+j)%len(batch)]
				got, err := api.RoundTrip(n)
				if err != nil {
					errs <- err
					return
				}
				if !ast.Equal(got, n) {
					errs <- fmt.Errorf("goroutine %d: round-trip not equal", seed)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestRejectedThenOK(t *testing.T) {
	bad := [][]byte{
		api.EncodeExpr(ast.Int(1))[:4], // truncated
		{7},                            // bad tag
		{1, 2},                         // bool byte not 0/1
		{2, 9, 0, 0, 0},                // var length exceeds remainder
	}
	for i, b := range bad {
		if n, err := api.DecodeExpr(b); err == nil || n != nil {
			t.Fatalf("bad input %d: accepted", i)
		}
	}
	n := ast.Bin(ast.OpAdd, ast.Int(258), ast.Name("ab"))
	got, err := api.RoundTrip(n)
	if err != nil {
		t.Fatalf("after rejections: %v", err)
	}
	if !ast.Equal(got, n) {
		t.Fatal("after rejections: round-trip not equal")
	}
}
