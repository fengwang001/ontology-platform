package repo

import (
	"sync"

	"ontology/engine"
	"ontology/model"
)

// ErrChoice 重导出 model.ErrChoice，errors.Is 可统一区分。
var ErrChoice = model.ErrChoice

type version struct {
	graph *model.Graph
}

// Repo 保存各 defID 的不可变版本序列与实例。
type Repo struct {
	mu    sync.Mutex
	defs  map[string][]version
	insts map[string]*engine.Instance
}

func New() *Repo {
	return &Repo{
		defs:  map[string][]version{},
		insts: map[string]*engine.Instance{},
	}
}

// Define 校验并追加一个新版本；被拒不占版本号。
func (r *Repo) Define(defID string, g *model.Graph) error {
	if defID == "" || g == nil {
		return ErrInvalidArg
	}
	if err := g.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defs[defID] = append(r.defs[defID], version{graph: cloneGraph(g)})
	return nil
}

// Start 固定当前最新版本、校验 choices 并创建实例；任何失败都不创建。
func (r *Repo) Start(instID, defID string, choices map[int][]int) error {
	if instID == "" || defID == "" {
		return ErrInvalidArg
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	vers, ok := r.defs[defID]
	if !ok {
		return ErrDefNotFound
	}
	if _, exists := r.insts[instID]; exists {
		return ErrInstanceExist
	}
	g := vers[len(vers)-1].graph
	ch := model.Choice(choices)
	if err := g.ValidateChoice(ch); err != nil {
		return err
	}
	r.insts[instID] = engine.New(g, ch)
	return nil
}

// Complete 在指定实例上完成一次 Task。
func (r *Repo) Complete(instID string, t int) error {
	if instID == "" {
		return ErrInvalidArg
	}
	r.mu.Lock()
	in, ok := r.insts[instID]
	r.mu.Unlock()
	if !ok {
		return ErrNoInstance
	}
	return in.Complete(t)
}

// Status 只读返回实例状态。
func (r *Repo) Status(instID string) (engine.State, error) {
	if instID == "" {
		return engine.State{}, ErrInvalidArg
	}
	r.mu.Lock()
	in, ok := r.insts[instID]
	r.mu.Unlock()
	if !ok {
		return engine.State{}, ErrNoInstance
	}
	return in.Status(), nil
}

func cloneGraph(g *model.Graph) *model.Graph {
	cp := &model.Graph{N: g.N, Kinds: append([]model.NodeType(nil), g.Kinds...)}
	cp.Edges = append([]model.Edge(nil), g.Edges...)
	return cp
}
