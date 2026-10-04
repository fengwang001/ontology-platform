package profile

// 参数边界（题目给定）。
const (
	MaxTime     int64 = 1_000_000_000_000
	MaxStep     int64 = 10_000_000
	MaxDB       int64 = 1_000_000_000
	MinMaxI     int64 = 1_000
	MaxValue    int64 = 1_000_000_000_000_000
	MaxInterval int64 = 1_000_000_000
)

// Params 是单个测点的静态参数，由 SetProfile 建立或整体替换。
type Params struct {
	DB          int64
	MinInterval int64
	MaxInterval int64
	Low, High   int64
}

// Catalog 保存全部测点参数，支持按名字热更新替换。
type Catalog struct {
	points map[string]Params
}

func NewCatalog() *Catalog { return &Catalog{points: map[string]Params{}} }

// Set 建立测点或整体替换其参数；已登记测点的运行状态不在本包保存。
func (c *Catalog) Set(name string, p Params) { c.points[name] = p }

func (c *Catalog) Get(name string) (Params, bool) {
	p, ok := c.points[name]
	return p, ok
}

// ValidParams 校验 db∈[0,1e9]、1≤minI<maxI、1000≤maxI≤1e9、lo≤hi，
// 以及量程端点绝对值不超过 1e15。
func ValidParams(p Params) bool {
	if p.DB < 0 || p.DB > MaxDB {
		return false
	}
	if p.MinInterval < 1 || p.MinInterval >= p.MaxInterval {
		return false
	}
	if p.MaxInterval < MinMaxI || p.MaxInterval > MaxInterval {
		return false
	}
	if p.Low > p.High || p.Low < -MaxValue || p.High > MaxValue {
		return false
	}
	return true
}

// Valid 判断样本值是否落在当前量程内。
func (p Params) Valid(v int64) bool { return v >= p.Low && v <= p.High }
