package altruistic

// naiveSim 是按题目规则逐步写成的朴素参考模型：全部用 map 集合表达，
// 不做任何位集优化，仅用于与 Manager 做差分对照。
type naiveSim struct {
	n       int
	nextID  int
	state   map[int]string // active / finished / aborted
	locked  map[int]map[int]bool
	holding map[int]map[int]bool
	donated map[int]map[int]bool
	wk      map[int]map[int]bool
	holder  map[int]int
	log     []string
}

func newNaive(n int) *naiveSim {
	s := &naiveSim{
		n:       n,
		nextID:  1,
		state:   map[int]string{},
		locked:  map[int]map[int]bool{},
		holding: map[int]map[int]bool{},
		donated: map[int]map[int]bool{},
		wk:      map[int]map[int]bool{},
		holder:  map[int]int{},
	}
	return s
}

// result 统一表示一次调用的输出：OK、拒绝原因（含事务/对象）或 Abort 列表。
type naiveResult struct {
	ok      bool
	kind    string
	txn     int
	obj     int
	beginID int
	aborted []int
}

func (s *naiveSim) Begin() naiveResult {
	t := s.nextID
	s.nextID++
	s.state[t] = "active"
	s.locked[t] = map[int]bool{}
	s.holding[t] = map[int]bool{}
	s.donated[t] = map[int]bool{}
	s.wk[t] = map[int]bool{}
	s.log = append(s.log, "Begin() -> "+itoa(t))
	return naiveResult{ok: true, beginID: t}
}

func (s *naiveSim) active(t int) bool { return s.state[t] == "active" }

func (s *naiveSim) Lock(t, o int) naiveResult {
	if _, exists := s.state[t]; !exists {
		r := naiveResult{kind: "unknown_transaction", txn: t}
		s.log = append(s.log, "Lock("+itoa(t)+","+itoa(o)+") -> REJECT unknown_transaction")
		return r
	}
	if !s.active(t) {
		r := naiveResult{kind: "not_active", txn: t}
		s.log = append(s.log, "Lock("+itoa(t)+","+itoa(o)+") -> REJECT not_active")
		return r
	}
	if o < 0 || o >= s.n {
		r := naiveResult{kind: "object_out_of_range", txn: t, obj: o}
		s.log = append(s.log, "Lock("+itoa(t)+","+itoa(o)+") -> REJECT object_out_of_range")
		return r
	}
	if s.donated[t][o] {
		s.log = append(s.log, "Lock("+itoa(t)+","+itoa(o)+") -> REJECT already_donated")
		return naiveResult{kind: "already_donated", txn: t, obj: o}
	}
	if s.holder[o] == t {
		s.log = append(s.log, "Lock("+itoa(t)+","+itoa(o)+") -> OK (already held)")
		return naiveResult{ok: true}
	}
	if h, held := s.holder[o]; held {
		s.log = append(s.log, "Lock("+itoa(t)+","+itoa(o)+") -> REJECT object_held by "+itoa(h))
		return naiveResult{kind: "object_held", txn: h, obj: o}
	}
	// S' = locked(t) ∪ {o}
	sPrime := map[int]bool{}
	for x := range s.locked[t] {
		sPrime[x] = true
	}
	sPrime[o] = true
	// C = wk(t) 中仍活跃者 ∪ 满足 o∈donated(T) 的其他活跃 T
	c := map[int]bool{}
	for u := range s.wk[t] {
		if s.active(u) {
			c[u] = true
		}
	}
	for donor, dset := range s.donated {
		if donor != t && s.active(donor) && dset[o] {
			c[donor] = true
		}
	}
	smallest := -1
	for u := range c {
		for x := range sPrime {
			if !s.donated[u][x] {
				if smallest == -1 || u < smallest {
					smallest = u
				}
				break
			}
		}
	}
	if smallest != -1 {
		s.log = append(s.log, "Lock("+itoa(t)+","+itoa(o)+") -> REJECT wake_violation by "+itoa(smallest))
		return naiveResult{kind: "wake_violation", txn: smallest, obj: o}
	}
	s.holder[o] = t
	s.holding[t][o] = true
	s.locked[t][o] = true
	for donor, dset := range s.donated {
		if donor != t && s.active(donor) && dset[o] {
			s.wk[t][donor] = true
		}
	}
	s.log = append(s.log, "Lock("+itoa(t)+","+itoa(o)+") -> OK granted")
	return naiveResult{ok: true}
}

func (s *naiveSim) Donate(t, o int) naiveResult {
	if _, exists := s.state[t]; !exists {
		s.log = append(s.log, "Donate("+itoa(t)+","+itoa(o)+") -> REJECT unknown_transaction")
		return naiveResult{kind: "unknown_transaction", txn: t}
	}
	if !s.active(t) {
		s.log = append(s.log, "Donate("+itoa(t)+","+itoa(o)+") -> REJECT not_active")
		return naiveResult{kind: "not_active", txn: t}
	}
	if o < 0 || o >= s.n {
		s.log = append(s.log, "Donate("+itoa(t)+","+itoa(o)+") -> REJECT object_out_of_range")
		return naiveResult{kind: "object_out_of_range", txn: t, obj: o}
	}
	if s.holder[o] != t {
		s.log = append(s.log, "Donate("+itoa(t)+","+itoa(o)+") -> REJECT not_held")
		return naiveResult{kind: "not_held", txn: t, obj: o}
	}
	delete(s.holder, o)
	delete(s.holding[t], o)
	s.donated[t][o] = true
	s.log = append(s.log, "Donate("+itoa(t)+","+itoa(o)+") -> OK")
	return naiveResult{ok: true}
}

func (s *naiveSim) Finish(t int) naiveResult {
	if _, exists := s.state[t]; !exists {
		s.log = append(s.log, "Finish("+itoa(t)+") -> REJECT unknown_transaction")
		return naiveResult{kind: "unknown_transaction", txn: t}
	}
	if !s.active(t) {
		s.log = append(s.log, "Finish("+itoa(t)+") -> REJECT not_active")
		return naiveResult{kind: "not_active", txn: t}
	}
	smallest := -1
	for u := range s.wk[t] {
		if s.active(u) && (smallest == -1 || u < smallest) {
			smallest = u
		}
	}
	if smallest != -1 {
		s.log = append(s.log, "Finish("+itoa(t)+") -> REJECT wake_unfinished "+itoa(smallest))
		return naiveResult{kind: "wake_unfinished", txn: smallest}
	}
	for o := range s.holding[t] {
		delete(s.holder, o)
	}
	s.holding[t] = map[int]bool{}
	s.state[t] = "finished"
	s.log = append(s.log, "Finish("+itoa(t)+") -> OK")
	return naiveResult{ok: true}
}

func (s *naiveSim) Abort(t int) naiveResult {
	if _, exists := s.state[t]; !exists {
		s.log = append(s.log, "Abort("+itoa(t)+") -> REJECT unknown_transaction")
		return naiveResult{kind: "unknown_transaction", txn: t}
	}
	if !s.active(t) {
		s.log = append(s.log, "Abort("+itoa(t)+") -> REJECT not_active")
		return naiveResult{kind: "not_active", txn: t}
	}
	a := map[int]bool{t: true}
	for {
		grew := false
		for u := range s.state {
			if !s.active(u) || a[u] {
				continue
			}
			for v := range a {
				if s.wk[u][v] {
					a[u] = true
					grew = true
					break
				}
			}
		}
		if !grew {
			break
		}
	}
	var list []int
	for u := range a {
		list = append(list, u)
	}
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if list[j] < list[i] {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	for _, u := range list {
		for o := range s.holding[u] {
			delete(s.holder, o)
		}
		s.holding[u] = map[int]bool{}
		s.state[u] = "aborted"
	}
	s.log = append(s.log, "Abort("+itoa(t)+") -> OK aborted="+fmtSlice(list))
	return naiveResult{ok: true, aborted: list}
}

func itoa(x int) string {
	if x == 0 {
		return "0"
	}
	neg := x < 0
	if neg {
		x = -x
	}
	var b [24]byte
	i := len(b)
	for x > 0 {
		i--
		b[i] = byte('0' + x%10)
		x /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func fmtSlice(xs []int) string {
	out := "["
	for i, x := range xs {
		if i > 0 {
			out += " "
		}
		out += itoa(x)
	}
	return out + "]"
}
