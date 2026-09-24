package reconcile

import (
	"fmt"
	"reflect"
	"testing"

	"ontology/batch"
	"ontology/importer"
	"ontology/store"
)

func makeBatch(id string, n int) *batch.Batch {
	b := &batch.Batch{ID: id}
	for i := 0; i < n; i++ {
		b.Records = append(b.Records, batch.Record{
			Key:   fmt.Sprintf("k%06d", i),
			Value: []byte(fmt.Sprintf("v%d", i)),
		})
	}
	return b
}

func TestReconcile(t *testing.T) {
	t.Run("three segments with exact endpoints", func(t *testing.T) {
		b := makeBatch("seg", 10)
		st := store.New()
		for _, i := range []int{0, 1, 2, 5, 6, 7} {
			st.Put(b.Records[i].Key, b.Records[i].Value)
		}
		st.Put("extra-key", []byte("x"))
		rep := Reconcile(st, b, false)
		wantW := []batch.Interval{{Start: 0, End: 3}, {Start: 5, End: 8}}
		wantG := []batch.Interval{{Start: 3, End: 5}, {Start: 8, End: 10}}
		if !reflect.DeepEqual(rep.Written, wantW) || !reflect.DeepEqual(rep.Gaps, wantG) {
			t.Fatalf("written=%v gaps=%v", rep.Written, rep.Gaps)
		}
		if !reflect.DeepEqual(rep.Extra, []string{"extra-key"}) {
			t.Fatalf("extra=%v", rep.Extra)
		}
	})

	t.Run("in-flight not counted as committed", func(t *testing.T) {
		b := makeBatch("if", 10)
		st := store.New()
		st.FailOnPut(5) // 第 5 条写入失败，批次中断在途
		importer.New(st, t.TempDir(), 3).Import(b)
		rep := Reconcile(st, b, false)
		if rep.Committed != 0 || rep.InFlight != 4 {
			t.Fatalf("committed=%d inflight=%d", rep.Committed, rep.InFlight)
		}
		wantW := []batch.Interval{{Start: 0, End: 4}}
		wantG := []batch.Interval{{Start: 4, End: 10}}
		if !reflect.DeepEqual(rep.Written, wantW) || !reflect.DeepEqual(rep.Gaps, wantG) {
			t.Fatalf("written=%v gaps=%v", rep.Written, rep.Gaps)
		}
	})

	t.Run("committed batch counts as committed", func(t *testing.T) {
		b := makeBatch("cm", 10)
		st := store.New()
		importer.New(st, t.TempDir(), 3).Import(b)
		rep := Reconcile(st, b, true)
		if rep.Committed != 10 || rep.InFlight != 0 || len(rep.Gaps) != 0 {
			t.Fatalf("rep=%+v", rep)
		}
	})

	t.Run("progress ahead of store: gap detected", func(t *testing.T) {
		b := makeBatch("skew", 100)
		dir := t.TempDir()
		st1 := store.New()
		st1.FailOnPut(51)
		importer.New(st1, dir, 10).Import(b) // 进度声称 50
		st2 := store.New()
		for i := 0; i < 30; i++ { // 存储实际只有 30
			st2.Put(b.Records[i].Key, b.Records[i].Value)
		}
		rep := Reconcile(st2, b, false)
		wantG := []batch.Interval{{Start: 30, End: 100}}
		if !reflect.DeepEqual(rep.Gaps, wantG) {
			t.Fatalf("gaps=%v, want %v", rep.Gaps, wantG)
		}
	})

	t.Run("linear store accesses", func(t *testing.T) {
		b := makeBatch("lin", 1000)
		st := store.New()
		for i := 0; i < 500; i++ {
			st.Put(b.Records[i].Key, b.Records[i].Value)
		}
		st.ResetOps()
		Reconcile(st, b, false)
		if ops := st.Ops(); ops > 3*1000 {
			t.Fatalf("store ops %d > 3n", ops)
		}
	})
}
