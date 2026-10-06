package endpointshard

// Endpoint 是一个服务端点。标识在服务内唯一。
type Endpoint struct {
	ID          string
	Region      string
	Healthy     bool
	Terminating bool
}

// Ready 派生条件：健康且非终止中。
func (e Endpoint) Ready() bool { return e.Healthy && !e.Terminating }

// Serveable 派生条件：健康。
func (e Endpoint) Serveable() bool { return e.Healthy }

// IsTerminating 派生条件：终止中位。
func (e Endpoint) IsTerminating() bool { return e.Terminating }
