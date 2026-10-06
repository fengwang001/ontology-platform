package idb_test

import (
	"testing"

	"ontology/idb"
)

func TestErrorRejectionOrder(t *testing.T) {
	k := testKernel(t)
	c := upgrade(t, k, "db", 1, mkStores("a"))

	if _, err := c.Transaction(idb.ReadOnly, nil); !errIsKind(err, idb.KindInvalidArgument) {
		t.Fatalf("empty scope: %v", err)
	}
	if _, err := k.Open("", 0); !errIsKind(err, idb.KindInvalidArgument) {
		t.Fatalf("empty name: %v", err)
	}
	if _, err := k.Open("dbz", -1); !errIsKind(err, idb.KindInvalidArgument) {
		t.Fatalf("negative version: %v", err)
	}

	err := idb.WithTx(c, idb.ReadWrite, []string{"a"}, func(tx *idb.Transaction) error {
		if _, e := tx.Get("", nil); !errIsKind(e, idb.KindInvalidArgument) {
			t.Fatalf("bad args: %v", e)
		}
		if _, e := tx.Get("nope", []byte("k")); !errIsKind(e, idb.KindNoObjectStore) {
			t.Fatalf("missing store: %v", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	c2 := upgrade(t, k, "db2", 1, mkStores("a", "b"))
	err = idb.WithTx(c2, idb.ReadWrite, []string{"a"}, func(tx *idb.Transaction) error {
		if _, e := tx.Put("b", []byte("k"), []byte("v"), false); !errIsKind(e, idb.KindScopeViolation) {
			t.Fatalf("scope: %v", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = idb.WithTx(c2, idb.ReadOnly, []string{"a"}, func(tx *idb.Transaction) error {
		if _, e := tx.Put("a", []byte("k"), []byte("v"), false); !errIsKind(e, idb.KindInvalidState) {
			t.Fatalf("read-only write: %v", e)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	tx, _ := c2.Transaction(idb.ReadOnly, []string{"a"})
	_ = tx.Wait()
	waitState(t, tx, idb.Committed)
	if _, e := tx.Get("a", []byte("k")); !errIsKind(e, idb.KindTxInactive) {
		t.Fatalf("request after finish: %v", e)
	}

	if e := c2.CreateObjectStore("x"); !errIsKind(e, idb.KindInvalidState) {
		t.Fatalf("create outside vc: %v", e)
	}
	if e := c2.DeleteObjectStore("a"); !errIsKind(e, idb.KindInvalidState) {
		t.Fatalf("delete outside vc: %v", e)
	}
}
