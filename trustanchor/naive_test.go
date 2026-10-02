package trustanchor

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveTracker 是按规则逐条直写的朴素模拟，用于对照。
// 与正式实现结构不同：切片存储、字符串状态、线性查找。
type naiveTracker struct {
	h, r       int64
	kmax, m, q int
	entries    []naiveEntry
	banned     []string
	maxNow     int64
}

type naiveEntry struct {
	id    string
	state string // "ADDPEND" | "VALID" | "MISSING" | "REVOKED"
	since int64
	at    int64
	cnt   int
}

func newNaive(cfg Config) *naiveTracker {
	n := &naiveTracker{h: cfg.AddHold, r: cfg.Retain, kmax: cfg.MaxTracked, m: cfg.MinAppear, q: cfg.Quorum}
	for _, a := range cfg.Anchors {
		n.entries = append(n.entries, naiveEntry{id: string(a), state: "VALID"})
	}
	return n
}

func (n *naiveTracker) find(entries []naiveEntry, id string) int {
	for i := range entries {
		if entries[i].id == id {
			return i
		}
	}
	return -1
}

func (n *naiveTracker) isBanned(banned []string, id string) bool {
	for _, b := range banned {
		if b == id {
			return true
		}
	}
	return false
}

// observe 返回 nil 表示接受，否则返回拒绝类别。
func (n *naiveTracker) observe(keySet []KeyItem, signers []string, now int64) error {
	// 第一关：参数非法。
	if now < 0 || now > MaxNow || len(keySet) < 1 || len(keySet) > MaxKeySet {
		return newError(ErrInvalidArgument, "naive: 参数非法")
	}
	for i, item := range keySet {
		if len(item.ID) == 0 {
			return newError(ErrInvalidArgument, "naive: 空标识")
		}
		for j := i + 1; j < len(keySet); j++ {
			if string(keySet[j].ID) == string(item.ID) {
				return newError(ErrInvalidArgument, "naive: 标识重复")
			}
		}
	}
	for _, s := range signers {
		if s == "" {
			return newError(ErrInvalidArgument, "naive: 空签名者")
		}
	}
	// 第二关：时钟回退。
	if now < n.maxNow {
		return newError(ErrClockRollback, "naive: 时钟回退")
	}
	// 第三关：签名者不可信（按观测开始前状态，去重）。
	{
		dedup := map[string]bool{}
		trusted := 0
		for _, s := range signers {
			if dedup[s] {
				continue
			}
			dedup[s] = true
			idx := n.find(n.entries, s)
			if idx >= 0 && n.entries[idx].state == "VALID" {
				trusted++
			}
		}
		if trusted < n.q {
			return newError(ErrUntrustedSigners, "naive: 签名者不可信")
		}
	}

	// 副本上处理。
	entries := make([]naiveEntry, len(n.entries))
	copy(entries, n.entries)
	banned := append([]string(nil), n.banned...)

	// 第一步：被跟踪而不在密钥集中的密钥。
	inSet := map[string]bool{}
	for _, item := range keySet {
		inSet[string(item.ID)] = true
	}
	kept := entries[:0]
	for _, e := range entries {
		if !inSet[e.id] {
			switch e.state {
			case "VALID":
				e.state, e.since = "MISSING", now
			case "ADDPEND":
				continue // 变未跟踪
			}
		}
		kept = append(kept, e)
	}
	entries = kept

	// 第二步：密钥集中的每一项。
	for _, item := range keySet {
		id := string(item.ID)
		if n.isBanned(banned, id) {
			continue
		}
		idx := n.find(entries, id)
		if item.Revoked {
			if idx >= 0 {
				switch entries[idx].state {
				case "VALID", "MISSING":
					entries[idx].state, entries[idx].at = "REVOKED", now
				case "ADDPEND":
					entries = append(entries[:idx], entries[idx+1:]...)
				}
			}
			continue
		}
		if idx < 0 {
			entries = append(entries, naiveEntry{id: id, state: "ADDPEND", since: now, cnt: 1})
			continue
		}
		switch entries[idx].state {
		case "ADDPEND":
			entries[idx].cnt++
		case "MISSING":
			entries[idx].state, entries[idx].since = "VALID", 0
		}
	}

	// 第三步（同一 now）。
	kept = entries[:0]
	for _, e := range entries {
		switch e.state {
		case "ADDPEND":
			if now >= e.since+n.h && e.cnt >= n.m {
				e.state, e.since, e.cnt = "VALID", 0, 0
			}
		case "REVOKED":
			if now >= e.at+n.r {
				banned = append(banned, e.id)
				continue
			}
		case "MISSING":
			if now >= e.since+n.r {
				continue
			}
		}
		kept = append(kept, e)
	}
	entries = kept

	// 锁死先于超限。
	valid := 0
	for _, e := range entries {
		if e.state == "VALID" {
			valid++
		}
	}
	if valid < n.q {
		return newError(ErrLockup, "naive: 锁死")
	}
	if len(entries) > n.kmax {
		return newError(ErrOverflow, "naive: 超限")
	}
	n.entries = entries
	n.banned = banned
	n.maxNow = now
	return nil
}

func (n *naiveTracker) status(id string) KeyStatus {
	if n.isBanned(n.banned, id) {
		return KeyStatus{State: StateBanned}
	}
	idx := n.find(n.entries, id)
	if idx < 0 {
		return KeyStatus{State: StateUntracked}
	}
	e := n.entries[idx]
	st := KeyStatus{Since: e.since, At: e.at, Cnt: e.cnt}
	switch e.state {
	case "ADDPEND":
		st.State = StateAddPend
	case "VALID":
		st.State = StateValid
	case "MISSING":
		st.State = StateMissing
	case "REVOKED":
		st.State = StateRevoked
	}
	return st
}

func errKindOf(err error) string {
	if err == nil {
		return "OK"
	}
	k, ok := KindOf(err)
	if !ok {
		return "UNKNOWN"
	}
	return k.String()
}

func describeSet(keySet []KeyItem) string {
	out := "["
	for i, item := range keySet {
		if i > 0 {
			out += " "
		}
		if item.Revoked {
			out += string(item.ID) + "!"
		} else {
			out += string(item.ID)
		}
	}
	return out + "]"
}

// 与朴素模拟对照 2000 组随机观测序列，并验证重放确定性。
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	pool := []string{"k0", "k1", "k2", "k3", "k4", "k5", "k6", "k7"}

	for seq := 0; seq < 2000; seq++ {
		kmax := 1 + rng.Intn(6)
		cfg := Config{
			AddHold:    int64(rng.Intn(5)), // 含 H=0
			Retain:     int64(1 + rng.Intn(4)),
			MaxTracked: kmax,
			MinAppear:  1 + rng.Intn(3),
			Quorum:     1 + rng.Intn(kmax),
		}
		nAnchors := cfg.Quorum + rng.Intn(kmax-cfg.Quorum+1)
		perm := rng.Perm(len(pool))
		for i := 0; i < nAnchors; i++ {
			cfg.Anchors = append(cfg.Anchors, []byte(pool[perm[i]]))
		}

		tr1 := mustNew(t, cfg)
		tr2 := mustNew(t, cfg) // 重放对照
		nv := newNaive(cfg)

		var maxNow int64
		nObs := 1 + rng.Intn(25)
		for obs := 0; obs < nObs; obs++ {
			// 随机密钥集：1..5 个不同标识，少量带吊销位。
			nItems := 1 + rng.Intn(5)
			itemPerm := rng.Perm(len(pool))
			var keySet []KeyItem
			for i := 0; i < nItems; i++ {
				keySet = append(keySet, KeyItem{
					ID:      []byte(pool[itemPerm[i]]),
					Revoked: rng.Intn(100) < 15,
				})
			}
			// 随机签名者：可重复、可含未知标识、可为空。
			var signers [][]byte
			var signerStrs []string
			for i := 0; i < rng.Intn(4); i++ {
				s := pool[rng.Intn(len(pool))]
				if rng.Intn(100) < 10 {
					s = "x" // 未知标识
				}
				signers = append(signers, []byte(s))
				signerStrs = append(signerStrs, s)
			}
			// now：多数单调前进，偶尔回退。
			now := maxNow + int64(rng.Intn(6))
			if rng.Intn(100) < 10 && maxNow > 0 {
				now = rng.Int63n(maxNow)
			}

			err1 := tr1.Observe(keySet, signers, now)
			err2 := tr2.Observe(keySet, signers, now)
			errN := nv.observe(keySet, signerStrs, now)

			kind1, kind2, kindN := errKindOf(err1), errKindOf(err2), errKindOf(errN)
			validNow := 0
			for _, ks := range tr1.keys {
				if ks.state == StateValid {
					validNow++
				}
			}
			t.Logf("seq=%d obs=%d now=%d set=%s signers=%v => tracker=%s naive=%s (依据: VALID=%d Q=%d tracked=%d Kmax=%d banned=%d)",
				seq, obs, now, describeSet(keySet), signerStrs, kind1, kindN,
				validNow, cfg.Quorum, len(tr1.keys), cfg.MaxTracked, len(tr1.banned))
			if kind1 != kindN || kind1 != kind2 {
				t.Fatalf("seq=%d obs=%d 判定不一致: tracker=%s replay=%s naive=%s",
					seq, obs, kind1, kind2, kindN)
			}
			if tr1.scanCount > tr1.scanBound {
				t.Fatalf("seq=%d obs=%d scanCount=%d 超过界 %d", seq, obs, tr1.scanCount, tr1.scanBound)
			}
			if err1 == nil {
				maxNow = now
				// 安全不变量：VALID 个数不低于 Q。
				valid := 0
				for _, ks := range tr1.keys {
					if ks.state == StateValid {
						valid++
					}
				}
				if valid < cfg.Quorum {
					t.Fatalf("seq=%d obs=%d 不变量破坏: VALID=%d < Q=%d", seq, obs, valid, cfg.Quorum)
				}
			}
			// 全状态对照（含 BANNED 与计时起点）。
			for _, id := range pool {
				s1 := tr1.State([]byte(id))
				s2 := tr2.State([]byte(id))
				sn := nv.status(id)
				if s1 != sn || s1 != s2 {
					t.Fatalf("seq=%d obs=%d id=%s 状态不一致: tracker=%+v replay=%+v naive=%+v",
						seq, obs, id, s1, s2, sn)
				}
			}
		}
		if seq%400 == 0 {
			keys := make([]string, 0, len(tr1.keys))
			for k := range tr1.keys {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			t.Logf("seq=%d 结束: cfg=%+v tracked=%v banned=%v", seq, cfg, keys, fmt.Sprint(tr1.banned))
		}
	}
}
