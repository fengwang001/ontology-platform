package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// TestDifferentialRandomOps 在大量随机操作序列上将 Service 与
// 独立维护全部标记的朴素实现逐步比对：错误、查询结果、最终快照。
func TestDifferentialRandomOps(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	svc := NewService()
	ref := NewNaiveService()

	const numObjects = 6
	props := []string{"status", "owner"}
	ids := make([]ObjectID, numObjects)
	for i := range ids {
		ids[i] = ObjectID(fmt.Sprintf("obj-%d", i))
	}
	// recordIDs[o][p] 记录已生成的记录 ID，供随机选取。
	recordIDs := map[ObjectID]map[string][]RecordID{}
	var clock int64

	checkErr := func(step int, op string, got, want error) {
		t.Helper()
		if !errors.Is(got, want) && !(got == nil && want == nil) {
			t.Fatalf("step %d %s: err mismatch got=%v want=%v", step, op, got, want)
		}
	}

	for step := 0; step < 20000; step++ {
		id := ids[rng.Intn(numObjects)]
		prop := props[rng.Intn(len(props))]
		switch rng.Intn(8) {
		case 0: // CreateObject（可能重复）
			checkErr(step, "create", svc.CreateObject(id), ref.CreateObject(id))
		case 1: // AddHistory（时间戳全局递增，保证单调）
			clock += 1 + rng.Int63n(10)
			v := fmt.Sprintf("v%d", clock)
			r1, e1 := svc.AddHistory(id, prop, v, clock)
			r2, e2 := ref.AddHistory(id, prop, v, clock)
			checkErr(step, "add", e1, e2)
			if e1 == nil && r1 != r2 {
				t.Fatalf("step %d add: record id mismatch %s vs %s", step, r1, r2)
			}
			if e1 == nil {
				if recordIDs[id] == nil {
					recordIDs[id] = map[string][]RecordID{}
				}
				recordIDs[id][prop] = append(recordIDs[id][prop], r1)
			}
		case 2: // DeleteObject
			checkErr(step, "del-obj", svc.DeleteObject(id), ref.DeleteObject(id))
		case 3: // RestoreObject
			checkErr(step, "restore", svc.RestoreObject(id), ref.RestoreObject(id))
		case 4, 5: // DeleteRecord / UndeleteRecord（随机选已有记录或伪造 ID）
			var rid RecordID
			if recs := recordIDs[id][prop]; len(recs) > 0 && rng.Intn(4) > 0 {
				rid = recs[rng.Intn(len(recs))]
			} else {
				rid = RecordID(fmt.Sprintf("bogus-%d", rng.Intn(1000)))
			}
			if rng.Intn(2) == 0 {
				checkErr(step, "del-rec", svc.DeleteRecord(id, rid), ref.DeleteRecord(id, rid))
			} else {
				checkErr(step, "undel-rec", svc.UndeleteRecord(id, rid), ref.UndeleteRecord(id, rid))
			}
		default: // QueryVisibleValue
			at := rng.Int63n(clock + 20)
			got := svc.QueryVisibleValue(id, prop, at)
			want := ref.QueryVisibleValue(id, prop, at)
			if !queryResultsEqual(got, want) {
				t.Fatalf("step %d query(%s,%s,%d):\ngot  %+v\nwant %+v", step, id, prop, at, got, want)
			}
		}
	}

	// 最终逐条比对全部标记。
	got, want := svc.Snapshot(), ref.Snapshot()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("final snapshot mismatch:\ngot  %v\nwant %v", got, want)
	}
}

func queryResultsEqual(a, b QueryResult) bool {
	return a.Visible == b.Visible && a.Value == b.Value && a.Reason == b.Reason &&
		a.Record == b.Record && errors.Is(a.Err, b.Err)
}

// TestConcurrentLinearizable 并发执行整体删除/复活/单独删除/查询，
// 然后按日志中的全局顺序在朴素实现上重放，验证：
// 1) 每条日志的输出与重放结果一致（可线性化）；
// 2) 最终标记集合与重放结果一致（无中间态泄漏）。
func TestConcurrentLinearizable(t *testing.T) {
	svc := NewService()
	const numObjects = 4
	ids := make([]ObjectID, numObjects)
	rids := make([][]RecordID, numObjects)
	for i := range ids {
		ids[i] = ObjectID(fmt.Sprintf("c-%d", i))
		if err := svc.CreateObject(ids[i]); err != nil {
			t.Fatal(err)
		}
		for h := 0; h < 3; h++ {
			rid, err := svc.AddHistory(ids[i], "p", fmt.Sprintf("v%d", h), int64(h+1))
			if err != nil {
				t.Fatal(err)
			}
			rids[i] = append(rids[i], rid)
		}
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 500; i++ {
				o := rng.Intn(numObjects)
				switch rng.Intn(5) {
				case 0:
					svc.DeleteObject(ids[o])
				case 1:
					svc.RestoreObject(ids[o])
				case 2:
					svc.DeleteRecord(ids[o], rids[o][rng.Intn(3)])
				case 3:
					svc.UndeleteRecord(ids[o], rids[o][rng.Intn(3)])
				default:
					svc.QueryVisibleValue(ids[o], "p", rng.Int63n(4))
				}
			}
		}(int64(w) + 1)
	}
	wg.Wait()

	// 按日志全局顺序重放到朴素实现，逐步核对输出。
	ref := NewNaiveService()
	for i := range ids {
		if err := ref.CreateObject(ids[i]); err != nil {
			t.Fatal(err)
		}
		for h := 0; h < 3; h++ {
			if _, err := ref.AddHistory(ids[i], "p", fmt.Sprintf("v%d", h), int64(h+1)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, e := range svc.Log() {
		switch e.Op {
		case OpCreateObject, OpAddHistory:
			continue // 前置构造阶段，已重放
		case OpDeleteObject:
			if err := ref.DeleteObject(e.ObjectID); !errors.Is(err, e.Err) {
				t.Fatalf("seq %d delete-object: replay err %v, logged %v", e.Seq, err, e.Err)
			}
		case OpRestoreObject:
			if err := ref.RestoreObject(e.ObjectID); !errors.Is(err, e.Err) {
				t.Fatalf("seq %d restore: replay err %v, logged %v", e.Seq, err, e.Err)
			}
		case OpDeleteRecord:
			if err := ref.DeleteRecord(e.ObjectID, e.RecordID); !errors.Is(err, e.Err) {
				t.Fatalf("seq %d delete-record: replay err %v, logged %v", e.Seq, err, e.Err)
			}
		case OpUndeleteRecord:
			if err := ref.UndeleteRecord(e.ObjectID, e.RecordID); !errors.Is(err, e.Err) {
				t.Fatalf("seq %d undelete-record: replay err %v, logged %v", e.Seq, err, e.Err)
			}
		case OpQueryVisibleValue:
			got := ref.QueryVisibleValue(e.ObjectID, e.Property, e.At)
			if !queryResultsEqual(got, *e.Result) {
				t.Fatalf("seq %d query: replay %+v, logged %+v", e.Seq, got, *e.Result)
			}
		}
	}
	if got, want := svc.Snapshot(), ref.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("post-concurrency snapshot mismatch:\ngot  %v\nwant %v", got, want)
	}
}
