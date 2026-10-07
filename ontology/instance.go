package ontology

// Baseline 是一次尝试所依据的判定基线：读取时的版本与权限高水位。
type Baseline struct {
	Version   int64
	HighWater Privilege
}

// Instance 是单个对象实例的 CAS 状态。
// 除版本与属性外，额外维护一个不对外暴露的“权限高水位”：
// 自版本 0 以来生效过的最高写权限。它使抢占判定为 O(1)，
// 且不随并发竞争该实例的动作总数增长。
type Instance struct {
	name      string
	mu        chan struct{}
	version   int64
	attrs     Attrs
	highWater Privilege
	// baseWater[v] = 产生版本 v 的那次写入“生效之前”的高水位。
	// 读基线携带 baseWater[version]，使抢占判定可区分“把基线推进到
	// 当前版本的写入者”与历史更早的写入者。该切片大小仅随版本数增长，
	// 与并发竞争动作总数无关（判定本身 O(1)）。
	baseWater []Privilege
}

func newInstance(name string) *Instance {
	return &Instance{
		name:      name,
		mu:        make(chan struct{}, 1),
		attrs:     Attrs{},
		baseWater: []Privilege{0},
	}
}

func (in *Instance) lock()   { in.mu <- struct{}{} }
func (in *Instance) unlock() { <-in.mu }

func (in *Instance) snapshot() Baseline {
	hw := Privilege(0)
	if int(in.version) < len(in.baseWater) {
		hw = in.baseWater[in.version]
	}
	return Baseline{Version: in.version, HighWater: hw}
}

// tryCommit 依据给定基线进行单次判定，调用方必须持锁。
//
// 判定顺序（严格按此顺序）：
//  1. 版本匹配：提交生效，推进版本与权限高水位。
//  2. 抢占：基线已被推进，且自该基线以来存在权限严格高于本动作的
//     生效写入。等价的 O(1) 判定为
//     highWater > base.HighWater && highWater > priv。
//     抢占终止不修改任何状态，也不消耗重试预算。
//  3. 其余情况为普通版本冲突，返回当前基线供调用方重试。
func (in *Instance) tryCommit(priv Privilege, base Baseline, next Attrs) (verdict, Baseline) {
	// 抢占优先于一切（也优先于成功提交与预算耗尽）：
	// 只要本动作读取到的基线曾被权限严格更高的写入推进
	// （highWater > base.HighWater 且 highWater > priv），
	// 无论版本是否匹配都立即终止。这覆盖“读基线时更高权限写入
	// 已经生效”的情形。
	if in.highWater > base.HighWater && in.highWater > priv {
		return verdictPreempted, Baseline{}
	}
	if in.version == base.Version {
		oldWater := in.highWater
		if priv > in.highWater {
			in.highWater = priv
		}
		in.version++
		in.attrs = next
		// baseWater[newVersion] = 新版本由“生效前”的什么状态产生。
		in.baseWater = append(in.baseWater, oldWater)
		return verdictCommitted, in.snapshot()
	}
	if in.highWater > base.HighWater && in.highWater > priv {
		return verdictPreempted, Baseline{}
	}
	return verdictConflict, in.snapshot()
}

func (in *Instance) readState() (int64, Privilege, Attrs) {
	in.lock()
	defer in.unlock()
	attrs := make(Attrs, len(in.attrs))
	for key, value := range in.attrs {
		attrs[key] = value
	}
	return in.version, in.highWater, attrs
}
