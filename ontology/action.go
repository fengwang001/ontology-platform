package ontology

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// ActionType 是一个动作类型的声明。
//
// 声明在注册时被分析并缓存分析结果，之后不可变；
// 前置条件与后置条件是两个独立的列表，各自独立演进。
type ActionType struct {
	ID string
	// Preconditions 全部通过才允许生成写入计划。
	Preconditions []PreCondition
	// Postconditions 在写入计划全部生成完毕后统一校验。
	Postconditions []PostCondition
	// Apply 基于已提交快照生成写入计划；不得直接触碰存储层。
	// 可以为 nil（纯校验动作，写入计划为空）。
	Apply func(*ApplyContext) error

	// analysis 是注册时缓存的声明分析结果。
	analysis DeclarationAnalysis
}

// Analysis 返回注册时缓存的声明分析结果。
func (a *ActionType) Analysis() DeclarationAnalysis { return a.analysis }

// RegisterOption 调整注册行为。
type RegisterOption func(*registerConfig)

type registerConfig struct {
	permitContradictory bool
}

// PermitContradictory 允许注册一个已被判定自相矛盾的声明。
//
// 正常路径下矛盾声明在注册时即被拒绝（定义阶段暴露配置错误）；
// 该选项用于演示/测试运行期的 RejectDeclarationContradiction 类别：
// 被强制注册的矛盾动作在执行时会在最早阶段被拒绝，优先于其余三类。
func PermitContradictory() RegisterOption {
	return func(c *registerConfig) { c.permitContradictory = true }
}

// Registry 是动作类型注册表。
type Registry struct {
	mu      sync.RWMutex
	actions map[string]*ActionType
	// analysisRuns 记录声明分析被执行的次数，用于验证分析开销
	// 只随定义次数增长、与调用次数无关。
	analysisRuns atomic.Int64
}

// NewRegistry 创建空的注册表。
func NewRegistry() *Registry {
	return &Registry{actions: make(map[string]*ActionType)}
}

// Register 注册一个动作类型。
//
// 注册时执行结构校验与矛盾分析；若声明自相矛盾且未使用
// PermitContradictory，返回 *Contradiction 错误且不予注册。
func (r *Registry) Register(at ActionType, opts ...RegisterOption) error {
	var cfg registerConfig
	for _, o := range opts {
		o(&cfg)
	}
	if err := validateDeclaration(&at); err != nil {
		return err
	}
	r.analysisRuns.Add(1)
	at.analysis = analyzeDeclaration(at.Preconditions, at.Postconditions)
	if at.analysis.Contradiction != nil && !cfg.permitContradictory {
		return at.analysis.Contradiction
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.actions[at.ID]; dup {
		return fmt.Errorf("ontology: action type %q already registered", at.ID)
	}
	stored := at
	r.actions[at.ID] = &stored
	return nil
}

// Get 返回已注册的动作类型。
func (r *Registry) Get(id string) (*ActionType, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	at, ok := r.actions[id]
	return at, ok
}

// AnalysisRuns 返回声明分析累计执行次数。
// 该值只应随 Register 调用增长，与动作被调用的次数无关。
func (r *Registry) AnalysisRuns() int64 { return r.analysisRuns.Load() }
