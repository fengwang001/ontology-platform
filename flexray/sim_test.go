package flexray

// 本文件仅包含朴素参考模拟器；随机对拍逻辑位于 fuzz_test.go。

type simFrame struct {
	assigned bool
	base     int
	rep      int
	static   *string
	queue    []simMsg
}

type simMsg struct {
	length int
	tag    string
}

// naiveSim 严格按题目文字逐步书写的参考模拟器。
type naiveSim struct {
	ns, nm, lt int
	c          int
	frames     []simFrame
	sent       int
	empty      int
	overwrite  int
	lastDynL   []int // 最近一次 cycle 中每条动态发送的 L，与 res.Sent 中动态项对齐
}

func newNaiveSim(ns, nm, lt int) (*naiveSim, ErrCode) {
	if ns < 1 || ns > 64 || nm < 0 || nm > 256 || lt < 0 || lt > nm {
		return nil, ErrInvalidParam
	}
	return &naiveSim{ns: ns, nm: nm, lt: lt, frames: make([]simFrame, ns+nm+1)}, 0
}

func validRep(rep int) bool {
	switch rep {
	case 1, 2, 4, 8, 16, 32, 64:
		return true
	}
	return false
}

func (s *naiveSim) assign(id, base, rep int) ErrCode {
	if !validRep(rep) || base < 0 || base >= rep {
		return ErrInvalidParam
	}
	if id < 1 || id > s.ns+s.nm {
		return ErrInvalidID
	}
	if s.frames[id].assigned {
		return ErrDuplicate
	}
	s.frames[id].assigned = true
	s.frames[id].base = base
	s.frames[id].rep = rep
	return 0
}

func (s *naiveSim) post(id, length int, tag string) ErrCode {
	if id < 1 || id > s.ns+s.nm {
		return ErrInvalidID
	}
	f := &s.frames[id]
	if !f.assigned {
		return ErrNotAssigned
	}
	if id > s.ns {
		if length < 1 || length > s.nm {
			return ErrInvalidParam
		}
		if len(f.queue) >= 8 {
			return ErrQueueFull
		}
		f.queue = append(f.queue, simMsg{length: length, tag: tag})
		return 0
	}
	if f.static != nil {
		s.overwrite++
	}
	v := tag
	f.static = &v
	return 0
}

func (s *naiveSim) cycle() CycleResult {
	c := s.c
	res := CycleResult{C: c, Sent: []SendItem{}}
	s.lastDynL = s.lastDynL[:0]

	u := 0
	for k := 1; k <= s.ns; k++ {
		f := &s.frames[k]
		if f.assigned && c%f.rep == f.base {
			if f.static != nil {
				res.Sent = append(res.Sent, SendItem{ID: k, Tag: *f.static, Slot: k})
				f.static = nil
				s.sent++
			} else {
				u++
				res.EmptyFrame++
				s.empty++
			}
		}
	}
	res.Unused = u

	N := s.nm + u
	k := s.ns + 1
	i := 1
	for i <= N {
		if k <= s.ns+s.nm {
			f := &s.frames[k]
			if f.assigned && c%f.rep == f.base && len(f.queue) > 0 {
				head := f.queue[0]
				L := head.length
				if i <= s.lt && i+L-1 <= N {
					res.Sent = append(res.Sent, SendItem{ID: k, Tag: head.tag, Start: i})
					s.lastDynL = append(s.lastDynL, L)
					f.queue = f.queue[1:]
					s.sent++
					i = i + L
					k = k + 1
					continue
				}
			}
		}
		i = i + 1
		k = k + 1
	}

	s.c = (c + 1) % 64
	return res
}

func (s *naiveSim) pending(id int) ([]string, ErrCode) {
	if id < 1 || id > s.ns+s.nm {
		return nil, ErrInvalidID
	}
	f := &s.frames[id]
	out := make([]string, len(f.queue))
	for i, m := range f.queue {
		out[i] = m.tag
	}
	return out, 0
}

func (s *naiveSim) stats() Stats {
	return Stats{Sent: s.sent, EmptyFrame: s.empty, Overwrite: s.overwrite}
}
