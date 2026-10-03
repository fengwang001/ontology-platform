package mailthread

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// 朴素模拟：线程用显式集合、合并逐元素搬移、候选扫描全部线程、
// 根与统计量每次现算。用于与优化实现（主题索引 + 按小并大）对拍。

type simNode struct {
	id   string
	real bool
	ts   int64
	norm string
}

type simThread struct {
	members map[string]*simNode
}

type simulation struct {
	w       int64
	n       int
	nodes   map[string]*simNode
	where   map[string]*simThread
	threads map[*simThread]bool
}

func newSimulation(w int64, n int) *simulation {
	return &simulation{
		w:       w,
		n:       n,
		nodes:   make(map[string]*simNode),
		where:   make(map[string]*simThread),
		threads: make(map[*simThread]bool),
	}
}

func simRoot(th *simThread) *simNode {
	var root *simNode
	for _, nd := range th.members {
		if !nd.real {
			continue
		}
		if root == nil || nd.ts < root.ts || (nd.ts == root.ts && nd.id < root.id) {
			root = nd
		}
	}
	return root
}

func simMinMaxTS(th *simThread) (minTS, maxTS int64) {
	first := true
	for _, nd := range th.members {
		if !nd.real {
			continue
		}
		if first || nd.ts < minTS {
			minTS = nd.ts
		}
		if first || nd.ts > maxTS {
			maxTS = nd.ts
		}
		first = false
	}
	return
}

func simThreadID(th *simThread) string {
	return simRoot(th).id
}

type simResult struct {
	thread string
	path   Path
	gone   []string
}

func (s *simulation) add(id string, refs []string, inReplyTo, subject string, ts int64) (simResult, error) {
	// 1. 参数非法。
	if id == "" || ts < 0 || ts > MaxTS || len(refs) > MaxRefs {
		return simResult{}, ErrInvalidParam
	}
	seen := map[string]bool{}
	var L []string
	for _, r := range refs {
		if r == "" || r == id {
			return simResult{}, ErrInvalidParam
		}
		if !seen[r] {
			seen[r] = true
			L = append(L, r)
		}
	}
	if inReplyTo != "" {
		if inReplyTo == id {
			return simResult{}, ErrInvalidParam
		}
		if !seen[inReplyTo] {
			seen[inReplyTo] = true
			L = append(L, inReplyTo)
		}
	}
	// 2. 重复登记。
	if nd, ok := s.nodes[id]; ok && nd.real {
		return simResult{}, ErrDuplicate
	}
	// 3. 容量不足。
	newCnt := 0
	if _, ok := s.nodes[id]; !ok {
		newCnt++
	}
	for _, r := range L {
		if _, ok := s.nodes[r]; !ok {
			newCnt++
		}
	}
	if len(s.nodes)+newCnt > s.n {
		return simResult{}, ErrCapacity
	}

	norm, isReply := normalizeSubject(subject)

	if len(L) > 0 || s.nodes[id] != nil {
		return s.addByRef(id, L, norm, ts), nil
	}
	if isReply {
		if best := s.findCandidate(norm, ts); best != nil {
			oldID := simThreadID(best)
			nd := &simNode{id: id, real: true, ts: ts, norm: norm}
			s.nodes[id] = nd
			best.members[id] = nd
			s.where[id] = best
			newID := simThreadID(best)
			var gone []string
			if oldID != newID {
				gone = []string{oldID}
			}
			return simResult{thread: newID, path: PathSubject, gone: gone}, nil
		}
	}
	nd := &simNode{id: id, real: true, ts: ts, norm: norm}
	th := &simThread{members: map[string]*simNode{id: nd}}
	s.nodes[id] = nd
	s.where[id] = th
	s.threads[th] = true
	return simResult{thread: id, path: PathNew}, nil
}

func (s *simulation) addByRef(id string, L []string, norm string, ts int64) simResult {
	involved := map[*simThread]bool{}
	if th, ok := s.where[id]; ok {
		involved[th] = true
	} else {
		s.nodes[id] = &simNode{id: id}
	}
	for _, r := range L {
		if th, ok := s.where[r]; ok {
			involved[th] = true
		} else {
			s.nodes[r] = &simNode{id: r}
		}
	}

	var oldIDs []string
	for th := range involved {
		oldIDs = append(oldIDs, simThreadID(th))
	}

	// 逐元素搬移到新集合。
	merged := &simThread{members: map[string]*simNode{}}
	for th := range involved {
		for nid, nd := range th.members {
			delete(th.members, nid)
			merged.members[nid] = nd
			s.where[nid] = merged
		}
		delete(s.threads, th)
	}
	// 新增节点逐个放入。
	for _, nid := range append(append([]string{}, L...), id) {
		nd := s.nodes[nid]
		if _, ok := s.where[nid]; !ok {
			merged.members[nid] = nd
			s.where[nid] = merged
		}
	}
	s.threads[merged] = true

	nd := s.nodes[id]
	nd.real = true
	nd.ts = ts
	nd.norm = norm

	newID := simThreadID(merged)
	var gone []string
	for _, g := range oldIDs {
		if g != newID {
			gone = append(gone, g)
		}
	}
	sort.Strings(gone)
	return simResult{thread: newID, path: PathRef, gone: gone}
}

func (s *simulation) findCandidate(norm string, ts int64) *simThread {
	var best *simThread
	var bestMax int64
	var bestID string
	for th := range s.threads {
		root := simRoot(th)
		if root.norm != norm {
			continue
		}
		minTS, maxTS := simMinMaxTS(th)
		if minTS > ts || ts-maxTS > s.w {
			continue
		}
		id := simThreadID(th)
		if best == nil || maxTS > bestMax || (maxTS == bestMax && id < bestID) {
			best = th
			bestMax = maxTS
			bestID = id
		}
	}
	return best
}

func (s *simulation) threadOf(id string) (string, bool) {
	th, ok := s.where[id]
	if !ok {
		return "", false
	}
	return simThreadID(th), true
}

func (s *simulation) threadIDs() []string {
	var ids []string
	for th := range s.threads {
		ids = append(ids, simThreadID(th))
	}
	sort.Strings(ids)
	return ids
}

func (s *simulation) members(tid string) []Member {
	for th := range s.threads {
		if simThreadID(th) != tid {
			continue
		}
		var ms []Member
		for _, nd := range th.members {
			if nd.real {
				ms = append(ms, Member{ID: nd.id, TS: nd.ts})
			}
		}
		sort.Slice(ms, func(i, j int) bool {
			if ms[i].TS != ms[j].TS {
				return ms[i].TS < ms[j].TS
			}
			return ms[i].ID < ms[j].ID
		})
		return ms
	}
	return nil
}

// 随机登记序列对拍：优化实现 vs 朴素模拟，2000 组。
// 日志打印每步输入、两边输出与判定依据。
func TestDifferentialRandom(t *testing.T) {
	const iterations = 2000
	idPool := []string{"m0", "m1", "m2", "m3", "m4", "m5", "m6", "m7"}
	refPool := []string{"m0", "m1", "m2", "m3", "m4", "m5", "m6", "m7",
		"p0", "p1", "p2", "p3", "p4"}
	subjPool := []string{
		"Hello", "Re: Hello", "RE: Hello", "Fwd: Hello", "回复：Hello",
		"Other", "Re: Other", "fw:Other", "X", "Re: X", "转发: X",
	}
	windows := []int64{0, 1, 2, 5, 100, 1000}

	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(iter)))
		w := windows[rng.Intn(len(windows))]
		n := 5 + rng.Intn(36)
		th, err := New(w, n)
		if err != nil {
			t.Fatal(err)
		}
		sim := newSimulation(w, n)

		ops := 1 + rng.Intn(40)
		t.Logf("=== 序列 %d: W=%d N=%d 操作数=%d ===", iter, w, n, ops)
		for step := 0; step < ops; step++ {
			id := idPool[rng.Intn(len(idPool))]
			var refs []string
			if rng.Intn(2) == 0 {
				k := rng.Intn(4)
				for i := 0; i < k; i++ {
					refs = append(refs, refPool[rng.Intn(len(refPool))])
				}
			}
			irt := ""
			if rng.Intn(10) < 3 {
				irt = refPool[rng.Intn(len(refPool))]
			}
			subj := subjPool[rng.Intn(len(subjPool))]
			ts := int64(rng.Intn(201))
			// 少量非法输入。
			switch rng.Intn(50) {
			case 0:
				id = ""
			case 1:
				ts = MaxTS + 1
			case 2:
				refs = append(refs, "")
			case 3:
				refs = append(refs, id)
			}

			gotRes, gotErr := th.Add(id, refs, irt, subj, ts)
			wantRes, wantErr := sim.add(id, refs, irt, subj, ts)

			input := fmt.Sprintf("Add(id=%q refs=%v irt=%q subj=%q ts=%d)",
				id, refs, irt, subj, ts)
			if (gotErr == nil) != (wantErr == nil) ||
				(gotErr != nil && !errorsIs(gotErr, wantErr)) {
				t.Fatalf("序列 %d 步 %d 错误类别不一致\n输入: %s\n实现: %v\n模拟: %v",
					iter, step, input, gotErr, wantErr)
			}
			if gotErr == nil {
				got := simResult{thread: gotRes.Thread, path: gotRes.Path, gone: gotRes.Gone}
				if got.thread != wantRes.thread || got.path != wantRes.path ||
					!equalStrings(got.gone, wantRes.gone) {
					t.Fatalf("序列 %d 步 %d 结果不一致\n输入: %s\n实现: %+v\n模拟: %+v",
						iter, step, input, got, wantRes)
				}
				t.Logf("步 %d %s -> 线程=%s 途径=%s Gone=%v (判定: 与朴素模拟一致)",
					step, input, got.thread, got.path, got.gone)
			} else {
				t.Logf("步 %d %s -> 拒绝(%v) (判定: 与朴素模拟一致)", step, input, gotErr)
			}
		}

		// 终态对比：线程划分、编号、成员、每个 id 的归属。
		gotThreads := th.Threads()
		wantThreads := sim.threadIDs()
		if !equalStrings(gotThreads, wantThreads) {
			t.Fatalf("序列 %d 终态线程不一致\n实现: %v\n模拟: %v", iter, gotThreads, wantThreads)
		}
		for _, tid := range gotThreads {
			gotM, _ := th.Members(tid)
			wantM := sim.members(tid)
			if !reflect.DeepEqual(gotM, wantM) {
				t.Fatalf("序列 %d 线程 %s 成员不一致\n实现: %v\n模拟: %v",
					iter, tid, gotM, wantM)
			}
		}
		for _, id := range append(append([]string{}, idPool...), "p0", "p1", "p2", "p3", "p4") {
			gotT, gotErr := th.ThreadOf(id)
			wantT, wantOK := sim.threadOf(id)
			if (gotErr == nil) != wantOK || (wantOK && gotT != wantT) {
				t.Fatalf("序列 %d ThreadOf(%s) 不一致: 实现=(%s,%v) 模拟=(%s,%v)",
					iter, id, gotT, gotErr, wantT, wantOK)
			}
		}
		t.Logf("序列 %d 终态一致: 线程=%v", iter, gotThreads)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func errorsIs(a, b error) bool {
	return a == b
}
