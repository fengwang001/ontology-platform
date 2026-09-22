package version

import (
	"bytes"
	"errors"
	"testing"

	"ontology/txid"
)

func TestAppendAndFindBySnapshot(t *testing.T) {
	c := NewChain(0)
	vals := [][]byte{[]byte("v1"), []byte("v2"), []byte("v3")}
	for i, v := range vals {
		if err := c.Append(txid.TxID(i+1), v, false); err != nil {
			t.Fatal(err)
		}
}
	if c.Len() != 3 {
		t.Fatalf("len = %d, want 3", c.Len())
	}

	// 快照点在 tx2：只可见 commit < 2 的版本。
	got, ok := c.Find(func(x txid.TxID) bool { return x < 2 }, 0)
	if !ok || !bytes.Equal(got.Value(), []byte("v1")) {
		t.Fatalf("snapshot@2 got %v,%v", got.Value(), ok)
	}
	// 快照点在 100：可见最新版本 v3。
	got, ok = c.Find(func(x txid.TxID) bool { return x < 100 }, 0)
	if !ok || !bytes.Equal(got.Value(), []byte("v3")) {
		t.Fatalf("snapshot@100 got %v,%v", got.Value(), ok)
	}

	// 调用方在 Append 后篡改原切片，存储不受影响（防御性拷贝）。
	mut := []byte("orig")
	if err := c.Append(4, mut, false); err != nil {
		t.Fatal(err)
	}
	mut[0] = 'X'
	got, _ = c.Find(func(x txid.TxID) bool { return x == 4 }, 0)
	if string(got.Value()) != "orig" {
		t.Fatalf("stored value mutated: %q", got.Value())
	}
}

func TestSelfWriteVisible(t *testing.T) {
	c := NewChain(0)
	if err := c.Append(1, []byte("committed"), false); err != nil {
		t.Fatal(err)
	}
	// 未提交版本不会进链；这里模拟已在链上但对公共快照不可见的高号版本。
	if err := c.Append(99, []byte("mine"), false); err != nil {
		t.Fatal(err)
	}
	// 他人快照（看不到 99）读到 v1。
	got, ok := c.Find(func(x txid.TxID) bool { return x < 50 }, 0)
	if !ok || string(got.Value()) != "committed" {
		t.Fatalf("other snapshot got %q,%v", got.Value(), ok)
	}
	// 自己读到自己的最新版本（读己之写）。
	got, ok = c.Find(func(x txid.TxID) bool { return x < 50 }, 99)
	if !ok || string(got.Value()) != "mine" {
		t.Fatalf("self read got %q,%v", got.Value(), ok)
	}
}

func TestDeleteMarker(t *testing.T) {
	c := NewChain(0)
	_ = c.Append(1, []byte("v1"), false)
	_ = c.Append(2, nil, true)
	// 新快照看到删除标记：ok 为真、Deleted 为真、值为 nil。
	got, ok := c.Find(func(x txid.TxID) bool { return x < 10 }, 0)
	if !ok || !got.Deleted() || got.Value() != nil {
		t.Fatalf("delete marker got ok=%v deleted=%v value=%v", ok, got.Deleted(), got.Value())
	}
	// 旧快照仍看到删除之前的值。
	got, ok = c.Find(func(x txid.TxID) bool { return x < 2 }, 0)
	if !ok || got.Deleted() || string(got.Value()) != "v1" {
		t.Fatalf("old snapshot got %v deleted=%v", got.Value(), got.Deleted())
	}
	// 空链：从未存在，ok 为假，可与删除标记区分。
	empty := NewChain(0)
	if _, ok := empty.Find(func(txid.TxID) bool { return true }, 0); ok {
		t.Fatal("empty chain must not report ok")
	}
}

func TestChainLimitRejectsWithoutChange(t *testing.T) {
	c := NewChain(2)
	if err := c.Append(1, []byte("a"), false); err != nil {
		t.Fatal(err)
	}
	if err := c.Append(2, []byte("b"), false); err != nil {
		t.Fatal(err)
	}
	err := c.Append(3, []byte("c"), false)
	if !errors.Is(err, ErrChainTooLong) {
		t.Fatalf("err = %v, want ErrChainTooLong", err)
	}
	if c.Len() != 2 {
		t.Fatalf("len after rejected append = %d, want 2", c.Len())
	}
}
