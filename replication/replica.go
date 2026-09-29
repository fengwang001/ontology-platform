package replication

// Entry 是日志中的一条消息；位点由其在日志中的下标确定。
type Entry struct {
	Gen  int64
	Data string
}

// GenMark 是世代缓存项：世代 Gen 的第一条消息起始位点 Start。
type GenMark struct {
	Gen   int64
	Start int
}

// Replica 是一个副本：本地日志 + 世代起始位点缓存。
type Replica struct {
	name string
	mu   chan struct{}
	log  []Entry
	gen  []GenMark
}

func newReplica(name string) *Replica {
	return &Replica{
		name: name,
		mu:   make(chan struct{}, 1),
	}
}

// lock 以可重入的方式持有单副本互斥量；unlock 成对调用。
func (r *Replica) lock()   { r.mu <- struct{}{} }
func (r *Replica) unlock() { <-r.mu }

// cloneLog 返回日志快照（调用方须持锁）。
func (r *Replica) cloneLog() []Entry {
	out := make([]Entry, len(r.log))
	copy(out, r.log)
	return out
}

// cloneGen 返回世代缓存快照（调用方须持锁）。
func (r *Replica) cloneGen() []GenMark {
	out := make([]GenMark, len(r.gen))
	copy(out, r.gen)
	return out
}

func (r *Replica) len() int { return len(r.log) }

func (r *Replica) entryAt(pos int) (Entry, bool) {
	if pos < 0 || pos >= len(r.log) {
		return Entry{}, false
	}
	return r.log[pos], true
}

// genStart 返回世代 gen 的起始位点；世代不存在时 ok=false。
func (r *Replica) genStart(gen int64) (int, bool) {
	for _, mark := range r.gen {
		if mark.Gen == gen {
			return mark.Start, true
		}
	}
	return 0, false
}

// appendGenMark 登记新世代起始位点：世代必须严格递增、位点必须落在日志末尾。
func (r *Replica) appendGenMark(gen int64, start int) bool {
	if len(r.gen) > 0 {
		last := r.gen[len(r.gen)-1]
		if gen <= last.Gen || start < last.Start {
			return false
		}
	}
	if start != len(r.log) {
		return false
	}
	r.gen = append(r.gen, GenMark{Gen: gen, Start: start})
	return true
}

// appendEntries 在日志末尾追加条目，并为其中出现的新世代登记缓存项。
func (r *Replica) appendEntries(entries []Entry) bool {
	base := len(r.log)
	maxGen := int64(0)
	if len(r.gen) > 0 {
		maxGen = r.gen[len(r.gen)-1].Gen
	}
	for i, entry := range entries {
		if entry.Gen < maxGen {
			return false
		}
		if entry.Gen > maxGen {
			r.gen = append(r.gen, GenMark{Gen: entry.Gen, Start: base + i})
			maxGen = entry.Gen
		}
	}
	r.log = append(r.log, entries...)
	return true
}

// truncateAt 删除位点 pos 及之后的日志，同时删除起始位点不小于 pos 的缓存项。
func (r *Replica) truncateAt(pos int) {
	if pos < 0 || pos > len(r.log) {
		return
	}
	r.log = r.log[:pos]
	kept := r.gen[:0]
	for _, mark := range r.gen {
		if mark.Start < pos {
			kept = append(kept, mark)
		}
	}
	r.gen = kept
}
