package ontology

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// naiveModel 是一份刻意“最直白”的独立实现：
//   - 所有条目放在一个切片里，每次查询全表扫描；
//   - 命中即按规格挑覆盖客户端的最长未过期范围；
//   - 不做任何并发合并（随机对照按串行顺序驱动，二者须逐字节一致）。
//
// 它与生产实现不共享任何内部数据结构，因此随机对照能有效抓出实现偏差。
type naiveModel struct {
	k        int
	clock    Clock
	upstream Upstream
	entries  []naiveEntry
}

type naiveEntry struct {
	name     string
	rrtype   uint16
	family   AddrFamily
	prefix   Addr
	bits     int
	kind     Kind
	records  []byte
	deadline time.Time
	writeSeq int64
}

func newNaiveModel(k int, clk Clock, up Upstream) *naiveModel {
	return &naiveModel{k: k, clock: clk, upstream: up}
}

func (m *naiveModel) Query(q Query) (Result, error) {
	if err := validateQuery(q); err != nil {
		return Result{}, err
	}
	_, mb := familyWidth(q.Family)
	nk := normalizeName(q.Name)
	now := m.clock.Now()

	if r, ok := m.lookup(nk, q.Rrtype, q.Family, q.Client, now); ok {
		return r, nil
	}

	ans, err := m.upstream.Resolve(q)
	if err != nil {
		return Result{}, ErrUpstreamFailure
	}
	if err := validateAnswer(ans, mb); err != nil {
		return Result{}, err
	}
	bits := ans.ScopePrefix
	if bits > q.SrcPrefix {
		bits = q.SrcPrefix
	}
	now = m.clock.Now()
	if ans.TTL > 0 {
		m.insert(nk, q.Rrtype, q.Family, q.Client, bits, ans, now)
	}
	return Result{Kind: ans.Kind, Records: append([]byte(nil), ans.Records...)}, nil
}

func (m *naiveModel) lookup(name string, rrtype uint16, fam AddrFamily, client Addr, now time.Time) (Result, bool) {
	m.gc(now)
	best := -1
	for i := range m.entries {
		e := &m.entries[i]
		if e.name == name && e.rrtype == rrtype && e.family == fam &&
			now.Before(e.deadline) && prefixCovers(e.prefix, e.bits, client) {
			if best == -1 || e.bits > m.entries[best].bits {
				best = i
			}
		}
	}
	if best == -1 {
		return Result{}, false
	}
	e := &m.entries[best]
	return Result{Kind: e.kind, Records: append([]byte(nil), e.records...)}, true
}

func (m *naiveModel) gc(now time.Time) {
	kept := m.entries[:0]
	for _, e := range m.entries {
		if now.Before(e.deadline) {
			kept = append(kept, e)
		}
	}
	m.entries = kept
}

var naiveSeq int64

func (m *naiveModel) insert(name string, rrtype uint16, fam AddrFamily, client Addr, bits int, a Answer, now time.Time) {
	if m.k <= 0 {
		return
	}
	var pfx Addr
	if bits == 0 {
		pfx = make(Addr, len(client))
	} else {
		pfx = maskPrefix(client, bits)
	}
	// 同键整体覆盖：移除旧条目。
	kept := m.entries[:0]
	for _, e := range m.entries {
		if !(e.name == name && e.rrtype == rrtype && e.family == fam &&
			e.bits == bits && bytesEqual(e.prefix, pfx)) {
			kept = append(kept, e)
		}
	}
	m.entries = kept

	naiveSeq++
	m.entries = append(m.entries, naiveEntry{
		name: name, rrtype: rrtype, family: fam, prefix: pfx, bits: bits,
		kind: a.Kind, records: append([]byte(nil), a.Records...),
		deadline: now.Add(time.Duration(a.TTL) * time.Second), writeSeq: naiveSeq,
	})

	type nk2 struct {
		name   string
		rrtype uint16
	}
	count := map[nk2]int{}
	for _, e := range m.entries {
		count[nk2{e.name, e.rrtype}]++
	}
	over := count[nk2{name, rrtype}] - m.k
	for ; over > 0; over-- {
		victim := -1
		for i := range m.entries {
			e := &m.entries[i]
			if e.name != name || e.rrtype != rrtype {
				continue
			}
			if victim == -1 {
				victim = i
				continue
			}
			v := &m.entries[victim]
			if e.deadline.Before(v.deadline) ||
				(e.deadline.Equal(v.deadline) && e.bits > v.bits) ||
				(e.deadline.Equal(v.deadline) && e.bits == v.bits && e.writeSeq < v.writeSeq) {
				victim = i
			}
		}
		if victim >= 0 {
			m.entries = append(m.entries[:victim], m.entries[victim+1:]...)
		}
	}
}

func bytesEqual(a, b []byte) bool {
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

// dumpEntries 返回与生产实现等价的“存活条目快照”，用于随机对照的结构性校验。
func (m *naiveModel) liveEntries(now time.Time) []string {
	m.gc(now)
	out := make([]string, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, fmt.Sprintf("%s/%d/%s/%d/%s@%d",
			e.name, e.rrtype, ipString(e.family, e.prefix), e.bits,
			kindName(e.kind), int(e.deadline.Sub(now).Seconds())))
	}
	sort.Strings(out)
	return out
}

func ipString(f AddrFamily, b []byte) string {
	if f == FamilyV4 {
		return fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3])
	}
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02x", x)
	}
	return strings.Join(parts, ":")
}

func kindName(k Kind) string {
	switch k {
	case KindRecords:
		return "R"
	case KindNoName:
		return "NX"
	case KindNoType:
		return "NT"
	default:
		return "?"
	}
}
