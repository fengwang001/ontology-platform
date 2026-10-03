package suppression

import (
	"fmt"
	"math/rand"
	"testing"
)

// 朴素模拟：按规格逐条写成的独立参照实现，与 Suppressor 对拍。

type modelAddr struct {
	reason      Cause
	since       int64
	softUntil   int64
	log         []int64
	lastHard    int64
	tokSeq      uint64
	tokIssuedAt int64
	hasTok      bool
	confirmedAt int64
}

type modelDomain struct {
	domUntil int64
	domSince int64
	addrs    map[string]bool
}

type model struct {
	cfg    Config
	maxNow int64
	seq    uint64
	addr   map[string]*modelAddr
	dom    map[string]*modelDomain
}

func newModel(cfg Config) *model {
	return &model{
		cfg:  cfg,
		addr: make(map[string]*modelAddr),
		dom:  make(map[string]*modelDomain),
	}
}

func (m *model) domainOf(key string) *modelDomain {
	name := key[len(key)-1:]
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '@' {
			name = key[i+1:]
			break
		}
	}
	d, ok := m.dom[name]
	if !ok {
		d = &modelDomain{addrs: make(map[string]bool)}
		m.dom[name] = d
	}
	return d
}

func (m *model) state(key string) *modelAddr {
	a, ok := m.addr[key]
	if !ok {
		a = &modelAddr{lastHard: -1, confirmedAt: -1}
		m.addr[key] = a
		m.domainOf(key).addrs[key] = true
	}
	return a
}

func (m *model) lazy(a *modelAddr, now int64) {
	if a.reason == CauseSoft && now >= a.softUntil {
		a.reason = CauseNone
		a.log = nil
	}
}

func (m *model) prepare(addr string, now int64) (string, *modelAddr, error) {
	key, err := Normalize(addr)
	if err != nil {
		return "", nil, err
	}
	if now < m.maxNow {
		return "", nil, ErrClockBackwards
	}
	return key, m.state(key), nil
}

func (m *model) Soft(addr string, now int64) error {
	_, a, err := m.prepare(addr, now)
	if err != nil {
		return err
	}
	m.maxNow = now
	m.lazy(a, now)
	if a.reason != CauseNone {
		return nil
	}
	var kept []int64
	for _, t := range a.log {
		if t > now-m.cfg.SoftWindow {
			kept = append(kept, t)
		}
	}
	a.log = append(kept, now)
	if int64(len(a.log)) >= m.cfg.SoftThreshold {
		a.reason = CauseSoft
		a.since = now
		a.softUntil = now + m.cfg.SoftTTL
		a.log = nil
	}
	return nil
}

func (m *model) Hard(addr string, now int64) error {
	key, a, err := m.prepare(addr, now)
	if err != nil {
		return err
	}
	m.maxNow = now
	m.lazy(a, now)
	a.lastHard = now
	if a.reason < CauseHard {
		a.reason = CauseHard
		a.since = now
		a.log = nil
	}
	d := m.domainOf(key)
	var count int64
	for other := range d.addrs {
		if m.addr[other].lastHard > now-m.cfg.DomainWindow {
			count++
		}
	}
	if count >= m.cfg.DomainThreshold {
		if d.domUntil <= now {
			d.domSince = now
		}
		if d.domUntil < now+m.cfg.DomainTTL {
			d.domUntil = now + m.cfg.DomainTTL
		}
	}
	return nil
}

func (m *model) Complaint(addr string, now int64) error {
	_, a, err := m.prepare(addr, now)
	if err != nil {
		return err
	}
	m.maxNow = now
	m.lazy(a, now)
	if a.reason < CauseComplaint {
		a.reason = CauseComplaint
		a.since = now
	}
	return nil
}

func (m *model) Unsub(addr string, now int64) error {
	_, a, err := m.prepare(addr, now)
	if err != nil {
		return err
	}
	m.maxNow = now
	m.lazy(a, now)
	if a.reason == CauseNone || a.reason == CauseSoft {
		a.reason = CauseUnsub
		a.since = now
	}
	return nil
}

func (m *model) RequestConfirm(addr string, now int64) (uint64, error) {
	_, a, err := m.prepare(addr, now)
	if err != nil {
		return 0, err
	}
	m.lazy(a, now)
	if a.reason == CauseComplaint {
		return 0, ErrRecoveryForbidden
	}
	if a.reason != CauseHard && a.reason != CauseUnsub {
		return 0, ErrRecoveryUnneeded
	}
	m.maxNow = now
	m.seq++
	a.tokSeq, a.tokIssuedAt, a.hasTok = m.seq, now, true
	return m.seq, nil
}

func (m *model) Confirm(addr string, seq uint64, now int64) error {
	_, a, err := m.prepare(addr, now)
	if err != nil {
		return err
	}
	m.lazy(a, now)
	switch {
	case !a.hasTok:
		return ErrNoToken
	case seq != a.tokSeq:
		return ErrStaleToken
	case now >= a.tokIssuedAt+m.cfg.TokenTTL:
		return ErrTokenExpired
	case a.reason == CauseComplaint:
		return ErrConfirmForbidden
	case a.reason != CauseHard && a.reason != CauseUnsub:
		return ErrConfirmUnneeded
	case a.tokIssuedAt <= a.since:
		return ErrTokenPreceded
	}
	m.maxNow = now
	a.reason = CauseNone
	a.log = nil
	a.hasTok = false
	a.confirmedAt = now
	return nil
}

func (m *model) IsSuppressed(addr string, now int64) (bool, Cause, error) {
	key, a, err := m.prepare(addr, now)
	if err != nil {
		return false, CauseNone, err
	}
	m.lazy(a, now)
	if a.reason != CauseNone {
		return true, a.reason, nil
	}
	d := m.domainOf(key)
	if d.domUntil > now && a.confirmedAt < d.domSince {
		return true, CauseDomain, nil
	}
	return false, CauseNone, nil
}

// 对拍：2000 组随机事件序列，逐步比较 Suppressor 与朴素模拟的输出。
func TestFuzzAgainstModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	addrPool := []string{
		"a.b+x@Gmail.com", "AB@googlemail.com", "ab@gmail.com",
		"x@corp.com", "y@corp.com", "z@corp.com", "w@corp.com",
		"p@other.com", "q@other.com",
		"bad", "a@b", "", // 非法地址
	}
	const sequences = 2000
	for seqNo := 0; seqNo < sequences; seqNo++ {
		cfg := Config{
			SoftThreshold:   1 + rng.Int63n(4),
			SoftWindow:      1 + rng.Int63n(30),
			SoftTTL:         1 + rng.Int63n(60),
			TokenTTL:        1 + rng.Int63n(50),
			DomainThreshold: 1 + rng.Int63n(3),
			DomainWindow:    1 + rng.Int63n(40),
			DomainTTL:       1 + rng.Int63n(80),
		}
		s := mustNew(t, cfg)
		m := newModel(cfg)
		now := int64(0)
		issued := map[string][]uint64{}
		ops := 20 + rng.Intn(20)
		for step := 0; step < ops; step++ {
			addr := addrPool[rng.Intn(len(addrPool))]
			now += rng.Int63n(40)
			if rng.Intn(20) == 0 { // 5% 概率时钟回退
				now -= rng.Int63n(200)
			}
			op := rng.Intn(100)
			log := func(out string) {
				t.Logf("seq=%d step=%d now=%d addr=%q cfg=%+v -> %s", seqNo, step, now, addr, cfg, out)
			}
			switch {
			case op < 20:
				gotErr := s.Soft(addr, now)
				wantErr := m.Soft(addr, now)
				log(fmt.Sprintf("Soft err=%v (判定依据: %v)", gotErr, wantErr))
				if gotErr != wantErr {
					t.Fatalf("seq=%d step=%d Soft(%q,%d): got %v, want %v", seqNo, step, addr, now, gotErr, wantErr)
				}
			case op < 35:
				gotErr := s.Hard(addr, now)
				wantErr := m.Hard(addr, now)
				log(fmt.Sprintf("Hard err=%v (判定依据: %v)", gotErr, wantErr))
				if gotErr != wantErr {
					t.Fatalf("seq=%d step=%d Hard(%q,%d): got %v, want %v", seqNo, step, addr, now, gotErr, wantErr)
				}
				if gotErr == nil {
					key, _ := Normalize(addr)
					domName := key[len(key)-1:]
					for i := len(key) - 1; i >= 0; i-- {
						if key[i] == '@' {
							domName = key[i+1:]
							break
						}
					}
					if n := len(s.dom[domName].addrs); s.lastDomainScan > n {
						t.Fatalf("seq=%d step=%d: lastDomainScan=%d 超过该域名地址数 %d", seqNo, step, s.lastDomainScan, n)
					}
				}
			case op < 40:
				gotErr := s.Complaint(addr, now)
				wantErr := m.Complaint(addr, now)
				log(fmt.Sprintf("Complaint err=%v (判定依据: %v)", gotErr, wantErr))
				if gotErr != wantErr {
					t.Fatalf("seq=%d step=%d Complaint(%q,%d): got %v, want %v", seqNo, step, addr, now, gotErr, wantErr)
				}
			case op < 50:
				gotErr := s.Unsub(addr, now)
				wantErr := m.Unsub(addr, now)
				log(fmt.Sprintf("Unsub err=%v (判定依据: %v)", gotErr, wantErr))
				if gotErr != wantErr {
					t.Fatalf("seq=%d step=%d Unsub(%q,%d): got %v, want %v", seqNo, step, addr, now, gotErr, wantErr)
				}
			case op < 62:
				gotSeq, gotErr := s.RequestConfirm(addr, now)
				wantSeq, wantErr := m.RequestConfirm(addr, now)
				log(fmt.Sprintf("RequestConfirm seq=%d err=%v (判定依据: seq=%d err=%v)", gotSeq, gotErr, wantSeq, wantErr))
				if gotSeq != wantSeq || gotErr != wantErr {
					t.Fatalf("seq=%d step=%d RequestConfirm(%q,%d): got (%d,%v), want (%d,%v)",
						seqNo, step, addr, now, gotSeq, gotErr, wantSeq, wantErr)
				}
				if gotErr == nil {
					issued[addr] = append(issued[addr], gotSeq)
				}
			case op < 78:
				var tok uint64
				if list := issued[addr]; len(list) > 0 && rng.Intn(10) < 7 {
					tok = list[rng.Intn(len(list))]
				} else {
					tok = uint64(rng.Intn(5))
				}
				gotErr := s.Confirm(addr, tok, now)
				wantErr := m.Confirm(addr, tok, now)
				log(fmt.Sprintf("Confirm tok=%d err=%v (判定依据: %v)", tok, gotErr, wantErr))
				if gotErr != wantErr {
					t.Fatalf("seq=%d step=%d Confirm(%q,%d,%d): got %v, want %v", seqNo, step, addr, tok, now, gotErr, wantErr)
				}
			default:
				gotSup, gotCause, gotErr := s.IsSuppressed(addr, now)
				wantSup, wantCause, wantErr := m.IsSuppressed(addr, now)
				log(fmt.Sprintf("IsSuppressed = (%v,%v,%v) (判定依据: %v)", gotSup, gotCause, gotErr, wantCause))
				if gotSup != wantSup || gotCause != wantCause || gotErr != wantErr {
					t.Fatalf("seq=%d step=%d IsSuppressed(%q,%d): got (%v,%v,%v), want (%v,%v,%v)",
						seqNo, step, addr, now, gotSup, gotCause, gotErr, wantSup, wantCause, wantErr)
				}
			}
		}
	}
}
