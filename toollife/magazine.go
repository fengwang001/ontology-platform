package toollife

import "sync"

// Tool 是组内一把刀的运行态。
type Tool struct {
	id       string
	status   ToolStatus
	used     uint64
	reserved uint64
	warned   bool // 本刀（自上次换新以来）是否已发过预警
}

// Group 是一个刀组：配置 + 固定顺序的刀具。
type Group struct {
	id    string
	cfg   GroupConfig
	order []*Tool
	byID  map[string]*Tool
}

// Magazine 是存储层：刀组与刀具的注册、查找。
type Magazine struct {
	mu     sync.RWMutex
	groups map[string]*Group
}

// NewMagazine 创建空刀库。
func NewMagazine() *Magazine {
	return &Magazine{groups: map[string]*Group{}}
}

// AddGroup 注册刀组。
func (m *Magazine) AddGroup(id string, cfg GroupConfig) error {
	if id == "" {
		return errf(ErrInvalid, "group id is empty")
	}
	if !validConfig(cfg) {
		return errf(ErrInvalid, "invalid config for group %q", id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.groups[id]; ok {
		return errf(ErrInvalid, "group %q already exists", id)
	}
	m.groups[id] = &Group{
		id:   id,
		cfg:  cfg,
		byID: map[string]*Tool{},
	}
	return nil
}

// AddTool 向刀组尾部追加一把可用新刀。
func (m *Magazine) AddTool(groupID, toolID string) error {
	if toolID == "" {
		return errf(ErrInvalid, "tool id is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[groupID]
	if !ok {
		return errf(ErrGroupNotFound, "group %q not found", groupID)
	}
	if _, dup := g.byID[toolID]; dup {
		return errf(ErrInvalid, "tool %q already exists in group %q", toolID, groupID)
	}
	t := &Tool{id: toolID, status: StatusAvailable}
	g.byID[toolID] = t
	g.order = append(g.order, t)
	return nil
}

func validConfig(cfg GroupConfig) bool {
	if cfg.Basis != BasisSeconds && cfg.Basis != BasisPieces {
		return false
	}
	if cfg.Mode != ModeStrict && cfg.Mode != ModeLenient {
		return false
	}
	return cfg.WarnPermille <= 1000
}

// lookupGroup 在不加锁的情况下取刀组；调用方须持有 m.mu。
func (m *Magazine) lookupGroup(groupID string) (*Group, error) {
	g, ok := m.groups[groupID]
	if !ok {
		return nil, errf(ErrGroupNotFound, "group %q not found", groupID)
	}
	return g, nil
}

func (g *Group) tool(toolID string) (*Tool, error) {
	t, ok := g.byID[toolID]
	if !ok {
		return nil, errf(ErrToolNotFound, "tool %q not found in group %q", toolID, g.id)
	}
	return t, nil
}
