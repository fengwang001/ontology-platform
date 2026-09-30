package ontology

import (
	"io"
	"log"
	"os"
	"sync"
)

// AssetSpec 声明一个数据资产及其分区范围。
type AssetSpec struct {
	Name  string
	First int
	Last  int
}

// EdgeSpec 声明一条依赖边：下游 Down 的分区 d 读取上游 Up 的分区 [d+Lo, d+Hi]。
type EdgeSpec struct {
	Up   string
	Down string
	Lo   int
	Hi   int
}

// PartitionRef 定位一个资产分区。
type PartitionRef struct {
	Asset     string
	Partition int
}

// Planner 是分区级过期判定与回填规划器。
// 所有方法均可并发调用；判定基于调用时刻的一致快照。
type Planner struct {
	mu     sync.RWMutex
	logger *log.Logger

	assets map[string]*asset
	// byDepth 按 (层深, 资产名) 排序，供输出稳定排序使用。
	byDepth []*asset
	inputs  map[string][]*edge // 下游资产名 -> 其声明的全部入边

	parts   map[partKey]*partState
	runs    map[int64]*run
	nextRun int64
	// reverseIndex: 输入分区 -> 已物化并读取它的下游分区集合。
	reverseIndex map[partKey]map[partKey]bool
}

// Option 配置 Planner。
type Option func(*Planner)

// WithLogOutput 将判定日志写入给定输出。
func WithLogOutput(w io.Writer) Option {
	return func(p *Planner) {
		if w != nil {
			p.logger = log.New(w, "ontology ", log.LstdFlags|log.Lmicroseconds)
		}
	}
}

// New 构建规划器。任何非法声明都会整体拒绝，不产生可用实例。
func New(assets []AssetSpec, edges []EdgeSpec, opts ...Option) (*Planner, error) {
	p := &Planner{
		logger:       log.New(os.Stderr, "ontology ", log.LstdFlags|log.Lmicroseconds),
		assets:       map[string]*asset{},
		inputs:       map[string][]*edge{},
		parts:        map[partKey]*partState{},
		runs:         map[int64]*run{},
		reverseIndex: map[partKey]map[partKey]bool{},
	}
	for _, opt := range opts {
		opt(p)
	}
	if err := p.build(assets, edges); err != nil {
		return nil, err
	}
	return p, nil
}

// 方法 Plan / Impact 见 plan.go。
