package notify

import (
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

type version struct {
	eff    int64
	body   *parsedBody // 墓碑为 nil
	retire bool
}

type versionList struct {
	vers []*version // 按 eff 升序，eff 唯一（相同 eff 覆盖）
}

// probeRecord 记录单个键一次二分查找的版本数与探测次数，
// 用于证明探测次数不超过 ceil(log2(版本数+1))。
type probeRecord struct {
	k     key
	n     int
	probe int
}

type probeStats struct {
	mu      sync.Mutex
	records map[key]probeRecord
}

type key struct {
	name, loc, ch string
}

type Store struct {
	mu sync.RWMutex
	dl string
	m  map[key]*versionList

	// renderKeyLookups 为最近一次 Render 的键查找计数（非导出，供测试证明界）。
	renderKeyLookups atomic.Int64
	probe            probeStats
}

func New(defaultLang string) *Store {
	if !validateLanguage(defaultLang) {
		panic("notify: invalid default language")
	}
	return &Store{dl: defaultLang, m: make(map[key]*versionList)}
}

func (p *probeStats) note(rec probeRecord) {
	p.mu.Lock()
	if p.records == nil {
		p.records = make(map[key]probeRecord)
	}
	p.records[rec.k] = rec
	p.mu.Unlock()
}

// probeSnapshot 返回最近一次记录的各键探测情况（非导出，供测试证明界）。
func (s *Store) probeSnapshot() map[key]probeRecord {
	s.probe.mu.Lock()
	defer s.probe.mu.Unlock()
	out := make(map[key]probeRecord, len(s.probe.records))
	for k, r := range s.probe.records {
		out[k] = r
	}
	return out
}

func (s *Store) Publish(name, loc, ch, body string, eff int64) *Error {
	if !validName(name) || !validChannel(ch) || !validEff(eff) || len(body) == 0 || len(body) > 1000 || !utf8.ValidString(body) {
		return &Error{Reason: ReasonInvalidParam}
	}
	if !validateLanguage(loc) {
		return &Error{Reason: ReasonInvalidLanguage}
	}
	pb, perr := parseBody(body)
	if perr != nil {
		return perr
	}
	s.mu.Lock()
	s.upsert(key{name, loc, ch}, &version{eff: eff, body: pb})
	s.mu.Unlock()
	return nil
}

func (s *Store) Retire(name, loc, ch string, eff int64) *Error {
	if !validName(name) || !validChannel(ch) || !validEff(eff) {
		return &Error{Reason: ReasonInvalidParam}
	}
	if !validateLanguage(loc) {
		return &Error{Reason: ReasonInvalidLanguage}
	}
	s.mu.Lock()
	s.upsert(key{name, loc, ch}, &version{eff: eff, retire: true})
	s.mu.Unlock()
	return nil
}

func (s *Store) upsert(k key, v *version) {
	vl := s.m[k]
	if vl == nil {
		vl = &versionList{}
		s.m[k] = vl
	}
	idx := vl.search(v.eff)
	if idx < len(vl.vers) && vl.vers[idx].eff == v.eff {
		vl.vers[idx] = v
		return
	}
	vl.vers = append(vl.vers, nil)
	copy(vl.vers[idx+1:], vl.vers[idx:])
	vl.vers[idx] = v
}

// search 返回 eff 不大于 target 的最大版本下标 +1（即插入位语义）：
// 命中版本为 vers[idx-1]。采用显式二分以计数探测次数。
func (vl *versionList) search(target int64, counter ...*int) int {
	lo, hi := 0, len(vl.vers)
	for lo < hi {
		if len(counter) > 0 && counter[0] != nil {
			*counter[0]++
		}
		mid := lo + (hi-lo)/2
		if vl.vers[mid].eff <= target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// latestAt 返回 eff 不大于 at 的最大 eff 版本，无版本则 nil，
// 同时返回本次二分的探测次数。
func (vl *versionList) latestAt(at int64) (*version, int) {
	probe := 0
	idx := vl.search(at, &probe)
	if idx == 0 {
		return nil, probe
	}
	return vl.vers[idx-1], probe
}

// ceilLog2Plus1 返回 ceil(log2(n+1))。
func ceilLog2Plus1(n int) int {
	x, k := n+1, 0
	for (1 << k) < x {
		k++
	}
	return k
}
