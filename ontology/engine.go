package ontology

import (
	"sync"
	"sync/atomic"
)

// Instance 是一个待呈现的对象实例。
type Instance struct {
	ID    string
	Attrs map[string]Value
}

// AttrOutcome 是单个属性的呈现裁决记录。
type AttrOutcome struct {
	Present   bool
	Value     Value
	PolicyIDs []string // 据以裁决的策略
}

// Result 是一次呈现请求的完整结果。
type Result struct {
	Values   map[string]Value
	Outcome  map[string]AttrOutcome
	Errors   []ClassifiedError // 按固定优先级排序
	Examined int               // 本次考察的策略数量（可观测开销证明）
}

// snapshot 是不可变的策略集合视图；运行期变更通过整体替换实现原子生效。
type snapshot struct {
	schema map[string]AttrType
	vis    map[string]VisibilityPolicy
	mask   map[string]MaskingPolicy
	// 索引：按属性分组，保证单次呈现的开销与策略总数无关。
	visByAttr  map[string][]string
	maskByAttr map[string][]string
}

func emptySnapshot() *snapshot {
	return &snapshot{
		schema:     map[string]AttrType{},
		vis:        map[string]VisibilityPolicy{},
		mask:       map[string]MaskingPolicy{},
		visByAttr:  map[string][]string{},
		maskByAttr: map[string][]string{},
	}
}

func (s *snapshot) clone() *snapshot {
	n := &snapshot{
		schema:     make(map[string]AttrType, len(s.schema)),
		vis:        make(map[string]VisibilityPolicy, len(s.vis)),
		mask:       make(map[string]MaskingPolicy, len(s.mask)),
		visByAttr:  make(map[string][]string, len(s.visByAttr)),
		maskByAttr: make(map[string][]string, len(s.maskByAttr)),
	}
	for k, v := range s.schema {
		n.schema[k] = v
	}
	for k, v := range s.vis {
		n.vis[k] = v
	}
	for k, v := range s.mask {
		n.mask[k] = v
	}
	return n
}

func (s *snapshot) reindex() {
	s.visByAttr = map[string][]string{}
	s.maskByAttr = map[string][]string{}
	for id, p := range s.vis {
		s.visByAttr[p.Attr] = append(s.visByAttr[p.Attr], id)
	}
	for id, p := range s.mask {
		s.maskByAttr[p.Attr] = append(s.maskByAttr[p.Attr], id)
	}
	for _, ids := range s.visByAttr {
		sortStrings(ids)
	}
	for _, ids := range s.maskByAttr {
		sortStrings(ids)
	}
}

// Engine 是脱敏与可见性策略冲突裁决引擎，可安全并发使用。
type Engine struct {
	mu           sync.Mutex // 串行化所有变更，保证线性一致
	snap         atomic.Pointer[snapshot]
	defaultAllow bool
	logger       CallLogger
}

// NewEngine 创建引擎。defaultAllow 决定无可见性策略命中时的默认结论。
func NewEngine(defaultAllow bool) *Engine {
	e := &Engine{defaultAllow: defaultAllow, logger: NopLogger{}}
	e.snap.Store(emptySnapshot())
	return e
}

// SetLogger 设置调用日志记录器。
func (e *Engine) SetLogger(l CallLogger) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if l == nil {
		l = NopLogger{}
	}
	e.logger = l
}

func (e *Engine) mutate(fn func(s *snapshot)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := e.snap.Load().clone()
	fn(n)
	n.reindex()
	e.snap.Store(n)
}

// SetSchema 原子替换属性声明类型集合。
func (e *Engine) SetSchema(schema map[string]AttrType) {
	e.mutate(func(s *snapshot) {
		s.schema = map[string]AttrType{}
		for k, v := range schema {
			s.schema[k] = v
		}
	})
}

// RegisterVisibility 登记一条可见性策略；同 ID 覆盖。
func (e *Engine) RegisterVisibility(p VisibilityPolicy) {
	e.mutate(func(s *snapshot) { s.vis[p.ID] = p })
}

// RegisterMasking 登记一条脱敏策略；同 ID 覆盖。
func (e *Engine) RegisterMasking(p MaskingPolicy) {
	e.mutate(func(s *snapshot) { s.mask[p.ID] = p })
}

// Unregister 删除指定 ID 的策略（无论类别）。
func (e *Engine) Unregister(id string) {
	e.mutate(func(s *snapshot) {
		delete(s.vis, id)
		delete(s.mask, id)
	})
}

// PolicyBatch 描述一组原子生效的策略变更。
type PolicyBatch struct {
	SetSchema          map[string]AttrType
	RegisterVisibility []VisibilityPolicy
	RegisterMasking    []MaskingPolicy
	Unregister         []string
}

// Apply 将整批变更作为一次原子修改应用：
// 任何进行中的呈现请求要么看到全部旧策略，要么看到全部新策略。
func (e *Engine) Apply(b PolicyBatch) {
	e.mutate(func(s *snapshot) {
		if b.SetSchema != nil {
			s.schema = map[string]AttrType{}
			for k, v := range b.SetSchema {
				s.schema[k] = v
			}
		}
		for _, p := range b.RegisterVisibility {
			s.vis[p.ID] = p
		}
		for _, p := range b.RegisterMasking {
			s.mask[p.ID] = p
		}
		for _, id := range b.Unregister {
			delete(s.vis, id)
			delete(s.mask, id)
		}
	})
}

// Present 对 instance 面向 subject 做一次完整呈现。
// 整个请求只读取一次快照指针，保证单次请求内策略集合一致。
func (e *Engine) Present(subject string, inst Instance) Result {
	s := e.snap.Load()
	ev := &evaluator{
		snap:         s,
		subject:      subject,
		inst:         inst,
		defaultAllow: e.defaultAllow,
		memo:         map[string]derivedEntry{},
		onStack:      map[string]bool{},
	}
	res := ev.run()
	e.logger.LogCall(CallLogEntry{Subject: subject, Instance: inst, Result: res})
	return res
}
