package growstack

// Config configures a Runtime.
type Config struct {
	BaseSize    int
	GrowthMul   int
	MaxPerStack int
	TotalQuota  int
	FrameSlots  int
	ShrinkRatio float64
}

// NewRuntime builds a runtime; it returns a configuration error when the
// configuration cannot guarantee deterministic, non-thrashing behaviour.
//
// Validation rules (any failure rejects the whole instance as ErrConfig):
//   - all integer fields must be positive; ShrinkRatio in (0,1).
//   - MaxPerStack must be a legal reachable size: BaseSize*GrowthMul^k.
//   - the shrink trigger must be strictly below the lower bound of the
//     post-growth utilization, i.e. ShrinkRatio < 1/GrowthMul.
//
// The last rule guarantees no thrash: right after a growth the utilization
// is strictly greater than 1/GrowthMul (the push that forced it would fit
// at size cur/GrowthMul, so used-after-growth > newSize/GrowthMul), while a
// shrink can only occur at strictly less than ShrinkRatio of the size.
func NewRuntime(cfg Config, opts ...Option) (*Runtime, error) {
	if cfg.BaseSize <= 0 || cfg.GrowthMul < 2 || cfg.MaxPerStack <= 0 ||
		cfg.TotalQuota <= 0 || cfg.FrameSlots <= 0 {
		return nil, &Error{Kind: ErrConfig, Msg: "integer fields must be positive and GrowthMul >= 2"}
	}
	if cfg.ShrinkRatio <= 0 || cfg.ShrinkRatio >= 1 {
		return nil, &Error{Kind: ErrConfig, Msg: "ShrinkRatio must be in (0,1)"}
	}
	if cfg.MaxPerStack%cfg.BaseSize != 0 || !isGrowthReachable(cfg.BaseSize, cfg.GrowthMul, cfg.MaxPerStack) {
		return nil, &Error{Kind: ErrConfig, Msg: "MaxPerStack must be BaseSize*GrowthMul^k"}
	}
	if !(cfg.ShrinkRatio < 1.0/float64(cfg.GrowthMul)) {
		return nil, &Error{Kind: ErrConfig, Msg: "ShrinkRatio must be strictly below 1/GrowthMul to prevent thrash"}
	}

	r := &Runtime{
		cfg:       cfg,
		log:       discardLogger(),
		alloc:     newDefaultAllocator(),
		remaining: cfg.TotalQuota,
		stacks:    map[int64]*coroutine{},
		globals:   map[string]Value{},
		nextCO:    1,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

func isGrowthReachable(base, mul, max int) bool {
	for size := base; ; size *= mul {
		if size == max {
			return true
		}
		if size > max || size > max/mul { // overflow guard
			return false
		}
	}
}

// Option customizes a Runtime (allocator hooks, log sinks).
type Option func(*Runtime)

// WithAllocator installs a backing-memory allocator (test failure injection).
func WithAllocator(a Allocator) Option { return func(r *Runtime) { r.alloc = a } }

// WithLogger installs an audit logger.
func WithLogger(l Logger) Option { return func(r *Runtime) { r.log = l } }
