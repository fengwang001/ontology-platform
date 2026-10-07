package precheck

// HookError 是钩子主动给出的校验失败（区别于基础设施错误）。
type HookError struct {
	Code    string
	Message string
}

func (e *HookError) Error() string { return e.Code + ": " + e.Message }

// PreContext 是前置钩子看到的上下文。
type PreContext struct {
	at         Moment
	caller     string
	params     map[string]any
	scratch    map[string]any
	objectView func(key string) (any, bool)
	objectGap  func(key string) bool
}

func (c *PreContext) At() Moment                   { return c.at }
func (c *PreContext) Caller() string               { return c.caller }
func (c *PreContext) Param(key string) (any, bool) { v, ok := c.params[key]; return v, ok }
func (c *PreContext) Object(key string) (any, bool) {
	return c.objectView(key)
}
func (c *PreContext) HasObjectGap(key string) bool     { return c.objectGap(key) }
func (c *PreContext) SetScratch(key string, value any) { c.scratch[key] = value }
func (c *PreContext) Scratch(key string) (any, bool)   { v, ok := c.scratch[key]; return v, ok }

// PostContext 是后置钩子看到的只读上下文（前置快照不可变）。
type PostContext struct {
	pre *PreContext
}

func (c *PostContext) At() Moment                    { return c.pre.at }
func (c *PostContext) Caller() string                { return c.pre.caller }
func (c *PostContext) Param(key string) (any, bool)  { v, ok := c.pre.params[key]; return v, ok }
func (c *PostContext) Object(key string) (any, bool) { return c.pre.objectView(key) }
func (c *PostContext) HasObjectGap(key string) bool  { return c.pre.objectGap(key) }
func (c *PostContext) Scratch(key string) (any, bool) {
	v, ok := c.pre.scratch[key]
	return deepCopy(v), ok
}

// Hook 是一个不可变、无外部副作用的校验钩子实现。
type Hook interface {
	ID() string
	Pre(ctx *PreContext) error
	Post(ctx *PostContext) ([]Intent, error)
}

// Registry 保存所有历史钩子实现（ID 一经发布永不复用、永不修改）。
type Registry struct {
	hooks map[string]Hook
}

func NewRegistry() *Registry { return &Registry{hooks: map[string]Hook{}} }

func (r *Registry) Register(h Hook) { r.hooks[h.ID()] = h }

func (r *Registry) Get(id string) (Hook, bool) { h, ok := r.hooks[id]; return h, ok }
