// Package kms 实现带惰性自动轮换、禁用与计划删除窗口的 KMS 密钥生命周期管理器。
//
// 所有操作共享一个全局时钟（已接受操作见过的最大 now），时间回退即拒绝。
// 补建（惰性轮换）按公式 c=(now-d)/P+1 一次性计算，只物化最后 min(c,V) 个版本，
// 版本号仍按完整的 c 推进，被淘汰的最小版本号凭据不可再解。
// 计划删除到期（deleteAt<=now）的密钥在该 now 下视同不存在，名额立即让出。
package kms

import (
	"container/heap"
	"fmt"
	"sync"
)

const (
	maxNowLimit  = int64(1_000_000_000_000_000) // now 上界 10^15
	minPeriod    = int64(60)
	maxPeriod    = int64(1_000_000_000)
	maxWindow    = int64(1_000_000_000)
	maxRetain    = int64(64)
	maxAliveKeys = int64(1_000_000)
)

// State 为密钥状态。
type State int

const (
	Enabled State = iota
	Disabled
	Pending // 计划删除中
)

func (s State) String() string {
	switch s {
	case Enabled:
		return "Enabled"
	case Disabled:
		return "Disabled"
	case Pending:
		return "Pending"
	}
	return "Unknown"
}

// ErrKind 为可区分的拒绝类别。
type ErrKind int

const (
	ErrInvalidConfig    ErrKind = iota // 配置非法（构造参数越界）
	ErrInvalidParam                    // 参数非法
	ErrClockRollback                   // 时钟回退
	ErrNotFound                        // 不存在
	ErrKeyDeleted                      // 密钥已删除（凭据世代非当前存活世代）
	ErrStateConflict                   // 状态冲突（带出当前状态）
	ErrStateRejected                   // 状态拒绝（加解密遇 Disabled/Pending）
	ErrVersionNotFound                 // 版本问题：版本不存在
	ErrVersionRetired                  // 版本问题：版本已淘汰
	ErrCapacityExceeded                // 超限（存活密钥数超过 Kmax）
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidConfig:
		return "配置非法"
	case ErrInvalidParam:
		return "参数非法"
	case ErrClockRollback:
		return "时钟回退"
	case ErrNotFound:
		return "不存在"
	case ErrKeyDeleted:
		return "密钥已删除"
	case ErrStateConflict:
		return "状态冲突"
	case ErrStateRejected:
		return "状态拒绝"
	case ErrVersionNotFound:
		return "版本不存在"
	case ErrVersionRetired:
		return "版本已淘汰"
	case ErrCapacityExceeded:
		return "超限"
	}
	return "未知错误"
}

// Error 为带定位信息的拒绝原因。
type Error struct {
	Kind     ErrKind // 拒绝类别
	Op       string  // 触发拒绝的操作名
	ID       string  // 相关密钥 id
	Gen      int64   // 相关世代（凭据类操作）
	Version  int64   // 相关版本号（凭据类操作）
	State    State   // 当前状态（状态冲突/状态拒绝时有效）
	HasState bool    // State 字段是否有效
	Detail   string  // 判定依据
}

func (e *Error) Error() string {
	s := fmt.Sprintf("kms: %s %q 被拒绝：%s", e.Op, e.ID, e.Kind)
	if e.HasState {
		s += fmt.Sprintf("（当前状态 %s）", e.State)
	}
	if e.Detail != "" {
		s += "：" + e.Detail
	}
	return s
}

// VersionInfo 描述一个保留版本。
type VersionInfo struct {
	Number    int64 // 版本号，从 1 起递增且永不复用
	CreatedAt int64 // 创建时刻（计划时刻，而非补建发生的 now）
}

// Credential 为加解密凭据。
type Credential struct {
	ID      string
	Gen     int64 // 世代号
	Version int64 // 版本号
}

// KeyView 为 Describe 返回的只读视图。
type KeyView struct {
	State        State
	Generation   int64
	Versions     []VersionInfo // 按 now 视角虚拟补建后的保留版本
	NextRotation int64         // 下次自动轮换时刻 d
	DeleteAt     int64         // 删除时刻（仅 Pending 有意义）
}

// key 为单个密钥某一代的内部状态。
type key struct {
	gen      int64
	state    State
	period   int64 // 轮换周期 P，0 表示不自动轮换
	versions []VersionInfo
	d        int64 // 下次自动轮换时刻
	deleteAt int64
	heapSeq  int64 // 当前有效的堆条目序号，-1 表示无
}

// delEntry 为计划删除堆条目；序号不匹配即视为作废（被取消或被新世代取代）。
type delEntry struct {
	deleteAt int64
	seq      int64
	id       string
	gen      int64
}

type delHeap []delEntry

func (h delHeap) Len() int { return len(h) }
func (h delHeap) Less(i, j int) bool {
	if h[i].deleteAt != h[j].deleteAt {
		return h[i].deleteAt < h[j].deleteAt
	}
	return h[i].seq < h[j].seq
}
func (h delHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *delHeap) Push(x interface{}) { *h = append(*h, x.(delEntry)) }
func (h *delHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Manager 为 KMS 密钥生命周期管理器，所有方法可并发调用，
// 结果等价于某个串行顺序。
type Manager struct {
	v    int64 // 保留版本上限 V
	wmin int64 // 删除窗口下限 Wmin
	wmax int64 // 删除窗口上限 Wmax
	kmax int64 // 存活密钥数上限 Kmax

	mu      sync.Mutex
	maxNow  int64            // 全局时钟：已接受操作见过的最大 now
	keys    map[string]*key  // 物理上仍持有的密钥（含尚未回收的到期密钥）
	maxGen  map[string]int64 // 每个 id 已创建过的最大世代号
	alive   int64            // 当前存活密钥数
	seq     int64            // 堆条目序号
	pending delHeap          // 计划删除最小堆

	materialized int64 // 非导出计数器：补建时物化的版本总数
	heapPops     int64 // 非导出计数器：回收时的堆弹出总次数
}

// NewManager 构造管理器。任一配置越界即以配置非法整体拒绝。
func NewManager(v, wmin, wmax, kmax int64) (*Manager, error) {
	if v < 1 || v > maxRetain || wmin < 1 || wmin > wmax || wmax > maxWindow ||
		kmax < 1 || kmax > maxAliveKeys {
		return nil, &Error{Kind: ErrInvalidConfig, Op: "NewManager",
			Detail: fmt.Sprintf("V=%d Wmin=%d Wmax=%d Kmax=%d", v, wmin, wmax, kmax)}
	}
	return &Manager{
		v: v, wmin: wmin, wmax: wmax, kmax: kmax,
		keys: make(map[string]*key), maxGen: make(map[string]int64),
	}, nil
}

func validNow(now int64) bool  { return now >= 0 && now <= maxNowLimit }
func validPeriod(p int64) bool { return p == 0 || (p >= minPeriod && p <= maxPeriod) }

func (m *Manager) paramErr(op, id, detail string) *Error {
	return &Error{Kind: ErrInvalidParam, Op: op, ID: id, Detail: detail}
}

func (m *Manager) clockErr(op, id string, now int64) *Error {
	return &Error{Kind: ErrClockRollback, Op: op, ID: id,
		Detail: fmt.Sprintf("now=%d 小于已接受的最大 now=%d", now, m.maxNow)}
}

// lookup 返回 now 时刻存活的密钥；Pending 且 deleteAt<=now 的密钥视同不存在。
func (m *Manager) lookup(id string, now int64) *key {
	k := m.keys[id]
	if k == nil {
		return nil
	}
	if k.state == Pending && k.deleteAt <= now {
		return nil
	}
	return k
}

// sweep 物理回收 deleteAt<=now 的堆条目；作废条目（被取消或世代不符）直接丢弃。
// 每次弹出的次数不超过到期删除数加上作废条目数。
func (m *Manager) sweep(now int64) {
	for len(m.pending) > 0 && m.pending[0].deleteAt <= now {
		e := heap.Pop(&m.pending).(delEntry)
		m.heapPops++
		k, ok := m.keys[e.id]
		if ok && k.heapSeq == e.seq && k.gen == e.gen {
			delete(m.keys, e.id)
			m.alive--
		}
	}
}

// catchUp 纯函数：计算补建后的版本表与新 d，不修改入参。
// 到期次数 c=(now-d)/P+1，依次在 d、d+P、...、d+(c-1)P 各建一个版本，
// 只物化最后 min(c,V) 个，版本号按完整的 c 推进，超出 V 时淘汰最小版本号。
func catchUp(versions []VersionInfo, d, period, now, v int64) (rot []VersionInfo, newD int64, materialized int64) {
	if period <= 0 || d > now {
		return versions, d, 0
	}
	c := (now-d)/period + 1
	materialized = c
	if materialized > v {
		materialized = v
	}
	maxNum := versions[len(versions)-1].Number
	rot = make([]VersionInfo, 0, len(versions)+int(materialized))
	rot = append(rot, versions...)
	for i := c - materialized; i < c; i++ {
		rot = append(rot, VersionInfo{Number: maxNum + i + 1, CreatedAt: d + i*period})
	}
	for int64(len(rot)) > v {
		rot = rot[1:]
	}
	return rot, d + c*period, materialized
}

// applyCatchUp 对 Enabled 且 P>0 且 d<=now 的密钥就地补建。
func (m *Manager) applyCatchUp(k *key, now int64) {
	if k.state != Enabled || k.period <= 0 || k.d > now {
		return
	}
	rot, newD, mat := catchUp(k.versions, k.d, k.period, now, m.v)
	k.versions = rot
	k.d = newD
	m.materialized += mat
}

// Create 新建 Enabled 密钥，版本 1 创建于 now，d=now+P。
// 同一 id 删除后可再次创建，世代号递增。
func (m *Manager) Create(id string, period, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		return m.paramErr("Create", id, "id 为空")
	}
	if !validPeriod(period) {
		return m.paramErr("Create", id, fmt.Sprintf("P=%d 非 0 且不在 [60,10^9]", period))
	}
	if !validNow(now) {
		return m.paramErr("Create", id, fmt.Sprintf("now=%d 越界", now))
	}
	if now < m.maxNow {
		return m.clockErr("Create", id, now)
	}
	if k := m.lookup(id, now); k != nil {
		return &Error{Kind: ErrStateConflict, Op: "Create", ID: id,
			State: k.state, HasState: true, Detail: "id 已有存活密钥"}
	}
	m.sweep(now)
	if m.alive >= m.kmax {
		return &Error{Kind: ErrCapacityExceeded, Op: "Create", ID: id,
			Detail: fmt.Sprintf("存活密钥数达到 Kmax=%d", m.kmax)}
	}
	gen := m.maxGen[id] + 1
	m.keys[id] = &key{
		gen: gen, state: Enabled, period: period,
		versions: []VersionInfo{{Number: 1, CreatedAt: now}},
		d:        now + period, heapSeq: -1,
	}
	m.maxGen[id] = gen
	m.alive++
	m.maxNow = now
	return nil
}

// lookupState 为凭据无关操作做不存在检查。
func (m *Manager) lookupState(op, id string, now int64) (*key, *Error) {
	k := m.lookup(id, now)
	if k == nil {
		return nil, &Error{Kind: ErrNotFound, Op: op, ID: id, Detail: "找不到存活的同名密钥"}
	}
	return k, nil
}

// Encrypt 要求 Enabled，先补建，返回凭据 (id, 世代, 补建后的最大版本号)。
func (m *Manager) Encrypt(id string, now int64) (Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		return Credential{}, m.paramErr("Encrypt", id, "id 为空")
	}
	if !validNow(now) {
		return Credential{}, m.paramErr("Encrypt", id, fmt.Sprintf("now=%d 越界", now))
	}
	if now < m.maxNow {
		return Credential{}, m.clockErr("Encrypt", id, now)
	}
	k, err := m.lookupState("Encrypt", id, now)
	if err != nil {
		return Credential{}, err
	}
	if k.state != Enabled {
		return Credential{}, &Error{Kind: ErrStateRejected, Op: "Encrypt", ID: id,
			State: k.state, HasState: true, Detail: stateRejectReason(k.state)}
	}
	m.sweep(now)
	m.applyCatchUp(k, now)
	m.maxNow = now
	return Credential{ID: id, Gen: k.gen, Version: k.versions[len(k.versions)-1].Number}, nil
}

func stateRejectReason(s State) string {
	if s == Disabled {
		return "密钥禁用中"
	}
	return "密钥计划删除中"
}

// checkCredential 校验凭据并返回 now 下存活的密钥（不做版本检查）。
func (m *Manager) checkCredential(op string, cred Credential, now int64) (*key, *Error) {
	mg, ok := m.maxGen[cred.ID]
	if !ok || cred.Gen > mg {
		return nil, &Error{Kind: ErrNotFound, Op: op, ID: cred.ID, Gen: cred.Gen,
			Version: cred.Version, Detail: "id 从未创建过或世代超过已创建的最大世代"}
	}
	k := m.lookup(cred.ID, now)
	if k == nil || k.gen != cred.Gen {
		return nil, &Error{Kind: ErrKeyDeleted, Op: op, ID: cred.ID, Gen: cred.Gen,
			Version: cred.Version, Detail: "凭据世代不是该 id 当前存活的世代"}
	}
	if k.state != Enabled {
		return nil, &Error{Kind: ErrStateRejected, Op: op, ID: cred.ID, Gen: cred.Gen,
			Version: cred.Version, State: k.state, HasState: true,
			Detail: stateRejectReason(k.state)}
	}
	return k, nil
}

func (m *Manager) validCredParam(op string, cred Credential, now int64) *Error {
	if cred.ID == "" {
		return m.paramErr(op, cred.ID, "凭据 id 为空")
	}
	if cred.Gen < 1 || cred.Version < 1 {
		return m.paramErr(op, cred.ID, "凭据世代或版本小于 1")
	}
	if !validNow(now) {
		return m.paramErr(op, cred.ID, fmt.Sprintf("now=%d 越界", now))
	}
	return nil
}

// Decrypt 返回凭据版本是否为补建后的当前最大版本。
// 补建先在副本上计算，任一拒绝则副本丢弃。
func (m *Manager) Decrypt(cred Credential, now int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validCredParam("Decrypt", cred, now); err != nil {
		return false, err
	}
	if now < m.maxNow {
		return false, m.clockErr("Decrypt", cred.ID, now)
	}
	k, err := m.checkCredential("Decrypt", cred, now)
	if err != nil {
		return false, err
	}
	rot, newD, mat := catchUp(k.versions, k.d, k.period, now, m.v)
	maxV := rot[len(rot)-1].Number
	minV := rot[0].Number
	if cred.Version > maxV {
		return false, &Error{Kind: ErrVersionNotFound, Op: "Decrypt", ID: cred.ID,
			Gen: cred.Gen, Version: cred.Version,
			Detail: fmt.Sprintf("版本大于当前最大版本 %d", maxV)}
	}
	if cred.Version < minV {
		return false, &Error{Kind: ErrVersionRetired, Op: "Decrypt", ID: cred.ID,
			Gen: cred.Gen, Version: cred.Version,
			Detail: fmt.Sprintf("版本小于保留的最小版本 %d", minV)}
	}
	m.sweep(now)
	k.versions = rot
	k.d = newD
	m.materialized += mat
	m.maxNow = now
	return cred.Version == maxV, nil
}

// ReEncrypt 检查同 Decrypt，成功后返回版本为当前最大版本的新凭据；
// 凭据本已是最大版本则原样返回并标记未变化。
func (m *Manager) ReEncrypt(cred Credential, now int64) (Credential, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validCredParam("ReEncrypt", cred, now); err != nil {
		return Credential{}, false, err
	}
	if now < m.maxNow {
		return Credential{}, false, m.clockErr("ReEncrypt", cred.ID, now)
	}
	k, err := m.checkCredential("ReEncrypt", cred, now)
	if err != nil {
		return Credential{}, false, err
	}
	rot, newD, mat := catchUp(k.versions, k.d, k.period, now, m.v)
	maxV := rot[len(rot)-1].Number
	minV := rot[0].Number
	if cred.Version > maxV {
		return Credential{}, false, &Error{Kind: ErrVersionNotFound, Op: "ReEncrypt",
			ID: cred.ID, Gen: cred.Gen, Version: cred.Version,
			Detail: fmt.Sprintf("版本大于当前最大版本 %d", maxV)}
	}
	if cred.Version < minV {
		return Credential{}, false, &Error{Kind: ErrVersionRetired, Op: "ReEncrypt",
			ID: cred.ID, Gen: cred.Gen, Version: cred.Version,
			Detail: fmt.Sprintf("版本小于保留的最小版本 %d", minV)}
	}
	m.sweep(now)
	k.versions = rot
	k.d = newD
	m.materialized += mat
	m.maxNow = now
	changed := cred.Version != maxV
	return Credential{ID: cred.ID, Gen: k.gen, Version: maxV}, changed, nil
}

// Disable 要求 Enabled，先补建再置 Disabled。
func (m *Manager) Disable(id string, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		return m.paramErr("Disable", id, "id 为空")
	}
	if !validNow(now) {
		return m.paramErr("Disable", id, fmt.Sprintf("now=%d 越界", now))
	}
	if now < m.maxNow {
		return m.clockErr("Disable", id, now)
	}
	k, err := m.lookupState("Disable", id, now)
	if err != nil {
		return err
	}
	if k.state != Enabled {
		return &Error{Kind: ErrStateConflict, Op: "Disable", ID: id,
			State: k.state, HasState: true, Detail: "要求 Enabled"}
	}
	m.sweep(now)
	m.applyCatchUp(k, now)
	k.state = Disabled
	m.maxNow = now
	return nil
}

// Enable 要求 Disabled，置 Enabled。若 P>0：d<=now 则 d 改为 now+P
// （期间错过的轮换不补建），d>now 则不变。
func (m *Manager) Enable(id string, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		return m.paramErr("Enable", id, "id 为空")
	}
	if !validNow(now) {
		return m.paramErr("Enable", id, fmt.Sprintf("now=%d 越界", now))
	}
	if now < m.maxNow {
		return m.clockErr("Enable", id, now)
	}
	k, err := m.lookupState("Enable", id, now)
	if err != nil {
		return err
	}
	if k.state != Disabled {
		return &Error{Kind: ErrStateConflict, Op: "Enable", ID: id,
			State: k.state, HasState: true, Detail: "要求 Disabled"}
	}
	m.sweep(now)
	k.state = Enabled
	if k.period > 0 && k.d <= now {
		k.d = now + k.period
	}
	m.maxNow = now
	return nil
}

// ScheduleDeletion 要求 w 在 [Wmin,Wmax] 内且状态为 Enabled 或 Disabled
// （Enabled 时先补建），置 Pending 并令 deleteAt=now+w。
func (m *Manager) ScheduleDeletion(id string, w, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		return m.paramErr("ScheduleDeletion", id, "id 为空")
	}
	if w < m.wmin || w > m.wmax {
		return m.paramErr("ScheduleDeletion", id,
			fmt.Sprintf("w=%d 不在 [%d,%d]", w, m.wmin, m.wmax))
	}
	if !validNow(now) {
		return m.paramErr("ScheduleDeletion", id, fmt.Sprintf("now=%d 越界", now))
	}
	if now < m.maxNow {
		return m.clockErr("ScheduleDeletion", id, now)
	}
	k, err := m.lookupState("ScheduleDeletion", id, now)
	if err != nil {
		return err
	}
	if k.state == Pending {
		return &Error{Kind: ErrStateConflict, Op: "ScheduleDeletion", ID: id,
			State: k.state, HasState: true, Detail: "要求 Enabled 或 Disabled"}
	}
	m.sweep(now)
	if k.state == Enabled {
		m.applyCatchUp(k, now)
	}
	k.state = Pending
	k.deleteAt = now + w
	m.seq++
	heap.Push(&m.pending, delEntry{deleteAt: k.deleteAt, seq: m.seq, id: id, gen: k.gen})
	k.heapSeq = m.seq
	m.maxNow = now
	return nil
}

// CancelDeletion 要求 Pending，置 Disabled（不是 Enabled），d 保持不变。
func (m *Manager) CancelDeletion(id string, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		return m.paramErr("CancelDeletion", id, "id 为空")
	}
	if !validNow(now) {
		return m.paramErr("CancelDeletion", id, fmt.Sprintf("now=%d 越界", now))
	}
	if now < m.maxNow {
		return m.clockErr("CancelDeletion", id, now)
	}
	k, err := m.lookupState("CancelDeletion", id, now)
	if err != nil {
		return err
	}
	if k.state != Pending {
		return &Error{Kind: ErrStateConflict, Op: "CancelDeletion", ID: id,
			State: k.state, HasState: true, Detail: "要求 Pending"}
	}
	m.sweep(now)
	k.state = Disabled
	k.heapSeq = -1 // 原堆条目作废，弹出时直接丢弃
	m.maxNow = now
	return nil
}

// Describe 只读：返回状态、世代、按 now 视角虚拟补建后的保留版本、
// d 与 deleteAt。不修改任何状态，也不推进全局时钟。
func (m *Manager) Describe(id string, now int64) (KeyView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		return KeyView{}, m.paramErr("Describe", id, "id 为空")
	}
	if !validNow(now) {
		return KeyView{}, m.paramErr("Describe", id, fmt.Sprintf("now=%d 越界", now))
	}
	if now < m.maxNow {
		return KeyView{}, m.clockErr("Describe", id, now)
	}
	k, err := m.lookupState("Describe", id, now)
	if err != nil {
		return KeyView{}, err
	}
	versions := k.versions
	d := k.d
	if k.state == Enabled && k.period > 0 && k.d <= now {
		versions, d, _ = catchUp(k.versions, k.d, k.period, now, m.v)
	}
	view := KeyView{
		State:        k.state,
		Generation:   k.gen,
		Versions:     append([]VersionInfo(nil), versions...),
		NextRotation: d,
		DeleteAt:     k.deleteAt,
	}
	return view, nil
}
