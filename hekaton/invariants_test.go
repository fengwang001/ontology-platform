package hekaton

import "testing"

// checkOpenVersionInvariant：每键非垃圾版本中无有效结束者的至多一个，
// 且它必是最新的非垃圾版本。
func checkOpenVersionInvariant(t *testing.T, e *Engine, tag string) {
	t.Helper()
	for k, vs := range e.keys {
		open := 0
		for i, v := range vs {
			if v.creator != 0 && e.txns[v.creator].state == Aborted {
				continue
			}
			validEnder := v.ender != 0 && e.txns[v.ender].state != Aborted
			if !validEnder {
				open++
				for j := i + 1; j < len(vs); j++ {
					cj := vs[j].creator
					if cj == 0 || e.txns[cj].state != Aborted {
						t.Fatalf("%s: 键%d 无有效结束者的非垃圾版本不是最新者",
							tag, k)
					}
				}
			}
		}
		if open > 1 {
			t.Fatalf("%s: 键%d 有 %d 个无有效结束者的非垃圾版本", tag, k, open)
		}
	}
}

// checkCommittedProperties 在独立引擎上重放序列：
//  1. 无推测残留：已提交事务在非自写键上读到的值 = ET<RT 的已提交创建者
//     中最大 ET 者的值；
//  2. 两个互有重叠的已提交事务不会写同一键。
func checkCommittedProperties(t *testing.T, e *Engine, ops []op, tag string) {
	t.Helper()
	type info struct {
		wrote map[int]bool
		reads map[int]int
	}
	infos := map[int]*info{}
	replay, err := New(e.k)
	if err != nil {
		t.Fatal(err)
	}
	getInfo := func(id int) *info {
		if infos[id] == nil {
			infos[id] = &info{wrote: map[int]bool{}, reads: map[int]int{}}
		}
		return infos[id]
	}
	for _, o := range ops {
		switch o.kind {
		case 'b':
			id := replay.Begin()
			getInfo(id)
		case 'r':
			if v, rerr := replay.Read(o.t, o.key); rerr == nil {
				getInfo(o.t).reads[o.key] = v
			}
		case 'w':
			if ab, werr := replay.Write(o.t, o.key, o.x); werr == nil && ab == nil {
				getInfo(o.t).wrote[o.key] = true
			}
		case 'p':
			_, _ = replay.Precommit(o.t)
		case 'f':
			_, _ = replay.Finish(o.t)
		case 'a':
			_, _ = replay.Abort(o.t)
		}
	}

	for id, tr := range replay.txns {
		if tr.state != Committed {
			continue
		}
		inf := getInfo(id)
		for key, got := range inf.reads {
			if inf.wrote[key] {
				continue
			}
			want, bestET := 0, 0
			for _, ver := range replay.keys[key] {
				if ver.creator == 0 {
					continue
				}
				cr := replay.txns[ver.creator]
				if cr.state != Committed || !(cr.et < tr.rt) {
					continue
				}
				if cr.et >= bestET {
					bestET = cr.et
					want = ver.value
				}
			}
			if got != want {
				t.Fatalf("%s: 已提交 t%d(RT%d) 键%d 读到 %d, 无推测残留期望 %d",
					tag, id, tr.rt, key, got, want)
			}
		}
	}

	writers := map[int][]int{}
	for id := range infos {
		if replay.txns[id].state != Committed {
			continue
		}
		for key := range infos[id].wrote {
			writers[key] = append(writers[key], id)
		}
	}
	for key, ids := range writers {
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				a, b := replay.txns[ids[i]], replay.txns[ids[j]]
				if a.rt < b.et && b.rt < a.et {
					t.Fatalf("%s: 重叠已提交事务 t%d,t%d 同写键%d",
						tag, ids[i], ids[j], key)
				}
			}
		}
	}
}
