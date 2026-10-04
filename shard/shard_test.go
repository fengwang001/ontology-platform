package shard

import (
	"errors"
	"testing"
)

func TestReserveAndAdopt(t *testing.T) {
	r := NewRegistry()
	if _, err := r.NewShard("", 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("empty id: %v", err)
	}
	if _, err := r.NewShard("1", 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("cap 0: %v", err)
	}
	sh, err := r.NewShard("1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AddResident("1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Reserve("1"); err != nil {
		t.Fatal(err)
	}
	if sh.Load() != 2 {
		t.Fatalf("load=%d want 2", sh.Load())
	}
	// 已达 cap：既不能再预留也不能再增常驻。
	if err := r.Reserve("1"); !errors.Is(err, ErrCapReached) {
		t.Fatalf("reserve over cap: %v", err)
	}
	// 预留转为常驻：reserved 减、residents 增，负载不变。
	r.AdoptReserved("1")
	if sh.Residents() != 2 || sh.Reserved() != 0 || sh.Load() != 2 {
		t.Fatalf("after adopt: residents=%d reserved=%d load=%d",
			sh.Residents(), sh.Reserved(), sh.Load())
	}
	if err := r.AddResident("1"); !errors.Is(err, ErrCapReached) {
		t.Fatalf("add over cap: %v", err)
	}
	// 离开一人后可再进入。
	if err := r.RemoveResident("1"); err != nil {
		t.Fatal(err)
	}
	if sh.Load() != 1 {
		t.Fatalf("load after leave=%d", sh.Load())
	}
	// 不存在的服。
	if err := r.Reserve("nope"); !errors.Is(err, ErrNoShard) {
		t.Fatalf("reserve missing: %v", err)
	}
	// 重复建服报错。
	if _, err := r.NewShard("1", 2); !errors.Is(err, ErrShardExists) {
		t.Fatalf("dup shard: %v", err)
	}
}
