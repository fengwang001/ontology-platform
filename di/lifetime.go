package di

// Lifetime 描述服务实例的生命周期。
type Lifetime int

const (
	// Singleton：每个容器至多成功构造一次。
	Singleton Lifetime = iota
	// Scoped：每个作用域至多成功构造一次。
	Scoped
	// Transient：每次解析新建。
	Transient
)

func (l Lifetime) String() string {
	switch l {
	case Singleton:
		return "singleton"
	case Scoped:
		return "scoped"
	case Transient:
		return "transient"
	default:
		return "unknown"
	}
}

// Constructor 根据依赖实例构造服务实例。
type Constructor func(deps map[string]any) (any, error)

// Disposer 由需要显式释放资源的实例实现；释放顺序由容器保证。
type Disposer interface {
	Dispose()
}

// Registration 描述一条服务注册项。
type Registration struct {
	Name         string
	Dependencies []string
	Lifetime     Lifetime
	Construct    Constructor
}
