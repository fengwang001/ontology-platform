package ontology

import "sync"

// Cert 表示一张待登记的证书。
type Cert struct {
	ID        []byte
	Subject   []byte
	Issuer    []byte
	Key       []byte
	AuthKey   []byte
	NotBefore int64
	NotAfter  int64
	IsCA      bool
	PathLen   int64
	Permitted []string
	Excluded  []string
	SAN       []string
}

// Validator 是证书登记、信任/吊销标记与路径验证器。
// 所有方法可并发调用：写操作互斥，Verify 在读锁下读取一致快照。
type Validator struct {
	mu        sync.RWMutex
	maxPath   int
	maxCerts  int
	certs     map[string]*cert
	bySubject map[string][]*cert
}

type cert struct {
	data      Cert
	trusted   bool
	revokedAt int64 // 未吊销为 -1
}

// New 创建验证器；参数非法时返回配置非法错误。
func New(maxPathLen int, maxCerts int) (*Validator, error) {
	if maxPathLen < 1 || maxPathLen > 16 {
		return nil, errConfig("New", "L must be in [1,16]")
	}
	if maxCerts < 1 || maxCerts > 100_000 {
		return nil, errConfig("New", "Nmax must be in [1,100000]")
	}
	return &Validator{
		maxPath:   maxPathLen,
		maxCerts:  maxCerts,
		certs:     make(map[string]*cert),
		bySubject: make(map[string][]*cert),
	}, nil
}

func cloneBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// Add 登记一张证书。
func (v *Validator) Add(c Cert) error {
	if err := validateCert(c); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	key := string(c.ID)
	if _, ok := v.certs[key]; ok {
		return errConflict("Add", c.ID)
	}
	if len(v.certs) >= v.maxCerts {
		return errLimit("Add")
	}
	stored := &cert{
		data: Cert{
			ID:        cloneBytes(c.ID),
			Subject:   cloneBytes(c.Subject),
			Issuer:    cloneBytes(c.Issuer),
			Key:       cloneBytes(c.Key),
			AuthKey:   cloneBytes(c.AuthKey),
			NotBefore: c.NotBefore,
			NotAfter:  c.NotAfter,
			IsCA:      c.IsCA,
			PathLen:   c.PathLen,
			Permitted: cloneStrings(c.Permitted),
			Excluded:  cloneStrings(c.Excluded),
			SAN:       cloneStrings(c.SAN),
		},
		revokedAt: -1,
	}
	v.certs[key] = stored
	sk := string(stored.data.Subject)
	v.bySubject[sk] = insertCandidate(v.bySubject[sk], stored)
	return nil
}

// insertCandidate 按 NotAfter 降序、NotAfter 相同时 ID 升序插入。
func insertCandidate(list []*cert, c *cert) []*cert {
	i := 0
	for i < len(list) && candidateLess(list[i], c) {
		i++
	}
	list = append(list, nil)
	copy(list[i+1:], list[i:])
	list[i] = c
	return list
}

// candidateLess 在候选次序中 a 应排在 b 之前时返回 true。
func candidateLess(a, b *cert) bool {
	if a.data.NotAfter != b.data.NotAfter {
		return a.data.NotAfter > b.data.NotAfter
	}
	return string(a.data.ID) < string(b.data.ID)
}

// Trust 将证书标记为信任锚（幂等）。
func (v *Validator) Trust(id []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	c, ok := v.certs[string(id)]
	if !ok {
		return errNotFound("Trust", id)
	}
	c.trusted = true
	return nil
}

// Revoke 登记证书的吊销时刻（保留较早者）。
func (v *Validator) Revoke(id []byte, at int64) error {
	if at < minTime || at > maxTime {
		return errConfig("Revoke", "at must be in [0,1e15]")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	c, ok := v.certs[string(id)]
	if !ok {
		return errNotFound("Revoke", id)
	}
	if c.revokedAt < 0 || at < c.revokedAt {
		c.revokedAt = at
	}
	return nil
}

// VerifyResult 是一次验证的结果。
// 找到通过路径时 Failure 为 nil；无结构路径时 NoPath 为 true。
type VerifyResult struct {
	Path       [][]byte
	Considered int
	Failure    *VerifyFailure
	NoPath     bool
}

// VerifyFailure 描述一条结构路径未通过检验的首个原因。
type VerifyFailure struct {
	Reason FailReason
	Index  int
	Path   [][]byte
}

// FailReason 是路径检验失败原因枚举。
type FailReason int

const (
	FailExpired FailReason = iota
	FailRevoked
	FailNotCA
	FailPathLen
	FailExcluded
	FailNotPermitted
	FailSAN
)

// Verify 从 leafID 出发构造并验证证书路径。
func (v *Validator) Verify(leafID []byte, name string, now int64) (*VerifyResult, error) {
	if !validDNSName(name) {
		return nil, errConfig("Verify", "invalid name: "+name)
	}
	if now < minTime || now > maxTime {
		return nil, errConfig("Verify", "now must be in [0,1e15]")
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	leaf, ok := v.certs[string(leafID)]
	if !ok {
		return nil, errNotFound("Verify", leafID)
	}

	en := &enumerator{
		v:      v,
		name:   name,
		now:    now,
		onPath: map[*cert]bool{},
	}
	if r := en.dfs(leaf); r != nil {
		return r, nil
	}
	if en.firstFail == nil {
		return &VerifyResult{NoPath: true, Considered: en.considered}, nil
	}
	return &VerifyResult{Failure: en.firstFail, Considered: en.considered}, nil
}

type enumerator struct {
	v          *Validator
	name       string
	now        int64
	path       []*cert
	onPath     map[*cert]bool
	considered int
	firstFail  *VerifyFailure
}

func (en *enumerator) dfs(cur *cert) *VerifyResult {
	en.path = append(en.path, cur)
	en.onPath[cur] = true
	defer func() {
		en.onPath[cur] = false
		en.path = en.path[:len(en.path)-1]
	}()

	if cur.trusted {
		failure := checkPath(en.path, en.name, en.now)
		if failure == nil {
			return &VerifyResult{Path: pathIDs(en.path), Considered: en.considered}
		}
		if en.firstFail == nil {
			failure.Path = pathIDs(en.path)
			en.firstFail = failure
		}
		return nil
	}

	for _, cand := range en.v.bySubject[string(cur.data.Issuer)] {
		if string(cand.data.Key) != string(cur.data.AuthKey) || cand == cur {
			continue
		}
		en.considered++
		if en.onPath[cand] {
			continue
		}
		if len(en.path) >= en.v.maxPath {
			continue
		}
		if r := en.dfs(cand); r != nil {
			return r
		}
	}
	return nil
}

func pathIDs(path []*cert) [][]byte {
	ids := make([][]byte, len(path))
	for i, c := range path {
		ids[i] = cloneBytes(c.data.ID)
	}
	return ids
}
