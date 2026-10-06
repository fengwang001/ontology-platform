package cookie

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"
)

// model 是独立编写的朴素参考实现：切片存储 + 线性扫描，
// 与 Store 的索引/堆实现语义完全一致，用于随机操作序列差分对照。
type model struct {
	cfg   Config
	now   time.Time
	seq   uint64
	items []Entry
	stats Stats
}

func newModel(cfg Config) *model { return &model{cfg: cfg} }

func (m *model) Set(in SetInput) error {
	if in.Name == "" || in.Site == "" || !strings.HasPrefix(in.Path, "/") || !in.SameSite.valid() {
		return ErrInvalidArgument
	}
	if in.At != nil && in.At.Before(m.now) {
		return ErrClockRegression
	}
	if strings.HasPrefix(in.Name, hostPrefix) && !(in.Secure && in.Path == "/" && in.Site == in.SourceSite) {
		return ErrHostPrefixViolation
	}
	if in.Secure && !in.SourceSecure {
		return ErrInsecureSource
	}
	if in.SameSite == SameSiteNone && !in.Secure {
		return ErrSameSiteNoneInsecure
	}
	k := makeKey(in.Name, in.Site, in.Path, in.PartitionKey)
	if in.ExpiresAt != nil && !in.ExpiresAt.After(m.now) {
		for i := range m.items {
			if m.items[i].key() == k {
				m.items = append(m.items[:i], m.items[i+1:]...)
				m.stats.ExplicitDeletes++
				break
			}
		}
		return nil
	}
	for i := range m.items {
		if m.items[i].key() == k {
			e := &m.items[i]
			e.Value = in.Value
			e.Secure = in.Secure
			e.HTTPOnly = in.HTTPOnly
			e.SameSite = in.SameSite
			e.ExpiresAt = in.ExpiresAt
			e.LastAccessedAt = m.now
			return nil
		}
	}
	m.seq++
	m.items = append(m.items, Entry{
		Name: in.Name, Value: in.Value, Site: in.Site, Path: in.Path,
		Secure: in.Secure, HTTPOnly: in.HTTPOnly, SameSite: in.SameSite,
		ExpiresAt: in.ExpiresAt, PartitionKey: in.PartitionKey,
		CreatedAt: m.now, LastAccessedAt: m.now, seq: m.seq,
	})
	m.stats.TotalAdds++
	m.enforce(in.Site)
	return nil
}

func (m *model) countSite(site string) int {
	n := 0
	for _, e := range m.items {
		if e.Site == site {
			n++
		}
	}
	return n
}

func (m *model) lruIndex(site string) int {
	best := -1
	for i := range m.items {
		if m.items[i].Site != site {
			continue
		}
		if best < 0 || lruLess(m.items[i], m.items[best]) {
			best = i
		}
	}
	return best
}

func lruLess(a, b Entry) bool {
	if !a.LastAccessedAt.Equal(b.LastAccessedAt) {
		return a.LastAccessedAt.Before(b.LastAccessedAt)
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.seq < b.seq
}

func (m *model) maxSite() string {
	counts := map[string]int{}
	for _, e := range m.items {
		counts[e.Site]++
	}
	best := ""
	bestN := -1
	for name, n := range counts {
		if n > bestN || (n == bestN && name < best) {
			best, bestN = name, n
		}
	}
	return best
}

func (m *model) removeAt(i int) {
	m.items = append(m.items[:i], m.items[i+1:]...)
}

func (m *model) enforce(site string) {
	for m.cfg.SiteLimit > 0 && m.countSite(site) > m.cfg.SiteLimit {
		var kept []Entry
		for _, e := range m.items {
			if e.Site == site && e.expired(m.now) {
				m.stats.ExpiredEvictions++
			} else {
				kept = append(kept, e)
			}
		}
		m.items = kept
		if m.countSite(site) <= m.cfg.SiteLimit {
			break
		}
		m.removeAt(m.lruIndex(site))
		m.stats.CapacityEvictions++
	}
	for m.cfg.GlobalLimit > 0 && len(m.items) > m.cfg.GlobalLimit {
		var kept []Entry
		for _, e := range m.items {
			if e.expired(m.now) {
				m.stats.ExpiredEvictions++
			} else {
				kept = append(kept, e)
			}
		}
		m.items = kept
		if len(m.items) <= m.cfg.GlobalLimit {
			break
		}
		m.removeAt(m.lruIndex(m.maxSite()))
		m.stats.CapacityEvictions++
	}
}

func (m *model) sameSiteOK(e *Entry, r Request) bool {
	sameSite := r.InitiatorSite == r.Site
	switch e.SameSite {
	case SameSiteStrict:
		return sameSite
	case SameSiteNone:
		return true
	case SameSiteLax:
		return sameSite || (r.TopLevelNav && r.SafeMethod)
	default:
		if sameSite || (r.TopLevelNav && r.SafeMethod) {
			return true
		}
		return r.TopLevelNav && !r.SafeMethod && m.now.Sub(e.CreatedAt) < m.cfg.LaxGrace
	}
}

func (m *model) Attach(r Request) ([]Entry, error) {
	if r.Site == "" || !strings.HasPrefix(r.Path, "/") {
		return nil, ErrInvalidArgument
	}
	var matched []Entry
	var kept []Entry
	for _, e := range m.items {
		if e.Site != r.Site {
			kept = append(kept, e)
			continue
		}
		if e.expired(m.now) {
			m.stats.ExpiredEvictions++
			continue
		}
		kept = append(kept, e)
		if !pathMatch(e.Path, r.Path) || (e.Secure && !r.Secure) ||
			!partitionEqual(e.PartitionKey, r.PartitionKey) || !m.sameSiteOK(&e, r) {
			continue
		}
		matched = append(matched, e)
	}
	m.items = kept
	sortAttachmentsModel(matched)
	for _, me := range matched {
		for i := range m.items {
			if m.items[i].key() == me.key() {
				m.items[i].LastAccessedAt = m.now
			}
		}
	}
	for j := range matched {
		matched[j].LastAccessedAt = m.now
	}
	return matched, nil
}

func (m *model) ScriptRead(site, path string, secure bool, partition *string) ([]Entry, error) {
	if site == "" || !strings.HasPrefix(path, "/") {
		return nil, ErrInvalidArgument
	}
	var matched []Entry
	var kept []Entry
	for _, e := range m.items {
		if e.Site != site {
			kept = append(kept, e)
			continue
		}
		if e.expired(m.now) {
			m.stats.ExpiredEvictions++
			continue
		}
		kept = append(kept, e)
		if e.HTTPOnly || !pathMatch(e.Path, path) || (e.Secure && !secure) ||
			!partitionEqual(e.PartitionKey, partition) {
			continue
		}
		matched = append(matched, e)
	}
	m.items = kept
	sortAttachmentsModel(matched)
	return matched, nil
}

func sortAttachmentsModel(es []Entry) {
	sort.Slice(es, func(i, j int) bool {
		if len(es[i].Path) != len(es[j].Path) {
			return len(es[i].Path) > len(es[j].Path)
		}
		if !es[i].CreatedAt.Equal(es[j].CreatedAt) {
			return es[i].CreatedAt.Before(es[j].CreatedAt)
		}
		return es[i].seq < es[j].seq
	})
}

func (m *model) ClearSite(site string) int {
	var kept []Entry
	n := 0
	for _, e := range m.items {
		if e.Site == site {
			n++
		} else {
			kept = append(kept, e)
		}
	}
	m.items = kept
	m.stats.ExplicitDeletes += uint64(n)
	return n
}

func (m *model) ClearPartition(partition *string) int {
	var kept []Entry
	n := 0
	for _, e := range m.items {
		if partitionEqual(e.PartitionKey, partition) {
			n++
		} else {
			kept = append(kept, e)
		}
	}
	m.items = kept
	m.stats.ExplicitDeletes += uint64(n)
	return n
}

func (m *model) ClearCreatedRange(start, end time.Time) (int, error) {
	if end.Before(start) {
		return 0, ErrInvalidArgument
	}
	var kept []Entry
	n := 0
	for _, e := range m.items {
		if !e.CreatedAt.Before(start) && e.CreatedAt.Before(end) {
			n++
		} else {
			kept = append(kept, e)
		}
	}
	m.items = kept
	m.stats.ExplicitDeletes += uint64(n)
	return n, nil
}

func (m *model) AdvanceClock(d time.Duration) error {
	if d < 0 {
		return ErrClockRegression
	}
	m.now = m.now.Add(d)
	return nil
}

func (m *model) dump() []Entry {
	out := make([]Entry, len(m.items))
	copy(out, m.items)
	sort.Slice(out, func(i, j int) bool { return entryLess(out[i], out[j]) })
	return out
}

func entryEqual(a, b Entry) bool {
	if a.Name != b.Name || a.Value != b.Value || a.Site != b.Site || a.Path != b.Path ||
		a.Secure != b.Secure || a.HTTPOnly != b.HTTPOnly || a.SameSite != b.SameSite ||
		!a.CreatedAt.Equal(b.CreatedAt) || !a.LastAccessedAt.Equal(b.LastAccessedAt) ||
		a.seq != b.seq {
		return false
	}
	if (a.ExpiresAt == nil) != (b.ExpiresAt == nil) {
		return false
	}
	if a.ExpiresAt != nil && !a.ExpiresAt.Equal(*b.ExpiresAt) {
		return false
	}
	return partitionEqual(a.PartitionKey, b.PartitionKey)
}

func checkState(t *testing.T, op int, st *Store, m *model) {
	t.Helper()
	gotStats, wantStats := st.Stats(), m.stats
	if gotStats != wantStats {
		t.Fatalf("op=%d 统计不一致: store=%+v model=%+v", op, gotStats, wantStats)
	}
	got, want := st.DebugEntries(), m.dump()
	if len(got) != len(want) {
		t.Fatalf("op=%d 条目数不一致: store=%d model=%d", op, len(got), len(want))
	}
	for i := range got {
		if !entryEqual(got[i], want[i]) {
			t.Logf("store 全量: %+v", got)
			t.Logf("model 全量: %+v", want)
			t.Fatalf("op=%d 条目不一致:\nstore=%+v\nmodel=%+v", op, got[i], want[i])
		}
	}
	// 不变量：三类删除 + 当前条目数 == 累计新增；站点与全局上限。
	if gotStats.ExpiredEvictions+gotStats.CapacityEvictions+gotStats.ExplicitDeletes+uint64(len(got)) != gotStats.TotalAdds {
		t.Fatalf("op=%d 计数不守恒: %+v len=%d", op, gotStats, len(got))
	}
}

func checkErr(t *testing.T, op int, got, want error) {
	t.Helper()
	if (got == nil) != (want == nil) || (got != nil && !errors.Is(got, want)) {
		t.Fatalf("op=%d 错误不一致: store=%v model=%v", op, got, want)
	}
}

func checkEntries(t *testing.T, op int, what string, got, want []Entry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("op=%d %s 结果数不一致: store=%d model=%d", op, what, len(got), len(want))
	}
	for i := range got {
		if !entryEqual(got[i], want[i]) {
			t.Fatalf("op=%d %s 第 %d 条不一致:\nstore=%+v\nmodel=%+v", op, what, i, got[i], want[i])
		}
	}
}

// 与朴素模型对照的随机操作序列差分测试；日志打印输入、输出与判定依据。
func TestModelDifferential(t *testing.T) {
	cfg := Config{SiteLimit: 6, GlobalLimit: 15, LaxGrace: 90 * time.Second}
	st := New(cfg)
	m := newModel(cfg)
	rng := rand.New(rand.NewSource(1524))

	sites := []string{"a.com", "b.com", "c.com"}
	paths := []string{"/", "/a", "/a/b", "/ab"}
	names := []string{"n1", "n2", "n3", "__Host-h"}
	parts := []*string{nil, sp("p1"), sp("p2")}
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }

	for i := 0; i < 4000; i++ {
		switch op := rng.Intn(100); {
		case op < 45: // 写入
			in := SetInput{
				Name:         pick(names),
				Value:        fmt.Sprintf("v%d", rng.Intn(5)),
				Site:         pick(sites),
				Path:         pick(paths),
				Secure:       rng.Intn(2) == 0,
				HTTPOnly:     rng.Intn(3) == 0,
				SameSite:     SameSite(rng.Intn(4)),
				PartitionKey: parts[rng.Intn(len(parts))],
				SourceSite:   pick(sites),
				SourceSecure: rng.Intn(10) < 7,
			}
			if rng.Intn(20) == 0 {
				in.Name = ""
			}
			if rng.Intn(20) == 0 {
				in.Path = "bad"
			}
			if rng.Intn(20) == 0 {
				in.SameSite = SameSite(9)
			}
			if rng.Intn(20) == 0 {
				in.At = tp(m.now.Add(-time.Second))
			}
			if rng.Intn(10) < 4 {
				in.ExpiresAt = tp(m.now.Add(time.Duration(rng.Intn(720)-120) * time.Second))
			}
			errStore := st.Set(in)
			errModel := m.Set(in)
			checkErr(t, i, errStore, errModel)
			t.Logf("op=%d SET %s/%s path=%s secure=%v/%v ss=%v exp=%v part=%v src=%s/%v -> %v",
				i, in.Site, in.Name, in.Path, in.Secure, in.HTTPOnly, in.SameSite,
				in.ExpiresAt != nil, in.PartitionKey, in.SourceSite, in.SourceSecure, errStore)
		case op < 65: // 请求附带判定
			r := Request{
				Site:          pick(sites),
				Path:          pick(paths),
				Secure:        rng.Intn(10) < 7,
				InitiatorSite: pick(sites),
				TopLevelNav:   rng.Intn(2) == 0,
				SafeMethod:    rng.Intn(2) == 0,
				PartitionKey:  parts[rng.Intn(len(parts))],
			}
			got, errStore := st.Attach(r)
			want, errModel := m.Attach(r)
			checkErr(t, i, errStore, errModel)
			checkEntries(t, i, "Attach", got, want)
			names := make([]string, len(got))
			for j, e := range got {
				names[j] = e.Name
			}
			t.Logf("op=%d ATTACH %s%s sec=%v init=%s nav=%v safe=%v part=%v -> %v",
				i, r.Site, r.Path, r.Secure, r.InitiatorSite, r.TopLevelNav, r.SafeMethod, r.PartitionKey, names)
		case op < 75: // 脚本读取
			site, path := pick(sites), pick(paths)
			secure := rng.Intn(2) == 0
			part := parts[rng.Intn(len(parts))]
			got, errStore := st.ScriptRead(site, path, secure, part)
			want, errModel := m.ScriptRead(site, path, secure, part)
			checkErr(t, i, errStore, errModel)
			checkEntries(t, i, "ScriptRead", got, want)
			t.Logf("op=%d SCRIPT %s%s sec=%v -> %d 条", i, site, path, secure, len(got))
		case op < 85: // 三类清除
			switch rng.Intn(3) {
			case 0:
				site := pick(sites)
				nStore := st.ClearSite(site)
				nModel := m.ClearSite(site)
				if nStore != nModel {
					t.Fatalf("op=%d ClearSite(%s): store=%d model=%d", i, site, nStore, nModel)
				}
				t.Logf("op=%d CLEAR site=%s -> %d", i, site, nStore)
			case 1:
				part := parts[rng.Intn(len(parts))]
				nStore := st.ClearPartition(part)
				nModel := m.ClearPartition(part)
				if nStore != nModel {
					t.Fatalf("op=%d ClearPartition: store=%d model=%d", i, nStore, nModel)
				}
				t.Logf("op=%d CLEAR part=%v -> %d", i, part, nStore)
			case 2:
				start := m.now.Add(-time.Duration(rng.Intn(600)) * time.Second)
				end := start.Add(time.Duration(rng.Intn(600)) * time.Second)
				nStore, errStore := st.ClearCreatedRange(start, end)
				nModel, errModel := m.ClearCreatedRange(start, end)
				checkErr(t, i, errStore, errModel)
				if nStore != nModel {
					t.Fatalf("op=%d ClearCreatedRange: store=%d model=%d", i, nStore, nModel)
				}
				t.Logf("op=%d CLEAR range -> %d", i, nStore)
			}
		default: // 时钟推进（偶发回退）
			d := time.Duration(rng.Intn(120)) * time.Second
			if rng.Intn(30) == 0 {
				d = -time.Second
			}
			errStore := st.AdvanceClock(d)
			errModel := m.AdvanceClock(d)
			checkErr(t, i, errStore, errModel)
			t.Logf("op=%d CLOCK +=%v -> %v", i, d, errStore)
		}
		checkState(t, i, st, m)
	}
	t.Logf("终态: stats=%+v len=%d", st.Stats(), st.Len())
}

// 基准：附带判定开销不随无关站点条目总数增长。
func BenchmarkAttach(b *testing.B) {
	for _, bystanders := range []int{1_000, 50_000} {
		b.Run(fmt.Sprintf("bystanders=%d", bystanders), func(b *testing.B) {
			st := New(Config{})
			for i := 0; i < bystanders; i++ {
				_ = st.Set(baseInput(fmt.Sprintf("k%d", i), fmt.Sprintf("s%d.com", i%1000)))
			}
			for i := 0; i < 8; i++ {
				_ = st.Set(baseInput(fmt.Sprintf("t%d", i), "target.com"))
			}
			req := Request{Site: "target.com", Path: "/", Secure: true, InitiatorSite: "target.com"}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = st.Attach(req)
			}
		})
	}
}

// 基准：淘汰选择开销不随站点条目数线性增长。
func BenchmarkEviction(b *testing.B) {
	for _, size := range []int{1_000, 20_000} {
		b.Run(fmt.Sprintf("sitelimit=%d", size), func(b *testing.B) {
			st := New(Config{SiteLimit: size})
			for i := 0; i < size; i++ {
				_ = st.Set(baseInput(fmt.Sprintf("k%d", i), "a.com"))
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = st.Set(baseInput(fmt.Sprintf("x%d", i), "a.com"))
			}
		})
	}
}
