package ontology

// keyState 是一把密钥在某个瞬间的完整登记信息。
type keyState struct {
	id         string
	validFrom  int64
	validUntil *int64 // 闭开区间右端；nil 表示无失效时刻
	revokedAt  *int64
	endorsedBy *Endorsement
	chainOk    bool // 自身或经若干条时刻有效的背书边可到达当前锚点根
}

// registry 非并发安全；并发由 Service 的单一读写锁保证。
type registry struct {
	keys map[string]*keyState
}

func newRegistry() *registry {
	return &registry{keys: map[string]*keyState{}}
}

func (r *registry) get(id string) (*keyState, bool) {
	k, ok := r.keys[id]
	return k, ok
}

// register 登记一把新密钥，可附带一条背书。调用方已持有写锁。
func (r *registry) register(id string, validFrom int64, validUntil *int64, end *Endorsement) error {
	if _, dup := r.keys[id]; dup {
		return ErrInvalidArgument
	}
	if end != nil {
		// 背书者必须存在，且不得形成（含自环的）环。
		if _, ok := r.keys[end.EndorserID]; !ok {
			return ErrKeyNotFound
		}
		if end.EndorserID == id || r.reaches(end.EndorserID, id) {
			return ErrEndorsementCycle
		}
	}
	r.keys[id] = &keyState{
		id:         id,
		validFrom:  validFrom,
		validUntil: validUntil,
		endorsedBy: end,
	}
	return nil
}

// addEndorsement 为已登记且尚无背书的密钥追加一条背书。
func (r *registry) addEndorsement(id string, end Endorsement) error {
	k, ok := r.keys[id]
	if !ok {
		return ErrKeyNotFound
	}
	if _, ok := r.keys[end.EndorserID]; !ok {
		return ErrKeyNotFound
	}
	if end.EndorserID == id || r.reaches(end.EndorserID, id) {
		return ErrEndorsementCycle
	}
	if k.endorsedBy != nil {
		return ErrInvalidArgument
	}
	k.endorsedBy = &end
	return nil
}

// revoke 设置吊销时刻；只能往后推，不能撤销也不能提前。
func (r *registry) revoke(id string, at int64) error {
	k, ok := r.keys[id]
	if !ok {
		return ErrKeyNotFound
	}
	if k.revokedAt != nil && at < *k.revokedAt {
		return ErrRevocationEarlier
	}
	k.revokedAt = &at
	return nil
}

// rebuild 在登记图或锚点改变后按当前根重算背书链标志。
func (r *registry) rebuild(rootID string) { r.rebuildRooted(rootID) }

// reaches 报告沿背书边从 from 出发能否到达 to（检测成环用）。
func (r *registry) reaches(from, to string) bool {
	visited := map[string]bool{}
	cur := from
	for cur != "" && !visited[cur] {
		if cur == to {
			return true
		}
		visited[cur] = true
		k, ok := r.keys[cur]
		if !ok || k.endorsedBy == nil {
			return false
		}
		cur = k.endorsedBy.EndorserID
	}
	return false
}

// endorsementValid 判断背书声明本身是否有效：签署时刻背书者有效且未吊销。
func endorsementValid(r *registry, k *keyState) bool {
	e := k.endorsedBy
	if e == nil {
		return false
	}
	er, ok := r.keys[e.EndorserID]
	if !ok {
		return false
	}
	return er.aliveAt(e.SignedAt)
}

// aliveAt 判断密钥自身在 at 是否处于有效区间且未吊销（不看背书）。
// 区间 [validFrom, validUntil)；吊销时刻之后（含相等）不受信。
func (k *keyState) aliveAt(at int64) bool {
	if at < k.validFrom {
		return false
	}
	if k.validUntil != nil && at >= *k.validUntil {
		return false
	}
	if k.revokedAt != nil && at >= *k.revokedAt {
		return false
	}
	return true
}

// statusAt 返回密钥在 at 的自身状态分类（不含背书链）。
// 优先级：未生效 → 已失效 → 已吊销。区间是密钥自身生命周期，
// 吊销发生于生命周期之内，故区间判定先于吊销判定。
func (k *keyState) statusAt(at int64) Reason {
	if at < k.validFrom {
		return ReasonNotYetValid
	}
	if k.validUntil != nil && at >= *k.validUntil {
		return ReasonExpired
	}
	if k.revokedAt != nil && at >= *k.revokedAt {
		return ReasonRevoked
	}
	return ""
}

// trustedAt 判断密钥在 at 是否受信。rootID 为当前锚点根密钥。
// chainOk 已在写路径上预算好（含每跳背书时刻的有效性），
// 故此处 O(1) 完成，且吊销不追溯：背书时刻的受信性不会被
// 背书者日后的吊销推翻。
func (r *registry) trustedAt(k *keyState, at int64, rootID string) bool {
	if !k.aliveAt(at) {
		return false
	}
	if k.id == rootID {
		return true
	}
	return k.chainOk
}

// rebuildChainOk 在登记图或锚点改变后重算 chainOk 标志。
// 一次重算 O(密钥数)，仅发生在登记/锚点变更的写路径上；
// 因而单次签名判定为 O(1)，不随密钥总数增长。
func (r *registry) rebuildRooted(rootID string) {
	for _, k := range r.keys {
		k.chainOk = false
	}
	if root, ok := r.keys[rootID]; ok {
		root.chainOk = true
	}
	changed := true
	for changed {
		changed = false
		for _, k := range r.keys {
			if k.chainOk || k.endorsedBy == nil {
				continue
			}
			er, ok := r.keys[k.endorsedBy.EndorserID]
			if ok && er.chainOk && endorsementValid(r, k) {
				k.chainOk = true
				changed = true
			}
		}
	}
}
