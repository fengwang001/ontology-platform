package bucket_test

// naive 模型：严格按规格文字独立实现的简单模拟，刻意不调用
// bucket/lock/version/authz 的任何实现逻辑，仅在相同操作序列下
// 与真实实现逐操作比较结果码、版本号、Get 与审计。

import "fmt"

const (
	nNone = iota
	nGov
	nComp
)

type nRec struct {
	ver    int64
	size   int64
	marker bool
	mode   int
	until  int64
	hold   bool
}

type nAudit struct {
	key      string
	ver      int64
	now      int64
	oldUntil int64
}

type nItem struct {
	key string
	ver int64
}

type naive struct {
	mode   int
	d      int64
	seq    int64
	now    int64
	recs   map[string][]*nRec
	byVer  map[int64]*nRec
	verKey map[int64]string
	aud    []nAudit
}

func newNaive(mode int, d int64) *naive {
	return &naive{
		mode:   mode,
		d:      d,
		recs:   map[string][]*nRec{},
		byVer:  map[int64]*nRec{},
		verKey: map[int64]string{},
	}
}

const nMaxNow = int64(1_000_000_000_000)

func nValidNow(now int64) bool { return now >= 0 && now <= nMaxNow }

func (n *naive) put(key string, size, now int64) (int64, string) {
	if key == "" || size < 0 || size > nMaxNow || !nValidNow(now) {
		return 0, "invalid"
	}
	if now < n.now {
		return 0, "regress"
	}
	n.seq++
	r := &nRec{ver: n.seq, size: size}
	if n.d > 0 {
		r.mode = n.mode
		r.until = now + n.d
	}
	n.recs[key] = append(n.recs[key], r)
	n.byVer[r.ver] = r
	n.verKey[r.ver] = key
	n.now = now
	return r.ver, "ok"
}

func (n *naive) del(key string, now int64) (int64, string) {
	if key == "" || !nValidNow(now) {
		return 0, "invalid"
	}
	if now < n.now {
		return 0, "regress"
	}
	n.seq++
	r := &nRec{ver: n.seq, marker: true}
	n.recs[key] = append(n.recs[key], r)
	n.byVer[r.ver] = r
	n.verKey[r.ver] = key
	n.now = now
	return r.ver, "ok"
}

func (n *naive) get(key string) (int64, string) {
	if key == "" {
		return 0, "invalid"
	}
	rs := n.recs[key]
	if len(rs) == 0 {
		return 0, "absent"
	}
	cur := rs[len(rs)-1]
	if cur.marker {
		return 0, "deleted"
	}
	return cur.ver, "ok"
}

func (n *naive) remove(key string, ver int64) {
	rs := n.recs[key]
	for i, r := range rs {
		if r.ver == ver {
			n.recs[key] = append(rs[:i], rs[i+1:]...)
			if len(n.recs[key]) == 0 {
				delete(n.recs, key)
			}
			break
		}
	}
	delete(n.byVer, ver)
	delete(n.verKey, ver)
}

func (n *naive) delCheck(key string, ver int64, hasByp, bypass bool, now int64) (*nRec, bool, string) {
	r := n.byVer[ver]
	if r == nil || n.verKey[ver] != key {
		return nil, false, "notexist"
	}
	if r.marker {
		return r, false, "ok"
	}
	if r.hold {
		return r, false, "hold"
	}
	if r.mode != nNone && now < r.until {
		if r.mode == nComp {
			return r, false, "compliance"
		}
		if bypass && hasByp {
			return r, true, "ok"
		}
		return r, false, "governance"
	}
	return r, false, "ok"
}

func (n *naive) delVersion(hasDel, hasByp bool, key string, ver int64, bypass bool, now int64) string {
	if key == "" || ver <= 0 || !nValidNow(now) {
		return "invalid"
	}
	if now < n.now {
		return "regress"
	}
	if !hasDel {
		return "perm"
	}
	r, audited, code := n.delCheck(key, ver, hasByp, bypass, now)
	if code != "ok" {
		return code
	}
	old := r.until
	n.remove(key, ver)
	if audited {
		n.aud = append(n.aud, nAudit{key: key, ver: ver, now: now, oldUntil: old})
	}
	n.now = now
	return "ok"
}

func (n *naive) setRetention(hasPut, hasByp bool, key string, ver int64, mode int, until int64, bypass bool, now int64) string {
	if key == "" || ver <= 0 || !nValidNow(now) {
		return "invalid"
	}
	if mode != nNone && mode != nGov && mode != nComp {
		return "invalid"
	}
	if mode != nNone && (until <= now || until > nMaxNow) {
		return "invalid"
	}
	if now < n.now {
		return "regress"
	}
	if !hasPut {
		return "perm"
	}
	r := n.byVer[ver]
	if r == nil || n.verKey[ver] != key || r.marker {
		return "notexist"
	}
	if r.mode != nNone && now < r.until {
		switch r.mode {
		case nComp:
			if mode != nComp || until < r.until {
				return "compliance"
			}
		case nGov:
			keepOrUpgrade := mode == nGov || mode == nComp
			if !keepOrUpgrade || until < r.until {
				if !(bypass && hasByp) {
					return "governance"
				}
			}
		}
	}
	if mode == nNone {
		r.mode, r.until = nNone, 0
	} else {
		r.mode, r.until = mode, until
	}
	n.now = now
	return "ok"
}

func (n *naive) setHold(hasPut bool, key string, ver int64, on bool, now int64) string {
	if key == "" || ver <= 0 || !nValidNow(now) {
		return "invalid"
	}
	if now < n.now {
		return "regress"
	}
	if !hasPut {
		return "perm"
	}
	r := n.byVer[ver]
	if r == nil || n.verKey[ver] != key || r.marker {
		return "notexist"
	}
	r.hold = on
	n.now = now
	return "ok"
}

func (n *naive) delVersions(hasDel, hasByp bool, items []nItem, bypass bool, now int64) string {
	if len(items) == 0 || len(items) > 1000 || !nValidNow(now) {
		return "invalid"
	}
	for _, it := range items {
		if it.key == "" || it.ver <= 0 {
			return "invalid"
		}
	}
	seen := map[string]bool{}
	for _, it := range items {
		id := fmt.Sprintf("%d:%s#%d", len(it.key), it.key, it.ver)
		if seen[id] {
			return "invalid"
		}
		seen[id] = true
	}
	if now < n.now {
		return "regress"
	}
	if !hasDel {
		return "perm"
	}
	type plan struct {
		key     string
		ver     int64
		audited bool
		until   int64
	}
	plans := make([]plan, 0, len(items))
	for i, it := range items {
		r, audited, code := n.delCheck(it.key, it.ver, hasByp, bypass, now)
		if code != "ok" {
			return fmt.Sprintf("batch:%d:%s", i, code)
		}
		plans = append(plans, plan{it.key, it.ver, audited, r.until})
	}
	for _, p := range plans {
		n.remove(p.key, p.ver)
		if p.audited {
			n.aud = append(n.aud, nAudit{p.key, p.ver, now, p.until})
		}
	}
	n.now = now
	return "ok"
}
