package lessor

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// simLease 是朴素模拟中的租约。
type simLease struct {
	id       int64
	g, sv, x int64
	keys     map[string]bool
}

// sim 是按规格逐条写成的朴素模拟：无堆，Tick 用全量排序。
type sim struct {
	cfg     Config
	primary bool
	T       int64
	leases  map[int64]*simLease
	keyTo   map[string]int64
}

func newSim(cfg Config) *sim {
	return &sim{cfg: cfg, leases: map[int64]*simLease{}, keyTo: map[string]int64{}}
}

func (s *sim) checkTime(now int64) error {
	if now < 0 || now > maxNow || now < s.T {
		return ErrInvalidTime
	}
	return nil
}

func (s *sim) Grant(id, ttl, now int64) (int64, error) {
	if id < 1 {
		return 0, ErrInvalidID
	}
	if ttl < 1 || ttl > s.cfg.MaxTTL {
		return 0, ErrInvalidTTL
	}
	if err := s.checkTime(now); err != nil {
		return 0, err
	}
	if !s.primary {
		return 0, ErrNotPrimary
	}
	if _, ok := s.leases[id]; ok {
		return 0, ErrLeaseExists
	}
	g := max(ttl, s.cfg.MinTTL)
	s.leases[id] = &simLease{id: id, g: g, x: now + g, keys: map[string]bool{}}
	s.T = now
	return g, nil
}

func (s *sim) Renew(id, now int64) (int64, error) {
	if id < 1 {
		return 0, ErrInvalidID
	}
	if err := s.checkTime(now); err != nil {
		return 0, err
	}
	if !s.primary {
		return 0, ErrNotPrimary
	}
	ls, ok := s.leases[id]
	if !ok {
		return 0, ErrLeaseMissing
	}
	if ls.x <= now {
		return 0, ErrLeaseExpired
	}
	ls.x = now + ls.g
	ls.sv = 0
	s.T = now
	return ls.g, nil
}

func (s *sim) Attach(key string, id, now int64) error {
	if id < 1 {
		return ErrInvalidID
	}
	if key == "" {
		return ErrEmptyKey
	}
	if err := s.checkTime(now); err != nil {
		return err
	}
	if !s.primary {
		return ErrNotPrimary
	}
	ls, ok := s.leases[id]
	if !ok {
		return ErrLeaseMissing
	}
	if ls.x <= now {
		return ErrLeaseExpired
	}
	if ls.keys[key] {
		s.T = now
		return nil
	}
	if len(ls.keys) >= s.cfg.Kmax {
		return ErrLeaseFull
	}
	if old, ok := s.keyTo[key]; ok {
		delete(s.leases[old].keys, key)
	}
	ls.keys[key] = true
	s.keyTo[key] = id
	s.T = now
	return nil
}

func (s *sim) Tick(now int64) ([]Revoked, error) {
	if err := s.checkTime(now); err != nil {
		return nil, err
	}
	if !s.primary {
		return nil, ErrNotPrimary
	}
	var expired []*simLease
	for _, ls := range s.leases {
		if ls.x <= now {
			expired = append(expired, ls)
		}
	}
	sort.Slice(expired, func(i, j int) bool {
		a, b := expired[i], expired[j]
		if a.x != b.x {
			return a.x < b.x
		}
		return a.id < b.id
	})
	var out []Revoked
	for i, ls := range expired {
		if i >= s.cfg.R {
			break
		}
		out = append(out, s.destroy(ls))
	}
	s.T = now
	return out, nil
}

func (s *sim) Revoke(id, now int64) ([]string, error) {
	if id < 1 {
		return nil, ErrInvalidID
	}
	if err := s.checkTime(now); err != nil {
		return nil, err
	}
	if !s.primary {
		return nil, ErrNotPrimary
	}
	ls, ok := s.leases[id]
	if !ok {
		return nil, ErrLeaseMissing
	}
	rev := s.destroy(ls)
	s.T = now
	return rev.Keys, nil
}

func (s *sim) destroy(ls *simLease) Revoked {
	keys := make([]string, 0, len(ls.keys))
	for k := range ls.keys {
		keys = append(keys, k)
		delete(s.keyTo, k)
	}
	sort.Strings(keys)
	delete(s.leases, ls.id)
	return Revoked{ID: ls.id, Keys: keys}
}

func (s *sim) Checkpoint(now int64) error {
	if err := s.checkTime(now); err != nil {
		return err
	}
	if !s.primary {
		return ErrNotPrimary
	}
	for _, ls := range s.leases {
		if ls.x > now {
			ls.sv = ls.x - now
		}
	}
	s.T = now
	return nil
}

func (s *sim) Demote(now int64) error {
	if err := s.checkTime(now); err != nil {
		return err
	}
	if !s.primary {
		return ErrAlreadyFoll
	}
	s.primary = false
	s.T = now
	return nil
}

func (s *sim) Promote(now int64) error {
	if err := s.checkTime(now); err != nil {
		return err
	}
	if s.primary {
		return ErrAlreadyPrim
	}
	for _, ls := range s.leases {
		rest := ls.g
		if ls.sv > 0 {
			rest = ls.sv
		}
		ls.x = now + s.cfg.E + rest
	}
	s.primary = true
	s.T = now
	return nil
}

func (s *sim) TTL(id, now int64) (int64, error) {
	if id < 1 {
		return 0, ErrInvalidID
	}
	if err := s.checkTime(now); err != nil {
		return 0, err
	}
	ls, ok := s.leases[id]
	if !ok {
		return 0, ErrLeaseMissing
	}
	if s.primary {
		return max(ls.x-now, 0), nil
	}
	if ls.sv > 0 {
		return ls.sv, nil
	}
	return ls.g, nil
}

// nextNow 生成下一个 now：多数合法且推进水位，少数非法以测试拒绝路径。
func nextNow(rng *rand.Rand, T int64) int64 {
	switch r := rng.Intn(100); {
	case r < 85:
		return T + rng.Int63n(16)
	case r < 90:
		return T // 恰等于水位
	case r < 96:
		return T - 1 - rng.Int63n(4) // 小于水位，可能为负
	case r < 98:
		return maxNow + 1 // 越上界
	default:
		return maxNow
	}
}

func normKeys(keys []string) []string {
	if keys == nil {
		return []string{}
	}
	return keys
}

func normRevoked(rs []Revoked) []Revoked {
	if rs == nil {
		return []Revoked{}
	}
	for i := range rs {
		rs[i].Keys = normKeys(rs[i].Keys)
	}
	return rs
}

// TestRandomAgainstNaive 用 2000 组随机操作序列对照朴素模拟，
// 逐操作比较输出与错误，并在每组结束后比较完整状态。
func TestRandomAgainstNaive(t *testing.T) {
	keyPool := []string{"a", "b", "c", "d", "e", "f", "g", "h", ""}
	for seed := int64(1); seed <= 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cfg := Config{
			MinTTL: 1 + rng.Int63n(20),
			E:      rng.Int63n(11),
			R:      1 + rng.Intn(5),
			Kmax:   1 + rng.Intn(6),
		}
		cfg.MaxTTL = cfg.MinTTL + rng.Int63n(60)
		l, err := New(cfg)
		if err != nil {
			t.Fatalf("seed=%d 配置 %+v 应合法: %v", seed, cfg, err)
		}
		s := newSim(cfg)
		nOps := 20 + rng.Intn(61)
		trace := seed == 1 // 第一组打印完整轨迹：输入、输出与判定依据
		if trace {
			t.Logf("序列 seed=%d cfg=%+v 操作数=%d", seed, cfg, nOps)
		}
		for i := 0; i < nOps; i++ {
			now := nextNow(rng, s.T)
			id := int64(1 + rng.Intn(8))
			key := keyPool[rng.Intn(len(keyPool))]
			ttl := rng.Int63n(cfg.MaxTTL + 3)
			op := rng.Intn(100)
			var desc, outcome string
			switch {
			case op < 18:
				desc = fmt.Sprintf("Grant(id=%d,ttl=%d,now=%d)", id, ttl, now)
				g1, e1 := l.Grant(id, ttl, now)
				g2, e2 := s.Grant(id, ttl, now)
				outcome = compareResults(t, seed, i, desc, g1, e1, g2, e2)
			case op < 32:
				desc = fmt.Sprintf("Renew(id=%d,now=%d)", id, now)
				g1, e1 := l.Renew(id, now)
				g2, e2 := s.Renew(id, now)
				outcome = compareResults(t, seed, i, desc, g1, e1, g2, e2)
			case op < 48:
				desc = fmt.Sprintf("Attach(key=%q,id=%d,now=%d)", key, id, now)
				e1 := l.Attach(key, id, now)
				e2 := s.Attach(key, id, now)
				outcome = compareResults(t, seed, i, desc, 0, e1, 0, e2)
			case op < 60:
				desc = fmt.Sprintf("Tick(now=%d)", now)
				r1, e1 := l.Tick(now)
				r2, e2 := s.Tick(now)
				outcome = compareResults(t, seed, i, desc, normRevoked(r1), e1, normRevoked(r2), e2)
				if e1 == nil && l.tickInspected > len(r1)+1 {
					t.Fatalf("seed=%d 第 %d 步 %s: 检视堆顶数 %d 超过 撤销数+1=%d",
						seed, i, desc, l.tickInspected, len(r1)+1)
				}
			case op < 68:
				desc = fmt.Sprintf("Revoke(id=%d,now=%d)", id, now)
				k1, e1 := l.Revoke(id, now)
				k2, e2 := s.Revoke(id, now)
				outcome = compareResults(t, seed, i, desc, normKeys(k1), e1, normKeys(k2), e2)
			case op < 76:
				desc = fmt.Sprintf("Checkpoint(now=%d)", now)
				e1 := l.Checkpoint(now)
				e2 := s.Checkpoint(now)
				outcome = compareResults(t, seed, i, desc, 0, e1, 0, e2)
			case op < 82:
				desc = fmt.Sprintf("Demote(now=%d)", now)
				e1 := l.Demote(now)
				e2 := s.Demote(now)
				outcome = compareResults(t, seed, i, desc, 0, e1, 0, e2)
			case op < 88:
				desc = fmt.Sprintf("Promote(now=%d)", now)
				e1 := l.Promote(now)
				e2 := s.Promote(now)
				outcome = compareResults(t, seed, i, desc, 0, e1, 0, e2)
			default:
				desc = fmt.Sprintf("TTL(id=%d,now=%d)", id, now)
				g1, e1 := l.TTL(id, now)
				g2, e2 := s.TTL(id, now)
				outcome = compareResults(t, seed, i, desc, g1, e1, g2, e2)
			}
			if trace {
				t.Logf("  第 %d 步 %s -> %s", i, desc, outcome)
			}
		}
		compareStates(t, seed, l, s)
		if trace {
			t.Logf("序列 seed=%d 状态一致: T=%d primary=%v 租约数=%d",
				seed, l.now, l.primary, len(l.leases))
		}
	}
}

// compareResults 比较一次操作的输出与错误；不一致时报告输入、双方输出与判定依据。
func compareResults[T any](t *testing.T, seed int64, step int, desc string,
	gotV T, gotE error, wantV T, wantE error) string {
	t.Helper()
	if gotE != wantE || (gotE == nil && !reflect.DeepEqual(gotV, wantV)) {
		t.Fatalf("seed=%d 第 %d 步 %s:\n 输入 %s\n 实现输出 (%v, %v)\n 模拟输出 (%v, %v)\n 判定依据: 两者应完全一致",
			seed, step, desc, desc, gotV, gotE, wantV, wantE)
	}
	if gotE != nil {
		return fmt.Sprintf("拒绝 err=%v", gotE)
	}
	return fmt.Sprintf("成功 输出=%v", gotV)
}

// compareStates 比较一组序列结束后的完整状态。
func compareStates(t *testing.T, seed int64, l *Lessor, s *sim) {
	t.Helper()
	if l.now != s.T {
		t.Fatalf("seed=%d 水位不一致: 实现 T=%d 模拟 T=%d", seed, l.now, s.T)
	}
	if l.primary != s.primary {
		t.Fatalf("seed=%d 角色不一致: 实现 %v 模拟 %v", seed, l.primary, s.primary)
	}
	if len(l.leases) != len(s.leases) {
		t.Fatalf("seed=%d 租约数不一致: 实现 %d 模拟 %d", seed, len(l.leases), len(s.leases))
	}
	for id, ls := range l.leases {
		sl, ok := s.leases[id]
		if !ok {
			t.Fatalf("seed=%d 租约 %d 仅存在于实现", seed, id)
		}
		if ls.g != sl.g || ls.sv != sl.sv || ls.x != sl.x {
			t.Fatalf("seed=%d 租约 %d 字段不一致: 实现 g=%d sv=%d x=%d，模拟 g=%d sv=%d x=%d",
				seed, id, ls.g, ls.sv, ls.x, sl.g, sl.sv, sl.x)
		}
		if len(ls.keys) != len(sl.keys) {
			t.Fatalf("seed=%d 租约 %d 键数不一致: 实现 %v 模拟 %v", seed, id, ls.keys, sl.keys)
		}
		for k := range ls.keys {
			if !sl.keys[k] {
				t.Fatalf("seed=%d 租约 %d 的键 %q 仅存在于实现", seed, id, k)
			}
		}
	}
	if !reflect.DeepEqual(l.keyTo, s.keyTo) {
		t.Fatalf("seed=%d 键挂靠不一致: 实现 %v 模拟 %v", seed, l.keyTo, s.keyTo)
	}
	// 堆与租约集一致且堆序成立
	if l.exp.Len() != len(l.leases) {
		t.Fatalf("seed=%d 堆大小 %d 与租约数 %d 不一致", seed, l.exp.Len(), len(l.leases))
	}
	for id, ls := range l.leases {
		idx, ok := l.exp.pos[id]
		if !ok || l.exp.items[idx] != ls {
			t.Fatalf("seed=%d 租约 %d 不在堆中或位置错误", seed, id)
		}
	}
	for i := 1; i < l.exp.Len(); i++ {
		if l.exp.Less(i, (i-1)/2) {
			t.Fatalf("seed=%d 堆序在下标 %d 处被破坏", seed, i)
		}
	}
}
