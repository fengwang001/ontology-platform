package ontology

// naiveMerger 是按规则逐步写成的朴素模拟：
// 线程用显式节点集合表示，合并时逐元素搬移；不做任何索引或按小并大。
// 仅用于与生产实现对拍。
type naiveMail struct {
	ts      int64
	subject string
}

type naiveThread struct {
	nodes map[string]bool // 全部节点（真实 + 占位）
	reals map[string]bool // 真实邮件
	mail  map[string]naiveMail
}

type naiveMerger struct {
	w, n  int
	nodes map[string]bool
	real  map[string]naiveMail
	// 每个节点所属线程对象。
	belong map[string]*naiveThread
}

func newNaive(w int64, n int) *naiveMerger {
	return &naiveMerger{
		w:      int(w),
		n:      n,
		nodes:  map[string]bool{},
		real:   map[string]naiveMail{},
		belong: map[string]*naiveThread{},
	}
}

func (s *naiveMerger) root(t *naiveThread) (string, int64) {
	id, ts := "", int64(0)
	for r := range t.reals {
		rts := t.mail[r].ts
		if id == "" || rts < ts || (rts == ts && r < id) {
			id, ts = r, rts
		}
	}
	return id, ts
}

func (s *naiveMerger) minMax(t *naiveThread) (int64, int64) {
	first := true
	var mn, mx int64
	for r := range t.reals {
		ts := t.mail[r].ts
		if first || ts < mn {
			mn = ts
		}
		if first || ts > mx {
			mx = ts
		}
		first = false
	}
	return mn, mx
}

// add 朴素实现 Add；返回 (结果, errKind)，errKind 为 "" / "invalid" / "duplicate" / "capacity"。
func (s *naiveMerger) add(id string, refs []string, irt, subject string, ts int64) (AddResult, string) {
	if id == "" || ts < 0 || ts > 1_000_000_000_000 || len(refs) > 50 {
		return AddResult{}, "invalid"
	}
	for _, r := range refs {
		if r == "" {
			return AddResult{}, "invalid"
		}
	}
	if irt != "" && irt == id {
		return AddResult{}, "invalid"
	}
	for _, r := range refs {
		if r == id {
			return AddResult{}, "invalid"
		}
	}
	linkSet := dedupePreserving(refs)
	if irt != "" {
		linkSet = appendUnique(linkSet, irt)
	}
	if _, ok := s.real[id]; ok {
		return AddResult{}, "duplicate"
	}
	need := 0
	if _, ok := s.nodes[id]; !ok {
		need++
	}
	for _, r := range linkSet {
		if !s.nodes[r] {
			need++
		}
	}
	if len(s.nodes)+need > s.n {
		return AddResult{}, "capacity"
	}

	norm, stripped := normalizeSubject(subject)
	isReply := stripped && norm != ""

	_, idExisted := s.nodes[id]

	if len(linkSet) > 0 || idExisted {
		// (1)
		for _, r := range linkSet {
			if !s.nodes[r] {
				s.nodes[r] = true
				th := &naiveThread{
					nodes: map[string]bool{r: true},
					reals: map[string]bool{},
					mail:  map[string]naiveMail{},
				}
				s.belong[r] = th
			}
		}
		if !idExisted {
			s.nodes[id] = true
			th := &naiveThread{
				nodes: map[string]bool{id: true},
				reals: map[string]bool{},
				mail:  map[string]naiveMail{},
			}
			s.belong[id] = th
		}
		pre := map[string]bool{}
		if rid, _ := s.root(s.belong[id]); rid != "" {
			pre[rid] = true
		}
		for _, r := range linkSet {
			if rid, _ := s.root(s.belong[r]); rid != "" {
				pre[rid] = true
			}
		}
		target := s.belong[id]
		for _, r := range linkSet {
			tr := s.belong[r]
			if tr == target {
				continue
			}
			// 显式集合、逐元素搬移。
			for x := range tr.nodes {
				target.nodes[x] = true
				s.belong[x] = target
			}
			for x := range tr.reals {
				target.reals[x] = true
			}
			for x, m := range tr.mail {
				target.mail[x] = m
			}
		}
		if _, real := s.real[id]; !real {
			s.real[id] = naiveMail{ts: ts, subject: subject}
			target.reals[id] = true
			target.mail[id] = naiveMail{ts: ts, subject: subject}
		}
		rid, _ := s.root(target)
		gone := []string{}
		for g := range pre {
			if g != rid {
				gone = append(gone, g)
			}
		}
		sortStrings(gone)
		return AddResult{ThreadID: rid, Way: "ref", Gone: gone}, ""
	}

	if isReply {
		// (2) 在全部线程中线性扫描候选（朴素实现不使用主题索引）。
		threadSet := map[*naiveThread]bool{}
		for _, th := range s.belong {
			threadSet[th] = true
		}
		best := (*naiveThread)(nil)
		bestMax := int64(0)
		for th := range threadSet {
			rid, _ := s.root(th)
			if rid == "" {
				continue
			}
			rn, _ := normalizeSubject(th.mail[rid].subject)
			if rn != norm {
				continue
			}
			mn, mx := s.minMax(th)
			if mn > ts {
				continue
			}
			if ts-mx > int64(s.w) {
				continue
			}
			if best == nil || mx > bestMax || (mx == bestMax && rid < bestRootID(best, s)) {
				best, bestMax = th, mx
			}
		}
		if best != nil {
			pre, _ := s.root(best)
			s.nodes[id] = true
			best.nodes[id] = true
			s.belong[id] = best
			s.real[id] = naiveMail{ts: ts, subject: subject}
			best.reals[id] = true
			best.mail[id] = naiveMail{ts: ts, subject: subject}
			rid, _ := s.root(best)
			gone := []string{}
			if pre != rid {
				gone = []string{pre}
			}
			return AddResult{ThreadID: rid, Way: "subject", Gone: gone}, ""
		}
	}

	// (3) 新建。
	s.nodes[id] = true
	s.real[id] = naiveMail{ts: ts, subject: subject}
	th := &naiveThread{
		nodes: map[string]bool{id: true},
		reals: map[string]bool{id: true},
		mail:  map[string]naiveMail{id: {ts: ts, subject: subject}},
	}
	s.belong[id] = th
	return AddResult{ThreadID: id, Way: "new", Gone: []string{}}, ""
}

func bestRootID(t *naiveThread, s *naiveMerger) string {
	id, _ := s.root(t)
	return id
}

func sortStrings(x []string) {
	for i := 1; i < len(x); i++ {
		for j := i; j > 0 && x[j-1] > x[j]; j-- {
			x[j-1], x[j] = x[j], x[j-1]
		}
	}
}

func (s *naiveMerger) threadOf(id string) (string, bool) {
	th, ok := s.belong[id]
	if !ok {
		return "", false
	}
	rid, _ := s.root(th)
	return rid, true
}

func (s *naiveMerger) members(threadID string) ([]string, bool) {
	th, ok := s.belong[threadID]
	if !ok {
		return nil, false
	}
	rid, _ := s.root(th)
	if rid != threadID {
		return nil, false
	}
	var ids []string
	for r := range th.reals {
		ids = append(ids, r)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0; j-- {
			a, b := th.mail[ids[j-1]].ts, th.mail[ids[j]].ts
			if a < b || (a == b && ids[j-1] < ids[j]) {
				break
			}
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
	return ids, true
}

func (s *naiveMerger) threads() []string {
	set := map[*naiveThread]bool{}
	for _, th := range s.belong {
		set[th] = true
	}
	var ids []string
	for th := range set {
		if rid, _ := s.root(th); rid != "" {
			ids = append(ids, rid)
		}
	}
	sortStrings(ids)
	return ids
}
