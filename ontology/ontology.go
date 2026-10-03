// Package ontology 实现 ACME 风格的证书订单与域名授权状态机。
//
// 状态机支持：创建订单并复用仍有效的授权、由授权状态与时间派生订单状态、
// 按验证失败的滑动窗口限流、按账户的待验证授权配额、有界一次性 nonce 池防重放。
// 所有操作可并发调用，结果等价于某个串行顺序；相同操作序列重放得到完全相同的结果。
package ontology

import (
	"container/list"
	"fmt"
	"sync"
)

// 授权存储状态。
const (
	AuthzPending     = "pending"
	AuthzValid       = "valid"
	AuthzInvalid     = "invalid"
	AuthzDeactivated = "deactivated"
)

// AuthzExpired 仅是有效（派生）状态，不作为存储状态。
const AuthzExpired = "expired"

// 订单存储状态与派生状态。
const (
	OrderActive  = "active" // 仅存储状态
	OrderValid   = "valid"
	OrderInvalid = "invalid"
	OrderReady   = "ready"
	OrderPending = "pending"
)

// maxNowBound 为 now 的合法上界（含）。
const maxNowBound int64 = 1_000_000_000_000_000

// ErrKind 区分被拒绝操作的类别，检查顺序与枚举顺序一致。
type ErrKind int

const (
	ErrParam     ErrKind = iota + 1 // 参数非法
	ErrClock                        // 时钟回退
	ErrNonce                        // nonce 无效
	ErrNotFound                     // 对象不存在（含属于别的账户）
	ErrState                        // 状态冲突
	ErrCSR                          // CSR 不符
	ErrRateLimit                    // 限流
	ErrQuota                        // 超限（配额）
)

// String 返回错误类别的稳定名称。
func (k ErrKind) String() string {
	switch k {
	case ErrParam:
		return "param"
	case ErrClock:
		return "clock"
	case ErrNonce:
		return "nonce"
	case ErrNotFound:
		return "not_found"
	case ErrState:
		return "state"
	case ErrCSR:
		return "csr"
	case ErrRateLimit:
		return "rate_limit"
	case ErrQuota:
		return "quota"
	}
	return "unknown"
}

// Error 是被拒绝操作返回的错误，携带定位信息。
type Error struct {
	Kind  ErrKind
	Msg   string
	State string // ErrState：当前有效状态
	Ident string // ErrRateLimit：第一个被限流的标识符
	Count int    // ErrRateLimit：窗口内失败次数
	P     int    // ErrQuota：当前待验证授权个数
	Q     int    // ErrQuota：本次需新建的授权个数
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	switch e.Kind {
	case ErrState:
		return fmt.Sprintf("state conflict: %s (current=%s)", e.Msg, e.State)
	case ErrRateLimit:
		return fmt.Sprintf("rate limited: ident=%s failures=%d", e.Ident, e.Count)
	case ErrQuota:
		return fmt.Sprintf("quota exceeded: p=%d q=%d", e.P, e.Q)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Msg)
}

func paramErr(format string, args ...any) *Error {
	return &Error{Kind: ErrParam, Msg: fmt.Sprintf(format, args...)}
}

// Config 为状态机的构造参数。
type Config struct {
	Ta int64 // 待验证授权存活期（秒）
	Tv int64 // 验证通过后的授权有效期（秒）
	To int64 // 订单有效期（秒）
	H  int64 // 失败窗口（秒）
	F  int   // 失败阈值
	C  int64 // nonce 池容量
	Pm int   // 每账户待验证授权上限
}

func (c Config) validate() error {
	for _, v := range []struct {
		name string
		val  int64
	}{
		{"Ta", c.Ta}, {"Tv", c.Tv}, {"To", c.To}, {"H", c.H},
	} {
		if v.val < 1 || v.val > 1_000_000_000 {
			return paramErr("%s must be in [1,1e9], got %d", v.name, v.val)
		}
	}
	if c.F < 1 || c.F > 1000 {
		return paramErr("F must be in [1,1000], got %d", c.F)
	}
	if c.C < 1 || c.C > 1_000_000 {
		return paramErr("C must be in [1,1e6], got %d", c.C)
	}
	if c.Pm < 1 || c.Pm > 10_000 {
		return paramErr("Pm must be in [1,1e4], got %d", c.Pm)
	}
	return nil
}

// Authz 是授权的对外视图。
type Authz struct {
	ID      string
	Account []byte
	Ident   string
	Status  string // 有效状态
	Expires int64
}

// Order 是订单的对外视图。
type Order struct {
	ID       string
	Account  []byte
	Idents   []string
	AuthzIDs []string
	Expires  int64
	Status   string // 派生状态
	CertSN   int64  // 0 表示尚未签发证书
}

// authz 是授权的内部表示。
type authz struct {
	id        string
	num       int
	account   string
	ident     string
	stored    string
	expires   int64
	reuseElem *list.Element // 在复用索引链表中的位置，不在链表中为 nil
}

// order 是订单的内部表示。
type order struct {
	id       string
	account  string
	idents   []string
	authzIDs []string
	expires  int64
	stored   string
	certSN   int64
}

// reuseKey 是复用索引与失败记录的键（账户, 标识符）。
type reuseKey struct {
	account string
	ident   string
}

// pendEnt 是待验证计数队列中的一条记录。
type pendEnt struct {
	expires int64
	id      string
}

// pendingTracker 按账户维护待验证授权个数，摊还 O(1)。
type pendingTracker struct {
	queue []pendEnt // 按 expires 非递减
	head  int
	count int // 当前有效状态为 pending 的授权个数
}

// failQueue 按 (账户, 标识符) 维护失败时间戳，摊还 O(1)。
type failQueue struct {
	t    []int64 // 按时间非递减
	head int
}

// Machine 是 ACME 风格的状态机，所有方法可并发调用。
type Machine struct {
	mu     sync.Mutex
	cfg    Config
	maxNow int64 // 已被接受操作见过的最大 now

	authzs  map[string]*authz
	orders  map[string]*order
	zSeq    int
	oSeq    int
	certSeq int64

	nonceNext int64
	noncePool map[int64]struct{}
	nonceQ    []int64 // 已发行 nonce，递增
	nonceHead int

	reuseIdx     map[reuseKey]*list.List // 每个键内按 expires 非递减存放 valid 授权 id
	reuseScanned int64                   // 复用查找考察的授权数（非导出计数器）

	fails   map[reuseKey]*failQueue
	pending map[string]*pendingTracker
}

// New 构造状态机；构造参数非法时整体拒绝。
func New(c Config) (*Machine, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &Machine{
		cfg:       c,
		authzs:    make(map[string]*authz),
		orders:    make(map[string]*order),
		noncePool: make(map[int64]struct{}),
		reuseIdx:  make(map[reuseKey]*list.List),
		fails:     make(map[reuseKey]*failQueue),
		pending:   make(map[string]*pendingTracker),
	}, nil
}

// Nonce 发行一个一次性 nonce，总是成功，依次返回 1、2、3…。
// 池满（个数等于 C）时先淘汰池中最小的一个。
func (m *Machine) Nonce() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nonceNext++
	n := m.nonceNext
	if int64(len(m.noncePool)) == m.cfg.C {
		// 发行序列递增，队列头部第一个仍在池中的即为最小者。
		for m.nonceHead < len(m.nonceQ) {
			h := m.nonceQ[m.nonceHead]
			m.nonceHead++
			if _, ok := m.noncePool[h]; ok {
				delete(m.noncePool, h)
				break
			}
		}
		if m.nonceHead > 1024 && m.nonceHead*2 >= len(m.nonceQ) {
			m.nonceQ = append([]int64(nil), m.nonceQ[m.nonceHead:]...)
			m.nonceHead = 0
		}
	}
	m.noncePool[n] = struct{}{}
	m.nonceQ = append(m.nonceQ, n)
	return n
}

// ---------- 参数校验 ----------

// validIdent 校验单个标识符：非空小写串，字符限于字母数字、连字符与点，
// 可带一个 "*." 前缀。
func validIdent(s string) bool {
	if s == "" {
		return false
	}
	if len(s) >= 2 && s[:2] == "*." {
		s = s[2:]
	}
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

// checkIdents 校验标识符列表：1 到 10 个、互不相同、逐个合法。
func checkIdents(idents []string) error {
	if len(idents) < 1 || len(idents) > 10 {
		return paramErr("idents count %d out of [1,10]", len(idents))
	}
	seen := make(map[string]struct{}, len(idents))
	for _, id := range idents {
		if !validIdent(id) {
			return paramErr("invalid identifier %q", id)
		}
		if _, dup := seen[id]; dup {
			return paramErr("duplicate identifier %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// checkNow 校验 now 的范围与时钟回退。
func (m *Machine) checkNow(now int64) *Error {
	if now < 0 || now > maxNowBound {
		return paramErr("now %d out of [0,1e15]", now)
	}
	if now < m.maxNow {
		return &Error{Kind: ErrClock, Msg: fmt.Sprintf("now %d < max seen %d", now, m.maxNow)}
	}
	return nil
}

// checkNonce 要求 nonce 当前在池中。
func (m *Machine) checkNonce(nonce int64) *Error {
	if _, ok := m.noncePool[nonce]; !ok {
		return &Error{Kind: ErrNonce, Msg: fmt.Sprintf("nonce %d not in pool", nonce)}
	}
	return nil
}

// consumeNonce 在操作被接受时把 nonce 从池中移除。
func (m *Machine) consumeNonce(nonce int64) {
	delete(m.noncePool, nonce)
}

// ---------- 派生状态 ----------

// authzEffective 计算授权在 now 时刻的有效状态。
func authzEffective(a *authz, now int64) string {
	if (a.stored == AuthzPending || a.stored == AuthzValid) && now >= a.expires {
		return AuthzExpired
	}
	return a.stored
}

// deriveOrder 按规则派生订单在 now 时刻的状态。
func (m *Machine) deriveOrder(o *order, now int64) string {
	if o.stored == OrderValid {
		return OrderValid
	}
	if now >= o.expires {
		return OrderInvalid
	}
	allValid := true
	for _, id := range o.authzIDs {
		switch authzEffective(m.authzs[id], now) {
		case AuthzInvalid, AuthzDeactivated, AuthzExpired:
			return OrderInvalid
		case AuthzValid:
		default:
			allValid = false
		}
	}
	if allValid {
		return OrderReady
	}
	return OrderPending
}

// ---------- 索引与计数 ----------

// findReuse 在该账户该标识符下找可复用授权：有效状态为 valid 中 expires 最大者，
// 并列取编号小者。顺带从索引前端清除已到期者（每个授权至多被清除一次）。
func (m *Machine) findReuse(account, ident string, now int64) *authz {
	l := m.reuseIdx[reuseKey{account, ident}]
	if l == nil {
		return nil
	}
	// 链表按 expires 非递减；已停用者在 Deactivate 时已摘除，
	// 故前端连续的一段即全部已到期者，逐个清除。
	for e := l.Front(); e != nil; e = l.Front() {
		a := m.authzs[e.Value.(string)]
		if now < a.expires {
			break
		}
		m.reuseScanned++
		l.Remove(e)
		a.reuseElem = nil
	}
	back := l.Back()
	if back == nil {
		return nil
	}
	// 链尾即 expires 最大者；并列块全部可复用，取编号最小者。
	best := m.authzs[back.Value.(string)]
	m.reuseScanned++
	for e := back.Prev(); e != nil; e = e.Prev() {
		a := m.authzs[e.Value.(string)]
		if a.expires != best.expires {
			break
		}
		m.reuseScanned++
		if a.num < best.num {
			best = a
		}
	}
	return best
}

// addReuse 把刚变为 valid 的授权挂到复用索引尾部（expires 非递减）。
func (m *Machine) addReuse(a *authz) {
	key := reuseKey{a.account, a.ident}
	l := m.reuseIdx[key]
	if l == nil {
		l = list.New()
		m.reuseIdx[key] = l
	}
	a.reuseElem = l.PushBack(a.id)
}

// failCount 统计该账户该标识符在 now 时刻窗口内的失败次数（t+H > now）。
func (m *Machine) failCount(account, ident string, now int64) int {
	fq := m.fails[reuseKey{account, ident}]
	if fq == nil {
		return 0
	}
	for fq.head < len(fq.t) && fq.t[fq.head]+m.cfg.H <= now {
		fq.head++
	}
	return len(fq.t) - fq.head
}

// recordFail 记录一次验证失败。
func (m *Machine) recordFail(account, ident string, now int64) {
	key := reuseKey{account, ident}
	fq := m.fails[key]
	if fq == nil {
		fq = &failQueue{}
		m.fails[key] = fq
	}
	fq.t = append(fq.t, now)
}

// pendingCount 返回该账户在 now 时刻有效状态为 pending 的授权个数，摊还 O(1)。
func (m *Machine) pendingCount(account string, now int64) int {
	pt := m.pending[account]
	if pt == nil {
		return 0
	}
	for pt.head < len(pt.queue) && pt.queue[pt.head].expires <= now {
		if m.authzs[pt.queue[pt.head].id].stored == AuthzPending {
			pt.count--
		}
		pt.head++
	}
	return pt.count
}

// trackPending 登记一个新建 pending 授权。
func (m *Machine) trackPending(a *authz) {
	pt := m.pending[a.account]
	if pt == nil {
		pt = &pendingTracker{}
		m.pending[a.account] = pt
	}
	pt.queue = append(pt.queue, pendEnt{expires: a.expires, id: a.id})
	pt.count++
}

// leavePending 在授权离开 pending（Report/Deactivate）时释放配额。
func (m *Machine) leavePending(a *authz) {
	m.pending[a.account].count--
}

// ---------- 视图 ----------

func (m *Machine) authzView(a *authz, now int64) *Authz {
	return &Authz{
		ID:      a.id,
		Account: []byte(a.account),
		Ident:   a.ident,
		Status:  authzEffective(a, now),
		Expires: a.expires,
	}
}

func (m *Machine) orderView(o *order, now int64) *Order {
	return &Order{
		ID:       o.id,
		Account:  []byte(o.account),
		Idents:   append([]string(nil), o.idents...),
		AuthzIDs: append([]string(nil), o.authzIDs...),
		Expires:  o.expires,
		Status:   m.deriveOrder(o, now),
		CertSN:   o.certSN,
	}
}

// ---------- 操作 ----------

// NewOrder 创建订单：对每个标识符复用该账户下有效状态为 valid 的授权中
// expires 最大者（并列取编号小者），没有则新建 pending 授权。
// 检查顺序：参数非法、时钟回退、nonce 无效、限流、超限。
func (m *Machine) NewOrder(account []byte, idents []string, nonce, now int64) (*Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(account) == 0 {
		return nil, paramErr("empty account")
	}
	if err := checkIdents(idents); err != nil {
		return nil, err
	}
	if err := m.checkNow(now); err != nil {
		return nil, err
	}
	if err := m.checkNonce(nonce); err != nil {
		return nil, err
	}
	acct := string(account)
	// 限流：按给定顺序逐标识符统计窗口内失败次数。
	for _, id := range idents {
		if c := m.failCount(acct, id, now); c >= m.cfg.F {
			return nil, &Error{
				Kind:  ErrRateLimit,
				Ident: id,
				Count: c,
				Msg:   fmt.Sprintf("ident %s has %d failures in window", id, c),
			}
		}
	}
	// 复用查找与新建计数。
	reused := make([]*authz, len(idents))
	q := 0
	for i, id := range idents {
		if a := m.findReuse(acct, id, now); a != nil {
			reused[i] = a
		} else {
			q++
		}
	}
	// 配额：p+q 大于 Pm 报超限；复用的授权不占配额。
	p := m.pendingCount(acct, now)
	if p+q > m.cfg.Pm {
		return nil, &Error{
			Kind: ErrQuota,
			P:    p,
			Q:    q,
			Msg:  fmt.Sprintf("pending %d + new %d > Pm %d", p, q, m.cfg.Pm),
		}
	}
	// 接受：消费 nonce，创建授权与订单，推进时钟。
	m.consumeNonce(nonce)
	authzIDs := make([]string, len(idents))
	for i, id := range idents {
		if reused[i] != nil {
			authzIDs[i] = reused[i].id
			continue
		}
		m.zSeq++
		z := &authz{
			id:      fmt.Sprintf("z%d", m.zSeq),
			num:     m.zSeq,
			account: acct,
			ident:   id,
			stored:  AuthzPending,
			expires: now + m.cfg.Ta,
		}
		m.authzs[z.id] = z
		m.trackPending(z)
		authzIDs[i] = z.id
	}
	m.oSeq++
	o := &order{
		id:       fmt.Sprintf("o%d", m.oSeq),
		account:  acct,
		idents:   append([]string(nil), idents...),
		authzIDs: authzIDs,
		expires:  now + m.cfg.To,
		stored:   OrderActive,
	}
	m.orders[o.id] = o
	m.maxNow = now
	return m.orderView(o, now), nil
}

// Report 是 CA 内部的验证结果上报（无账户与 nonce）。
// 授权有效状态须为 pending；ok 使其变为 valid 且 expires=now+Tv，
// 否则变为 invalid 并记一次失败。
func (m *Machine) Report(authzID string, ok bool, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if authzID == "" {
		return paramErr("empty authz id")
	}
	if err := m.checkNow(now); err != nil {
		return err
	}
	a := m.authzs[authzID]
	if a == nil {
		return &Error{Kind: ErrNotFound, Msg: fmt.Sprintf("authz %s not found", authzID)}
	}
	eff := authzEffective(a, now)
	if eff != AuthzPending {
		return &Error{Kind: ErrState, State: eff, Msg: fmt.Sprintf("authz %s is %s", authzID, eff)}
	}
	m.leavePending(a)
	if ok {
		a.stored = AuthzValid
		a.expires = now + m.cfg.Tv
		m.addReuse(a)
	} else {
		a.stored = AuthzInvalid
		m.recordFail(a.account, a.ident, now)
	}
	m.maxNow = now
	return nil
}

// Deactivate 要求授权属于该账户且有效状态为 pending 或 valid，置 deactivated。
func (m *Machine) Deactivate(account []byte, authzID string, nonce, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(account) == 0 {
		return paramErr("empty account")
	}
	if authzID == "" {
		return paramErr("empty authz id")
	}
	if err := m.checkNow(now); err != nil {
		return err
	}
	if err := m.checkNonce(nonce); err != nil {
		return err
	}
	a := m.authzs[authzID]
	if a == nil || a.account != string(account) {
		return &Error{Kind: ErrNotFound, Msg: fmt.Sprintf("authz %s not found for account", authzID)}
	}
	eff := authzEffective(a, now)
	if eff != AuthzPending && eff != AuthzValid {
		return &Error{Kind: ErrState, State: eff, Msg: fmt.Sprintf("authz %s is %s", authzID, eff)}
	}
	m.consumeNonce(nonce)
	if a.stored == AuthzPending {
		m.leavePending(a)
	}
	if a.reuseElem != nil {
		m.reuseIdx[reuseKey{a.account, a.ident}].Remove(a.reuseElem)
		a.reuseElem = nil
	}
	a.stored = AuthzDeactivated
	m.maxNow = now
	return nil
}

// Finalize 要求订单属于该账户且派生状态为 ready，CSR 标识符集合与订单一致。
// 成功后订单存储状态变为 valid，返回全局证书序号（从 1 起）。
func (m *Machine) Finalize(account []byte, orderID string, csr []string, nonce, now int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(account) == 0 {
		return 0, paramErr("empty account")
	}
	if orderID == "" {
		return 0, paramErr("empty order id")
	}
	if err := checkIdents(csr); err != nil {
		return 0, err
	}
	if err := m.checkNow(now); err != nil {
		return 0, err
	}
	if err := m.checkNonce(nonce); err != nil {
		return 0, err
	}
	o := m.orders[orderID]
	if o == nil || o.account != string(account) {
		return 0, &Error{Kind: ErrNotFound, Msg: fmt.Sprintf("order %s not found for account", orderID)}
	}
	st := m.deriveOrder(o, now)
	if st != OrderReady {
		return 0, &Error{Kind: ErrState, State: st, Msg: fmt.Sprintf("order %s is %s", orderID, st)}
	}
	if !sameSet(o.idents, csr) {
		return 0, &Error{Kind: ErrCSR, Msg: "csr identifiers differ from order identifiers"}
	}
	m.consumeNonce(nonce)
	o.stored = OrderValid
	m.certSeq++
	o.certSN = m.certSeq
	m.maxNow = now
	return o.certSN, nil
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return true
}

// ---------- 只读查询 ----------

// Status 返回订单在 now 时刻的派生状态。只读，不推进时钟。
func (m *Machine) Status(orderID string, now int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if orderID == "" {
		return "", paramErr("empty order id")
	}
	if err := m.checkNow(now); err != nil {
		return "", err
	}
	o := m.orders[orderID]
	if o == nil {
		return "", &Error{Kind: ErrNotFound, Msg: fmt.Sprintf("order %s not found", orderID)}
	}
	return m.deriveOrder(o, now), nil
}

// GetOrder 返回订单在 now 时刻的视图。只读，不推进时钟。
func (m *Machine) GetOrder(orderID string, now int64) (*Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if orderID == "" {
		return nil, paramErr("empty order id")
	}
	if err := m.checkNow(now); err != nil {
		return nil, err
	}
	o := m.orders[orderID]
	if o == nil {
		return nil, &Error{Kind: ErrNotFound, Msg: fmt.Sprintf("order %s not found", orderID)}
	}
	return m.orderView(o, now), nil
}

// GetAuthz 返回授权在 now 时刻的视图。只读，不推进时钟。
func (m *Machine) GetAuthz(authzID string, now int64) (*Authz, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if authzID == "" {
		return nil, paramErr("empty authz id")
	}
	if err := m.checkNow(now); err != nil {
		return nil, err
	}
	a := m.authzs[authzID]
	if a == nil {
		return nil, &Error{Kind: ErrNotFound, Msg: fmt.Sprintf("authz %s not found", authzID)}
	}
	return m.authzView(a, now), nil
}
