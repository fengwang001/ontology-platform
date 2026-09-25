package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/api"
)

func readEq(tx *api.Txn, key, want string) error {
	got, err := tx.Read(key)
	if err == nil && got != want {
		err = fmt.Errorf("read %s = %q, want %q", key, got, want)
	}
	return err
}

// TestFourteenSteps replays the NOTES.md 14-step scenario (invariant 2).
func TestFourteenSteps(t *testing.T) {
	db := api.New()
	seed := db.Begin()
	for k, v := range map[string]string{"x": "10", "y": "10", "z": "5"} {
		_ = seed.Write(k, v)
	}
	if err := seed.Commit(); err != nil {
		t.Fatal(err)
	}
	var t1, t2, t3 *api.Txn
	base := map[string]string{"x": "10", "y": "10", "z": "5"}
	fin := map[string]string{"x": "-10", "y": "10", "z": "99"}
	steps := []struct {
		name    string
		do      func() error
		wantErr error
		state   map[string]string
	}{
		{"T1.Begin", func() error { t1 = db.Begin(); return nil }, nil, base},
		{"T2.Begin", func() error { t2 = db.Begin(); return nil }, nil, base},
		{"T1.Read(x)", func() error { return readEq(t1, "x", "10") }, nil, base},
		{"T2.Read(x)", func() error { return readEq(t2, "x", "10") }, nil, base},
		{"T1.Read(y)", func() error { return readEq(t1, "y", "10") }, nil, base},
		{"T2.Read(y)", func() error { return readEq(t2, "y", "10") }, nil, base},
		{"T3.Begin", func() error { t3 = db.Begin(); return nil }, nil, base},
		{"T3.Write(z,99)", func() error { return t3.Write("z", "99") }, nil, base},
		{"T3.Read(z)=99", func() error { return readEq(t3, "z", "99") }, nil, base},
		{"T1.Write(x,-10)", func() error { return t1.Write("x", "-10") }, nil, base},
		{"T1.Commit", func() error { return t1.Commit() }, nil, map[string]string{"x": "-10", "y": "10", "z": "5"}},
		{"T3.Commit", func() error { return t3.Commit() }, nil, fin},
		{"T2.Write(y,-10)", func() error { return t2.Write("y", "-10") }, nil, fin},
		{"T2.Commit", func() error { return t2.Commit() }, api.ErrConflict, fin},
	}
	for i, s := range steps {
		if err := s.do(); !errors.Is(err, s.wantErr) {
			t.Fatalf("step %d %s = %v, want %v", i+1, s.name, err, s.wantErr)
		}
		if got := db.Committed(); !reflect.DeepEqual(got, s.state) {
			t.Fatalf("after step %d %s: state %v, want %v", i+1, s.name, got, s.state)
		}
	}
}

// TestSerialReference: state == commit-order serial replay (invariant 1).
func TestSerialReference(t *testing.T) {
	keys := []string{"k0", "k1", "k2", "k3"}
	for _, seed := range []int64{1, 2, 3} {
		db, rng, model := api.New(), rand.New(rand.NewSource(seed)), map[string]string{}
		for i := 0; i < 200; i++ {
			tx, ws := db.Begin(), map[string]string{}
			for j := 0; j < 4; j++ {
				k := keys[rng.Intn(len(keys))]
				if rng.Intn(2) == 0 {
					if _, err := tx.Read(k); err != nil {
						t.Fatal(err)
					}
				} else if v := fmt.Sprint(rng.Intn(100)); tx.Write(k, v) == nil {
					ws[k] = v
				}
			}
			switch err := tx.Commit(); {
			case err == nil:
				for k, v := range ws {
					model[k] = v
				}
			case !errors.Is(err, api.ErrConflict):
				t.Fatal(err)
			}
		}
		if got := db.Committed(); !reflect.DeepEqual(got, model) {
			t.Fatalf("seed %d: state %v != serial replay %v", seed, got, model)
		}
	}
}

// TestRejectedOpsLeaveNoTrace: distinct sentinels, no trace (invariant 4).
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	db := api.New()
	base := db.Committed()
	var zero api.Txn
	tx := db.Begin()
	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"zero.Read", func() error { _, e := zero.Read("k"); return e }, api.ErrNoActiveTxn},
		{"zero.Write", func() error { return zero.Write("k", "v") }, api.ErrNoActiveTxn},
		{"zero.Commit", zero.Commit, api.ErrNoActiveTxn},
		{"empty.Read", func() error { _, e := tx.Read(""); return e }, api.ErrEmptyKey},
		{"empty.Write", func() error { return tx.Write("", "v") }, api.ErrEmptyKey},
		{"commit", tx.Commit, nil},
		{"ended.Read", func() error { _, e := tx.Read("k"); return e }, api.ErrTxnEnded},
		{"ended.Write", func() error { return tx.Write("k", "v") }, api.ErrTxnEnded},
		{"ended.Commit", tx.Commit, api.ErrTxnEnded},
	}
	for _, c := range cases {
		if err := c.run(); !errors.Is(err, c.want) {
			t.Fatalf("%s = %v, want %v", c.name, err, c.want)
		}
		if got := db.Committed(); !reflect.DeepEqual(got, base) {
			t.Fatalf("%s changed state: %v", c.name, got)
		}
	}
	ok := db.Begin() // still usable
	if err := ok.Write("k", "v"); err != nil {
		t.Fatal(err)
	}
	if err := ok.Commit(); err != nil || db.Committed()["k"] != "v" {
		t.Fatalf("engine unusable after rejections: %v", err)
	}
}

// TestSelfCheckConcurrent: SelfCheck and Committed are goroutine-safe.
func TestSelfCheckConcurrent(t *testing.T) {
	db := api.New()
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { _ = db.Committed(); errs <- db.SelfCheck() }()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}
