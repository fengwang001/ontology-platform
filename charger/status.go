package charger

// Status 返回充电站当前整体快照（按会话 ID 升序）。
func (st *Station) Status() Status {
	st.mu.Lock()
	defer st.mu.Unlock()

	out := Status{Now: st.now, TotalCap: st.totalCap}
	for _, s := range st.sess {
		out.Sessions = append(out.Sessions, s.snapshot())
	}
	sortSnapshots(out.Sessions)
	return out
}

// SessionStatus 返回单个会话快照。
func (st *Station) SessionStatus(id int64) (Snapshot, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.sess[id]
	if !ok {
		return Snapshot{}, false
	}
	return s.snapshot(), true
}

// Now 返回当前时钟。
func (st *Station) Now() int64 {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.now
}

func sortSnapshots(xs []Snapshot) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1].ID > xs[j].ID; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}
