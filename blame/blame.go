package blame

import (
	"sort"

	"ontology/dag"
	"ontology/fresh"
)

// Kind 是根因类型；排序时 Self 在 Unreachable 之前。
type Kind int

const (
	Self Kind = iota
	Unreachable
)

func (kd Kind) String() string {
	switch kd {
	case Self:
		return "Self"
	case Unreachable:
		return "Unreachable"
	default:
		return "Unknown"
	}
}

// Root 是归因终点：根因数据集与类型。
type Root struct {
	Dataset string
	Kind    Kind
}

// Alert 是一次 Evaluate 中同一 (root,kind,k) 键聚合出的一条告警，
// Affected 只含本次新归入该键的数据集（升序）。
type Alert struct {
	K        int64
	Root     Root
	Affected []string
}

type frozenKey struct {
	name string
	k    int64
}

// Attributor 在 fresh.Tracker 之上做根因归因与告警聚合去重。
// Land/Evaluate 经由嵌入 Graph 的同一把锁串行化，等价于某个全局串行顺序。
type Attributor struct {
	*fresh.Tracker
	frozen map[frozenKey]Root
}

// New 构造周期长度为 T 秒的归因器。
func New(T int64) (*Attributor, error) {
	tr, err := fresh.New(T)
	if err != nil {
		return nil, err
	}
	return &Attributor{Tracker: tr, frozen: make(map[frozenKey]Root)}, nil
}

// AddDataset 委托给底层图。
func (a *Attributor) AddDataset(name string, off, dur int64, parents []string) error {
	return a.Tracker.Graph.AddDataset(name, off, dur, parents)
}

// trace 从违约节点 x 沿关键父单链追溯（调用方持 Graph 锁）。
// 不变量：除终点外路径上每个节点在 t 均违约；Self 终点自身必违约。
func (a *Attributor) trace(x string, k, t int64) Root {
	for {
		d, _ := a.Tracker.DatasetLocked(x)
		if len(d.Parents) == 0 {
			return Root{Dataset: x, Kind: Self}
		}

		// ready = 各父第 k 期落地时刻最大值；有父未落地则为无穷。
		// Parents 已按名字节序升序，未落地父取第一个即最小者。
		var ready int64
		readyFinite := true
		var missingParent string
		for _, p := range d.Parents {
			pt, landed := a.Tracker.LandedLocked(p, k)
			if !landed {
				readyFinite = false
				if missingParent == "" {
					missingParent = p
				}
				continue
			}
			if pt > ready {
				ready = pt
			}
		}

		if readyFinite {
			if ready+d.Dur <= a.Tracker.DeadlineLocked(x, k) {
				return Root{Dataset: x, Kind: Self} // 上游给足了时间
			}
			// 关键父：落地最晚者，并列取名字最小者（有序遍历首个即最小）。
			for _, p := range d.Parents {
				if pt, _ := a.Tracker.LandedLocked(p, k); pt == ready {
					missingParent = p
					break
				}
			}
		}

		if !a.Tracker.ViolatingLocked(missingParent, k, t) {
			return Root{Dataset: x, Kind: Unreachable} // 上游按时也不够用
		}
		x = missingParent // 关键父违约：继续向上
	}
}

// Evaluate 推进时钟，对所有新违约逐个归因、冻结，并按 (root,kind,k)
// 聚合成告警。输出按 (k, root 名字节序, Self 先于 Unreachable) 升序。
func (a *Attributor) Evaluate(now int64) ([]Alert, error) {
	a.Tracker.Lock()
	defer a.Tracker.Unlock()

	viols, err := a.Tracker.DetectLocked(now)
	if err != nil {
		return nil, err
	}

	// 在同一把锁内完成 检测→归因→冻结，保证快照一致、整体等价串行。
	type gk struct {
		root Root
		k    int64
	}
	groups := make(map[gk][]string)
	keys := make([]gk, 0)
	for _, v := range viols {
		root := a.trace(v.Dataset, v.K, now)
		a.frozen[frozenKey{v.Dataset, v.K}] = root
		a.Tracker.MarkAlertedLocked(v.Dataset, v.K)
		key := gk{root: root, k: v.K}
		if _, exists := groups[key]; !exists {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], v.Dataset)
	}

	alerts := make([]Alert, 0, len(keys))
	for _, key := range keys {
		list := groups[key]
		sort.Strings(list)
		alerts = append(alerts, Alert{K: key.k, Root: key.root, Affected: list})
	}
	sort.Slice(alerts, func(i, j int) bool {
		if alerts[i].K != alerts[j].K {
			return alerts[i].K < alerts[j].K
		}
		if alerts[i].Root.Dataset != alerts[j].Root.Dataset {
			return alerts[i].Root.Dataset < alerts[j].Root.Dataset
		}
		return alerts[i].Root.Kind < alerts[j].Root.Kind
	})
	return alerts, nil
}

// Blame 只读：返回 (name,k) 首次告警时冻结的根因；未告警报 ErrNotAlerted。
// 拒绝次序：参数非法 > 数据集不存在 > ErrNotAlerted。
func (a *Attributor) Blame(name string, k int64) (Root, error) {
	if name == "" || k < 0 {
		return Root{}, dag.ErrInvalid
	}
	a.Tracker.Lock()
	defer a.Tracker.Unlock()
	if !a.Tracker.HasLocked(name) {
		return Root{}, dag.ErrNoDataset
	}
	r, ok := a.frozen[frozenKey{name, k}]
	if !ok {
		return Root{}, dag.ErrNotAlerted
	}
	return r, nil
}
