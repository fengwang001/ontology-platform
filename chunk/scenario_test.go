package chunk

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/wal"
)

func U(k int64, v string) wal.Entry { return wal.Entry{Op: wal.Upsert, Key: k, Val: v} }
func D(k int64) wal.Entry           { return wal.Entry{Op: wal.Delete, Key: k} }

func me(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}

// scenario 随机交错 8 个不相交 chunk 的三步与 Poll，逐条核验不变量 1/2/3。
func scenario(t *testing.T, seed int64) {
	t.Helper()
	rng, lg := rand.New(rand.NewSource(seed)), wal.New()
	c := New(lg, 1<<20)
	cur := map[int64]string{}
	got := map[int64][]Out{}
	batch := func() []wal.Entry {
		var es []wal.Entry
		for n := rng.Intn(5); n > 0; n-- {
			k := int64(rng.Intn(1000)) // 0..799 在 chunk 内，800..999 在所有 chunk 外
			if rng.Intn(2) == 0 {
				es = append(es, U(k, fmt.Sprint(rng.Intn(4))))
			} else {
				es = append(es, D(k))
			}
		}
		return es
	}
	flush := func(os []Out) {
		for _, o := range os {
			got[o.Key] = append(got[o.Key], o)
			if o.Op == wal.Upsert {
				cur[o.Key] = o.Val
				continue
			}
			if _, ok := cur[o.Key]; !ok { // 不变量2：删除时键必存在
				t.Fatalf("inv2 delete missing %d", o.Key)
			}
			delete(cur, o.Key)
		}
		if !reflect.DeepEqual(cur, c.View()) { // 每个前缀回放后等于当前视图
			t.Fatal("inv2 prefix mismatch")
		}
	}
	for ch := 0; ch < 8; ch++ {
		lo := int64(ch * 100)
		lg.Append(batch())
		me(t, c.BeginChunk(lo, lo+100))
		lg.Append(batch())
		me(t, c.ReadChunk())
		lg.Append(batch())
		os, e := c.EndChunk()
		me(t, e)
		flush(os)
		lg.Append(batch())
		os, e = c.Poll()
		me(t, e)
		flush(os)
	}
	os, _ := c.Poll()
	flush(os)
	if !reflect.DeepEqual(lg.Snapshot(0, 800), c.View()) { // 不变量1
		t.Fatal("inv1 view != source within completed chunks")
	}
	all := lg.Slice(0, lg.Position()) // 不变量3：H 时刻那条后接 LSN>H 各次写入
	for k, seq := range got {
		var rg region
		for _, r := range c.done {
			if k >= r.lo && k < r.hi {
				rg = r
			}
		}
		st := map[int64]string{}
		for _, e := range all[:int(rg.h)] {
			if e.Key == k {
				if e.Op == wal.Upsert {
					st[k] = e.Val
				} else {
					delete(st, k)
				}
			}
		}
		exp := []Out{}
		if v, ok := st[k]; ok {
			exp = append(exp, Out{Op: wal.Upsert, Key: k, Val: v})
		}
		pres := len(exp) == 1
		for _, e := range all[int(rg.h):] {
			if e.Key != k {
				continue
			}
			if e.Op == wal.Upsert {
				exp = append(exp, Out{Op: wal.Upsert, Key: k, Val: e.Val})
				pres = true
			} else if pres {
				exp = append(exp, Out{Op: wal.Delete, Key: k})
				pres = false
			}
		}
		if fmt.Sprint(exp) != fmt.Sprint(seq) {
			t.Fatalf("inv3 key %d: %v != %v", k, seq, exp)
		}
	}
}

func TestInvariantOutputPrefix(t *testing.T) { scenario(t, 1) }
func TestInvariantViewConsistent(t *testing.T) {
	for _, s := range []int64{1, 2, 3} {
		scenario(t, s)
	}
}
func TestInvariantNoRepeat(t *testing.T) { scenario(t, 7) }

// limitAPI 建一个 maxRows=1、已到 ReadChunk 阶段的协调器；two 时快照里有两个键。
func limitAPI(t *testing.T, two bool) *Coordinator {
	t.Helper()
	c := New(wal.New(), 1)
	es := []wal.Entry{U(1, "a")}
	if two {
		es = append(es, U(2, "b"))
	}
	c.lg.Append(es)
	me(t, c.BeginChunk(0, 10))
	me(t, c.ReadChunk())
	return c
}
