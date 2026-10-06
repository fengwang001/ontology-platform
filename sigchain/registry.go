package sigchain

import "sync"

// registry 维护密钥登记与「背书链是否锚定到根密钥」的求值。
// 信任定义：密钥 k 在时刻 t 受信，当且仅当
// t 落在 k 的 [ValidFrom, ValidUntil) 内、k 在 t 未吊销，且 chainTrusted(k)。
// chainTrusted 与查询时刻无关：k 是根密钥，或 k 有一条有效背书
// （背书时刻落在背书者有效且未吊销区间内）且背书者的链同样锚定到根。
type registry struct {
	keys      map[string]Key
	rootKeyID string
	hasRoot   bool
	// 备忘录有独立锁：裁决只持有服务的读锁，多个并发读者共享备忘。
	memoMu     sync.Mutex
	trustMemo  map[string]bool // 背书链结论备忘，任何登记变更后整体失效
	trustEvals int             // 备忘未命中次数，供性能测试断言
}

func newRegistry() *registry {
	return &registry{keys: make(map[string]Key), trustMemo: make(map[string]bool)}
}

func (r *registry) resetMemo() {
	r.memoMu.Lock()
	defer r.memoMu.Unlock()
	r.trustMemo = make(map[string]bool)
}

// chainTrusted 判定密钥的背书链是否锚定到根密钥，结果按密钥备忘：
// 沿背书链的任一环节断裂，链上所有密钥的结论同为不锚定，可整体回填。
// 登记时已拒绝成环，迭代必然终止。
func (r *registry) chainTrusted(id string) bool {
	r.memoMu.Lock()
	defer r.memoMu.Unlock()
	if v, ok := r.trustMemo[id]; ok {
		return v
	}
	r.trustEvals++
	var path []string
	cur := id
	var v bool
	for {
		if mv, ok := r.trustMemo[cur]; ok {
			v = mv
			break
		}
		path = append(path, cur)
		next, done, okv := r.chainStep(cur)
		if done {
			v = okv
			break
		}
		cur = next
	}
	for _, p := range path {
		r.trustMemo[p] = v
	}
	return v
}

// chainStep 求单步结论：返回（下一跳，是否终止，终止时的结论）。
func (r *registry) chainStep(id string) (next string, done bool, value bool) {
	if r.hasRoot && id == r.rootKeyID {
		return "", true, true
	}
	k, ok := r.keys[id]
	if !ok || k.Endorsement == nil {
		return "", true, false
	}
	e := k.Endorsement
	endorser, ok := r.keys[e.Endorser]
	if !ok {
		return "", true, false
	}
	if e.Time < endorser.ValidFrom ||
		(endorser.ValidUntil != nil && e.Time >= *endorser.ValidUntil) ||
		(endorser.RevokedAt != nil && e.Time >= *endorser.RevokedAt) {
		return "", true, false // 背书时刻越出背书者有效且未吊销区间，背书无效
	}
	return e.Endorser, false, false
}

// trustedAt 判定密钥在时刻 t 是否受信。
func (r *registry) trustedAt(id string, t Time) bool {
	k, ok := r.keys[id]
	if !ok {
		return false
	}
	if t < k.ValidFrom || (k.ValidUntil != nil && t >= *k.ValidUntil) {
		return false
	}
	if k.RevokedAt != nil && t >= *k.RevokedAt {
		return false
	}
	return r.chainTrusted(id)
}

// validateKeyParams 校验单把密钥登记的参数合法性（错误次序第 1 位）。
func validateKeyParams(k Key) error {
	if k.ID == "" || k.ValidFrom < 0 {
		return ErrInvalidParam
	}
	if k.ValidUntil != nil && *k.ValidUntil <= k.ValidFrom {
		return ErrInvalidParam // 失效必须严格晚于生效
	}
	if k.RevokedAt != nil && *k.RevokedAt < 0 {
		return ErrInvalidParam
	}
	if k.Endorsement != nil && (k.Endorsement.Endorser == "" || k.Endorsement.Time < 0) {
		return ErrInvalidParam
	}
	return nil
}

// sameKeyShape 比较两把密钥除吊销时刻外的登记信息是否一致。
func sameKeyShape(a, b Key) bool {
	if a.ID != b.ID || a.ValidFrom != b.ValidFrom {
		return false
	}
	if (a.ValidUntil == nil) != (b.ValidUntil == nil) ||
		(a.ValidUntil != nil && *a.ValidUntil != *b.ValidUntil) {
		return false
	}
	if (a.Endorsement == nil) != (b.Endorsement == nil) {
		return false
	}
	if a.Endorsement != nil && *a.Endorsement != *b.Endorsement {
		return false
	}
	return true
}

// hasCycleFrom 从 id 沿背书链（批量登记覆盖已登记信息）检查是否成环。
func hasCycleFrom(start string, batch, registered map[string]Key) bool {
	visited := make(map[string]bool)
	cur := start
	for {
		if visited[cur] {
			return true
		}
		visited[cur] = true
		k, ok := batch[cur]
		if !ok {
			k, ok = registered[cur]
		}
		if !ok || k.Endorsement == nil {
			return false
		}
		cur = k.Endorsement.Endorser
	}
}
