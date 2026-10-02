package lifecycle

import (
	"math/big"
	"sync"
)

// 层级常量。
const (
	LayerHot  = iota // 热层
	LayerCool        // 凉层
	LayerCold        // 冷层
)

const periodLen = int64(30)
const maxKeyBytes = 64

// Config 描述分层存储生命周期计费器的全部配置参数。
type Config struct {
	P0 int64 // 热层日价（每单位大小每日）
	P1 int64 // 凉层日价
	P2 int64 // 冷层日价
	A  int64 // 热层转凉层天数
	B  int64 // 凉层转冷层天数（B>A）
	M1 int64 // 凉层最短停留天数
	M2 int64 // 冷层最短停留天数
	R1 int64 // 凉层检索单价
	R2 int64 // 冷层检索单价
	Q  int64 // 每周期（30 天）免费检索量
}

// Fees 是一次操作结清的费用明细，各字段非负。
type Fees struct {
	Storage *big.Int // 存储费
	Exit    *big.Int // 早离补费
	Retriev *big.Int // 检索费
}

// Object 是一个存储对象：大小与最近访问日。
type Object struct {
	Size int64
	LA   int64
}

// Billing 是生命周期计费器，可被并发调用。
type Billing struct {
	mu  sync.Mutex
	cfg Config

	maxNow int64
	period int64
	used   int64
	objs   map[string]Object
}

// Layer 根据最近访问日 la 与当前日期 now 推导对象当前所处层级。
// 层级只由 la 与 now 决定：Δ<A 为热层，A≤Δ<B 为凉层，Δ≥B 为冷层。
func Layer(la, now, A, B int64) int {
	d := now - la
	switch {
	case d < A:
		return LayerHot
	case d < B:
		return LayerCool
	default:
		return LayerCold
	}
}

// LayerStays 返回在各层的停留天数（热、凉、冷）。
// 热层停留 = min(Δ,A)，凉层停留 = max(0,min(Δ,B)-A)，冷层停留 = max(0,Δ-B)。
func LayerStays(la, now, A, B int64) (hot, cool, cold int64) {
	d := now - la
	if d < 0 {
		d = 0
	}
	hot = min64(d, A)
	cool = max64(0, min64(d, B)-A)
	cold = max64(0, d-B)
	return
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// settleFees 按规则计算存储费与早离补费（检索费不在 Settle 内）。
// 早离补费只看 now 所处的当前层，沿途已转出的层不产生补费。
// 输入参数已保证落在合法范围内，费用用 big.Int 精确计算。
func zeroFees() Fees {
	return Fees{Storage: big.NewInt(0), Exit: big.NewInt(0), Retriev: big.NewInt(0)}
}

func settleFees(cfg Config, obj Object, now int64) Fees {
	hot, cool, cold := LayerStays(obj.LA, now, cfg.A, cfg.B)

	storage := big.NewInt(obj.Size)
	storage.Mul(storage, big.NewInt(cfg.P0*hot+cfg.P1*cool+cfg.P2*cold))

	var exit *big.Int
	delta := now - obj.LA
	switch Layer(obj.LA, now, cfg.A, cfg.B) {
	case LayerHot:
		exit = big.NewInt(0)
	case LayerCool:
		stay := delta - cfg.A
		exit = big.NewInt(max64(0, cfg.M1-stay))
		exit.Mul(exit, big.NewInt(cfg.P1*obj.Size))
	default: // LayerCold
		stay := delta - cfg.B
		exit = big.NewInt(max64(0, cfg.M2-stay))
		exit.Mul(exit, big.NewInt(cfg.P2*obj.Size))
	}

	return Fees{Storage: storage, Exit: exit, Retriev: big.NewInt(0)}
}

func validConfig(cfg Config) bool {
	ranges := [][2]int64{
		{cfg.P0, 1e6}, {cfg.P1, 1e6}, {cfg.P2, 1e6},
		{cfg.M1, 1e6}, {cfg.M2, 1e6},
		{cfg.R1, 1e6}, {cfg.R2, 1e6},
	}
	for _, r := range ranges {
		if r[0] < 0 || r[0] > r[1] {
			return false
		}
	}
	if cfg.A < 1 || cfg.B <= cfg.A || cfg.B > 1e6 {
		return false
	}
	if cfg.Q < 0 || cfg.Q > 1e12 {
		return false
	}
	return true
}

func validKey(key string) bool {
	return key != "" && len(key) <= maxKeyBytes
}

func validSize(size int64) bool { return size >= 1 && size <= 1e6 }

func validNow(now int64) bool { return now >= 0 && now <= 1e9 }

// 可区分的拒绝原因。
var (
	ErrInvalidConfig = errInvalidConfig{}
	ErrInvalidArgs   = errInvalidArgs{}
	ErrClockRollback = errClockRollback{}
	ErrNotFound      = errNotFound{}
)

// New 校验配置并构造计费器；配置越界时整体拒绝。
func New(cfg Config) (*Billing, error) {
	if !validConfig(cfg) {
		return nil, ErrInvalidConfig
	}
	return &Billing{
		cfg:    cfg,
		period: 0,
		objs:   make(map[string]Object),
	}, nil
}

// Put 建立对象；键已存在时先结清旧对象费用再覆盖。
func (b *Billing) Put(key string, size, now int64) (Fees, error) {
	if !validKey(key) || !validSize(size) || !validNow(now) {
		return zeroFees(), ErrInvalidArgs
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if now < b.maxNow {
		return zeroFees(), ErrClockRollback
	}

	fees := zeroFees()
	if old, ok := b.objs[key]; ok {
		fees = settleFees(b.cfg, old, now)
	}
	b.objs[key] = Object{Size: size, LA: now}
	b.maxNow = now
	return fees, nil
}

// Delete 结清存储费与补费后移除对象。
func (b *Billing) Delete(key string, now int64) (Fees, error) {
	if !validKey(key) || !validNow(now) {
		return zeroFees(), ErrInvalidArgs
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if now < b.maxNow {
		return zeroFees(), ErrClockRollback
	}

	obj, ok := b.objs[key]
	if !ok {
		return zeroFees(), ErrNotFound
	}

	fees := settleFees(b.cfg, obj, now)
	delete(b.objs, key)
	b.maxNow = now
	return fees, nil
}

// Get 结清费用后按当前层计检索费，并把最近访问日重置为 now。
func (b *Billing) Get(key string, now int64) (Fees, error) {
	if !validKey(key) || !validNow(now) {
		return zeroFees(), ErrInvalidArgs
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if now < b.maxNow {
		return zeroFees(), ErrClockRollback
	}

	if _, ok := b.objs[key]; !ok {
		return zeroFees(), ErrNotFound
	}
	return b.getLocked(key, now)
}

// GetMany 按给定次序依次执行 Get；任一键不存在则整体拒绝。
func (b *Billing) GetMany(keys []string, now int64) ([]Fees, error) {
	for _, key := range keys {
		if !validKey(key) {
			return nil, ErrInvalidArgs
		}
	}
	if !validNow(now) {
		return nil, ErrInvalidArgs
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if now < b.maxNow {
		return nil, ErrClockRollback
	}

	// 全部键都存在才执行；报告第一个不存在的键。
	for _, key := range keys {
		if _, ok := b.objs[key]; !ok {
			return nil, ErrNotFound
		}
	}

	all := make([]Fees, len(keys))
	for i, key := range keys {
		fees, err := b.getLocked(key, now)
		if err != nil {
			return nil, err
		}
		all[i] = fees
	}
	return all, nil
}

// getLocked 是 Get 在已持锁、已做参数/时钟/存在性校验后的核心部分。
func (b *Billing) getLocked(key string, now int64) (Fees, error) {
	obj := b.objs[key]
	fees := settleFees(b.cfg, obj, now)

	layer := Layer(obj.LA, now, b.cfg.A, b.cfg.B)
	if layer == LayerCool || layer == LayerCold {
		p := now / periodLen
		if p != b.period {
			b.period = p
			b.used = 0
		}
		rate := b.cfg.R1
		if layer == LayerCold {
			rate = b.cfg.R2
		}
		free := min64(obj.Size, b.cfg.Q-b.used)
		b.used += free
		fees.Retriev = big.NewInt((obj.Size - free) * rate)
	}

	obj.LA = now
	b.objs[key] = obj
	b.maxNow = now
	return fees, nil
}

// Settle 是纯查询：给定配置、对象与 now，返回该对象此刻结清的存储费与补费。
// 它不改变计费器状态，要求 now≥0 且 now≥la。
func Settle(cfg Config, obj Object, now int64) (Fees, error) {
	if !validConfig(cfg) || !validSize(obj.Size) || !validNow(now) || now < obj.LA || obj.LA < 0 {
		return zeroFees(), ErrInvalidArgs
	}
	return settleFees(cfg, obj, now), nil
}
