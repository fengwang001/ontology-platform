package kms

// 朴素模拟：按规格逐次轮换、逐个创建版本（P 取小值以便展开），
// 不用堆、不用公式跳跃，用于与 Manager 的优化实现做随机对照。

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

type nkey struct {
	gen      int64
	state    State
	period   int64
	versions []VersionInfo
	d        int64
	deleteAt int64
}

type naive struct {
	v, wmin, wmax, kmax int64
	maxNow              int64
	keys                map[string]*nkey
	maxGen              map[string]int64
}

func newNaive(v, wmin, wmax, kmax int64) *naive {
	return &naive{v: v, wmin: wmin, wmax: wmax, kmax: kmax,
		keys: make(map[string]*nkey), maxGen: make(map[string]int64)}
}

func (n *naive) lookup(id string, now int64) *nkey {
	k := n.keys[id]
	if k == nil {
		return nil
	}
	if k.state == Pending && k.deleteAt <= now {
		return nil
	}
	return k
}

// catchUp 逐次轮换：每到期一次就创建一个新版本并淘汰最小版本号。
func (n *naive) catchUp(k *nkey, now int64) {
	if k.state != Enabled || k.period <= 0 {
		return
	}
	for k.d <= now {
		maxNum := k.versions[len(k.versions)-1].Number
		k.versions = append(k.versions, VersionInfo{Number: maxNum + 1, CreatedAt: k.d})
		for int64(len(k.versions)) > n.v {
			k.versions = k.versions[1:]
		}
		k.d += k.period
	}
}

func (n *naive) Create(id string, period, now int64) error {
	if id == "" || !validPeriod(period) || !validNow(now) {
		return &Error{Kind: ErrInvalidParam, Op: "Create", ID: id}
	}
	if now < n.maxNow {
		return &Error{Kind: ErrClockRollback, Op: "Create", ID: id}
	}
	if k := n.lookup(id, now); k != nil {
		return &Error{Kind: ErrStateConflict, Op: "Create", ID: id, State: k.state, HasState: true}
	}
	alive := int64(0)
	for _, k := range n.keys {
		if !(k.state == Pending && k.deleteAt <= now) {
			alive++
		}
	}
	if alive >= n.kmax {
		return &Error{Kind: ErrCapacityExceeded, Op: "Create", ID: id}
	}
	gen := n.maxGen[id] + 1
	n.keys[id] = &nkey{gen: gen, state: Enabled, period: period,
		versions: []VersionInfo{{Number: 1, CreatedAt: now}}, d: now + period}
	n.maxGen[id] = gen
	n.maxNow = now
	return nil
}

func (n *naive) Encrypt(id string, now int64) (Credential, error) {
	if id == "" || !validNow(now) {
		return Credential{}, &Error{Kind: ErrInvalidParam, Op: "Encrypt", ID: id}
	}
	if now < n.maxNow {
		return Credential{}, &Error{Kind: ErrClockRollback, Op: "Encrypt", ID: id}
	}
	k := n.lookup(id, now)
	if k == nil {
		return Credential{}, &Error{Kind: ErrNotFound, Op: "Encrypt", ID: id}
	}
	if k.state != Enabled {
		return Credential{}, &Error{Kind: ErrStateRejected, Op: "Encrypt", ID: id, State: k.state, HasState: true}
	}
	n.catchUp(k, now)
	n.maxNow = now
	return Credential{ID: id, Gen: k.gen, Version: k.versions[len(k.versions)-1].Number}, nil
}

func (n *naive) checkCred(op string, cred Credential, now int64) (*nkey, *Error) {
	mg, ok := n.maxGen[cred.ID]
	if !ok || cred.Gen > mg {
		return nil, &Error{Kind: ErrNotFound, Op: op, ID: cred.ID}
	}
	k := n.lookup(cred.ID, now)
	if k == nil || k.gen != cred.Gen {
		return nil, &Error{Kind: ErrKeyDeleted, Op: op, ID: cred.ID}
	}
	if k.state != Enabled {
		return nil, &Error{Kind: ErrStateRejected, Op: op, ID: cred.ID, State: k.state, HasState: true}
	}
	return k, nil
}

// catchUpCopy 在副本上逐次轮换，供凭据版本检查使用（拒绝则丢弃）。
func (n *naive) catchUpCopy(k *nkey, now int64) ([]VersionInfo, int64) {
	versions := append([]VersionInfo(nil), k.versions...)
	d := k.d
	if k.period > 0 {
		for d <= now {
			maxNum := versions[len(versions)-1].Number
			versions = append(versions, VersionInfo{Number: maxNum + 1, CreatedAt: d})
			for int64(len(versions)) > n.v {
				versions = versions[1:]
			}
			d += k.period
		}
	}
	return versions, d
}

func (n *naive) Decrypt(cred Credential, now int64) (bool, error) {
	if cred.ID == "" || cred.Gen < 1 || cred.Version < 1 || !validNow(now) {
		return false, &Error{Kind: ErrInvalidParam, Op: "Decrypt", ID: cred.ID}
	}
	if now < n.maxNow {
		return false, &Error{Kind: ErrClockRollback, Op: "Decrypt", ID: cred.ID}
	}
	k, err := n.checkCred("Decrypt", cred, now)
	if err != nil {
		return false, err
	}
	versions, d := n.catchUpCopy(k, now)
	maxV := versions[len(versions)-1].Number
	minV := versions[0].Number
	if cred.Version > maxV {
		return false, &Error{Kind: ErrVersionNotFound, Op: "Decrypt", ID: cred.ID}
	}
	if cred.Version < minV {
		return false, &Error{Kind: ErrVersionRetired, Op: "Decrypt", ID: cred.ID}
	}
	k.versions = versions
	k.d = d
	n.maxNow = now
	return cred.Version == maxV, nil
}

func (n *naive) ReEncrypt(cred Credential, now int64) (Credential, bool, error) {
	if cred.ID == "" || cred.Gen < 1 || cred.Version < 1 || !validNow(now) {
		return Credential{}, false, &Error{Kind: ErrInvalidParam, Op: "ReEncrypt", ID: cred.ID}
	}
	if now < n.maxNow {
		return Credential{}, false, &Error{Kind: ErrClockRollback, Op: "ReEncrypt", ID: cred.ID}
	}
	k, err := n.checkCred("ReEncrypt", cred, now)
	if err != nil {
		return Credential{}, false, err
	}
	versions, d := n.catchUpCopy(k, now)
	maxV := versions[len(versions)-1].Number
	minV := versions[0].Number
	if cred.Version > maxV {
		return Credential{}, false, &Error{Kind: ErrVersionNotFound, Op: "ReEncrypt", ID: cred.ID}
	}
	if cred.Version < minV {
		return Credential{}, false, &Error{Kind: ErrVersionRetired, Op: "ReEncrypt", ID: cred.ID}
	}
	k.versions = versions
	k.d = d
	n.maxNow = now
	return Credential{ID: cred.ID, Gen: k.gen, Version: maxV}, cred.Version != maxV, nil
}

func (n *naive) Disable(id string, now int64) error {
	if id == "" || !validNow(now) {
		return &Error{Kind: ErrInvalidParam, Op: "Disable", ID: id}
	}
	if now < n.maxNow {
		return &Error{Kind: ErrClockRollback, Op: "Disable", ID: id}
	}
	k := n.lookup(id, now)
	if k == nil {
		return &Error{Kind: ErrNotFound, Op: "Disable", ID: id}
	}
	if k.state != Enabled {
		return &Error{Kind: ErrStateConflict, Op: "Disable", ID: id, State: k.state, HasState: true}
	}
	n.catchUp(k, now)
	k.state = Disabled
	n.maxNow = now
	return nil
}

func (n *naive) Enable(id string, now int64) error {
	if id == "" || !validNow(now) {
		return &Error{Kind: ErrInvalidParam, Op: "Enable", ID: id}
	}
	if now < n.maxNow {
		return &Error{Kind: ErrClockRollback, Op: "Enable", ID: id}
	}
	k := n.lookup(id, now)
	if k == nil {
		return &Error{Kind: ErrNotFound, Op: "Enable", ID: id}
	}
	if k.state != Disabled {
		return &Error{Kind: ErrStateConflict, Op: "Enable", ID: id, State: k.state, HasState: true}
	}
	k.state = Enabled
	if k.period > 0 && k.d <= now {
		k.d = now + k.period
	}
	n.maxNow = now
	return nil
}

func (n *naive) ScheduleDeletion(id string, w, now int64) error {
	if id == "" || w < n.wmin || w > n.wmax || !validNow(now) {
		return &Error{Kind: ErrInvalidParam, Op: "ScheduleDeletion", ID: id}
	}
	if now < n.maxNow {
		return &Error{Kind: ErrClockRollback, Op: "ScheduleDeletion", ID: id}
	}
	k := n.lookup(id, now)
	if k == nil {
		return &Error{Kind: ErrNotFound, Op: "ScheduleDeletion", ID: id}
	}
	if k.state == Pending {
		return &Error{Kind: ErrStateConflict, Op: "ScheduleDeletion", ID: id, State: k.state, HasState: true}
	}
	if k.state == Enabled {
		n.catchUp(k, now)
	}
	k.state = Pending
	k.deleteAt = now + w
	n.maxNow = now
	return nil
}

func (n *naive) CancelDeletion(id string, now int64) error {
	if id == "" || !validNow(now) {
		return &Error{Kind: ErrInvalidParam, Op: "CancelDeletion", ID: id}
	}
	if now < n.maxNow {
		return &Error{Kind: ErrClockRollback, Op: "CancelDeletion", ID: id}
	}
	k := n.lookup(id, now)
	if k == nil {
		return &Error{Kind: ErrNotFound, Op: "CancelDeletion", ID: id}
	}
	if k.state != Pending {
		return &Error{Kind: ErrStateConflict, Op: "CancelDeletion", ID: id, State: k.state, HasState: true}
	}
	k.state = Disabled
	n.maxNow = now
	return nil
}

func (n *naive) Describe(id string, now int64) (KeyView, error) {
	if id == "" || !validNow(now) {
		return KeyView{}, &Error{Kind: ErrInvalidParam, Op: "Describe", ID: id}
	}
	if now < n.maxNow {
		return KeyView{}, &Error{Kind: ErrClockRollback, Op: "Describe", ID: id}
	}
	k := n.lookup(id, now)
	if k == nil {
		return KeyView{}, &Error{Kind: ErrNotFound, Op: "Describe", ID: id}
	}
	versions := k.versions
	d := k.d
	if k.state == Enabled {
		versions, d = n.catchUpCopy(k, now)
	}
	return KeyView{State: k.state, Generation: k.gen,
		Versions:     append([]VersionInfo(nil), versions...),
		NextRotation: d, DeleteAt: k.deleteAt}, nil
}

// rndOp 为一条随机操作。
type rndOp struct {
	name   string
	id     string
	period int64
	w      int64
	now    int64
	cred   Credential
}

func (o rndOp) String() string {
	switch o.name {
	case "Create":
		return fmt.Sprintf("Create(%q, P=%d, now=%d)", o.id, o.period, o.now)
	case "ScheduleDeletion":
		return fmt.Sprintf("ScheduleDeletion(%q, w=%d, now=%d)", o.id, o.w, o.now)
	case "Decrypt", "ReEncrypt":
		return fmt.Sprintf("%s(%+v, now=%d)", o.name, o.cred, o.now)
	default:
		return fmt.Sprintf("%s(%q, now=%d)", o.name, o.id, o.now)
	}
}

// opResult 为可比较的操作结果（错误归约为类别与状态）。
type opResult struct {
	cred     Credential
	isMax    bool
	changed  bool
	view     KeyView
	hasView  bool
	errKind  ErrKind
	errState State
	hasErr   bool
	hasState bool
}

func normErr(err error) (ErrKind, State, bool, bool) {
	if err == nil {
		return 0, 0, false, false
	}
	ke, ok := err.(*Error)
	if !ok {
		return -1, 0, true, false
	}
	return ke.Kind, ke.State, true, ke.HasState
}

func applyReal(m *Manager, o rndOp) opResult {
	var r opResult
	var err error
	switch o.name {
	case "Create":
		err = m.Create(o.id, o.period, o.now)
	case "Encrypt":
		r.cred, err = m.Encrypt(o.id, o.now)
	case "Decrypt":
		r.isMax, err = m.Decrypt(o.cred, o.now)
	case "ReEncrypt":
		r.cred, r.changed, err = m.ReEncrypt(o.cred, o.now)
	case "Disable":
		err = m.Disable(o.id, o.now)
	case "Enable":
		err = m.Enable(o.id, o.now)
	case "ScheduleDeletion":
		err = m.ScheduleDeletion(o.id, o.w, o.now)
	case "CancelDeletion":
		err = m.CancelDeletion(o.id, o.now)
	case "Describe":
		r.view, err = m.Describe(o.id, o.now)
		r.hasView = err == nil
	}
	r.errKind, r.errState, r.hasErr, r.hasState = normErr(err)
	return r
}

func applyNaive(n *naive, o rndOp) opResult {
	var r opResult
	var err error
	switch o.name {
	case "Create":
		err = n.Create(o.id, o.period, o.now)
	case "Encrypt":
		r.cred, err = n.Encrypt(o.id, o.now)
	case "Decrypt":
		r.isMax, err = n.Decrypt(o.cred, o.now)
	case "ReEncrypt":
		r.cred, r.changed, err = n.ReEncrypt(o.cred, o.now)
	case "Disable":
		err = n.Disable(o.id, o.now)
	case "Enable":
		err = n.Enable(o.id, o.now)
	case "ScheduleDeletion":
		err = n.ScheduleDeletion(o.id, o.w, o.now)
	case "CancelDeletion":
		err = n.CancelDeletion(o.id, o.now)
	case "Describe":
		r.view, err = n.Describe(o.id, o.now)
		r.hasView = err == nil
	}
	r.errKind, r.errState, r.hasErr, r.hasState = normErr(err)
	return r
}

var opNames = []string{"Create", "Encrypt", "Decrypt", "ReEncrypt", "Disable",
	"Enable", "ScheduleDeletion", "CancelDeletion", "Describe"}

func genOp(rng *rand.Rand, creds []Credential, now, wmin, wmax int64) rndOp {
	o := rndOp{name: opNames[rng.Intn(len(opNames))], now: now}
	ids := []string{"a", "b", "c", "d"}
	o.id = ids[rng.Intn(len(ids))]
	if rng.Intn(100) < 3 {
		o.id = "" // 偶发空 id 触发参数非法
	}
	periods := []int64{0, 60, 60, 61, 120, 300, 1_000_000_000, 59, -1}
	o.period = periods[rng.Intn(len(periods))]
	ws := []int64{wmin - 1, wmin, wmax, wmax + 1, (wmin + wmax) / 2, 0}
	o.w = ws[rng.Intn(len(ws))]
	switch {
	case len(creds) > 0 && rng.Intn(100) < 70:
		o.cred = creds[rng.Intn(len(creds))]
		switch rng.Intn(4) {
		case 0: // 版本偏移
			o.cred.Version += int64(rng.Intn(5)) - 2
		case 1: // 世代偏移
			o.cred.Gen += int64(rng.Intn(3)) - 1
		}
	default: // 捏造凭据
		o.cred = Credential{
			ID:      ids[rng.Intn(len(ids))],
			Gen:     int64(rng.Intn(4)),
			Version: int64(rng.Intn(10)),
		}
		if rng.Intn(100) < 10 {
			o.cred.ID = "ghost"
		}
	}
	return o
}

// 与朴素模拟对照 2000 组随机操作序列。
func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	for s := 0; s < sequences; s++ {
		rng := rand.New(rand.NewSource(int64(s)))
		v := int64(1 + rng.Intn(8))
		wmin := int64(1 + rng.Intn(50))
		wmax := wmin + int64(rng.Intn(200))
		kmax := int64(1 + rng.Intn(4))
		m, err := NewManager(v, wmin, wmax, kmax)
		if err != nil {
			t.Fatal(err)
		}
		n := newNaive(v, wmin, wmax, kmax)
		var creds []Credential
		now := int64(0)
		ops := 30 + rng.Intn(70)
		verbose := s < 2
		if verbose {
			t.Logf("序列 %d：V=%d Wmin=%d Wmax=%d Kmax=%d，%d 个操作", s, v, wmin, wmax, kmax, ops)
		}
		for i := 0; i < ops; i++ {
			if rng.Intn(100) < 5 { // 偶发时钟回退
				now -= int64(rng.Intn(50))
				if now < 0 {
					now = 0
				}
			} else {
				now += int64(rng.Intn(300))
			}
			o := genOp(rng, creds, now, wmin, wmax)
			r1 := applyReal(m, o)
			r2 := applyNaive(n, o)
			if verbose {
				verdict := "成功"
				if r1.hasErr {
					verdict = "拒绝：" + r1.errKind.String()
					if r1.hasState {
						verdict += "（当前状态 " + r1.errState.String() + "）"
					}
				}
				t.Logf("  #%d %s => %s cred=%+v isMax=%v changed=%v view=%+v",
					i, o, verdict, r1.cred, r1.isMax, r1.changed, r1.view)
			}
			if !reflect.DeepEqual(r1, r2) {
				t.Fatalf("序列 %d 操作 #%d 不一致：\n输入 %s\n优化实现 %+v\n朴素模拟 %+v",
					s, i, o, r1, r2)
			}
			if !r1.hasErr && (o.name == "Encrypt" || o.name == "ReEncrypt") {
				creds = append(creds, r1.cred)
			}
		}
	}
}
