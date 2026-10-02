package ontology

// commitColumn 在写锁内把一条记录在某列上的条目提交，处理分页与统计。
func (s *Shredder) commitColumn(col, recIdx int, es []Entry) {
	c := &s.cols[col]
	if c.curRecs > 0 && c.curEntry >= s.pageSize {
		c.pages = append(c.pages, PageInfo{
			StartRecord: c.curStart,
			RecordCount: c.curRecs,
			EntryCount:  c.curEntry,
		})
		c.curStart = recIdx
		c.curRecs = 0
		c.curEntry = 0
	}
	if c.curRecs == 0 {
		c.curStart = recIdx
	}
	c.entries = append(c.entries, es...)
	c.curRecs++
	c.curEntry += len(es)

	maxDef := s.leaves[col].maxDef
	for _, e := range es {
		if e.Def < maxDef {
			c.nulls++
		} else {
			c.presents++
			if !c.hasValue {
				c.min, c.max, c.hasValue = e.Value, e.Value, true
			} else {
				if e.Value < c.min {
					c.min = e.Value
				}
				if e.Value > c.max {
					c.max = e.Value
				}
			}
		}
	}
}

func (s *Shredder) colIndex(col string) (int, error) {
	for i := range s.leaves {
		if s.leaves[i].name == col {
			return i, nil
		}
	}
	return -1, &FieldError{Kind: ErrSchema, Path: col}
}

func (s *Shredder) snapshotPages(col int) []PageInfo {
	c := &s.cols[col]
	out := make([]PageInfo, len(c.pages), len(c.pages)+1)
	copy(out, c.pages)
	if c.curRecs > 0 {
		out = append(out, PageInfo{
			StartRecord: c.curStart,
			RecordCount: c.curRecs,
			EntryCount:  c.curEntry,
		})
	}
	return out
}

// Entries 返回某列的全部条目副本。
func (s *Shredder) Entries(col string) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, err := s.colIndex(col)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, len(s.cols[i].entries))
	copy(out, s.cols[i].entries)
	return out, nil
}

// Pages 返回某列的分页信息。
func (s *Shredder) Pages(col string) ([]PageInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, err := s.colIndex(col)
	if err != nil {
		return nil, err
	}
	return s.snapshotPages(i), nil
}

// Stats 返回某列统计。
func (s *Shredder) Stats(col string) (Stats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, err := s.colIndex(col)
	if err != nil {
		return Stats{}, err
	}
	c := &s.cols[i]
	return Stats{
		NullCount:    c.nulls,
		PresentCount: c.presents,
		Min:          c.min,
		Max:          c.max,
	}, nil
}

// Records 返回已成功接受的记录数。
func (s *Shredder) Records() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recordN
}

// EntriesRead 返回上次 Assemble 读取的条目总数。
func (s *Shredder) EntriesRead() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.read
}
