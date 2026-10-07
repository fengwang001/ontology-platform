package ontology

type Gateway struct {
	mu      chan struct{}
	types   map[string]*typeState
	objects map[string]*objectState
	logs    *decisionLogger
}

func NewGateway(options ...func(*Gateway)) *Gateway {
	gateway := &Gateway{
		mu:      make(chan struct{}, 1),
		types:   map[string]*typeState{},
		objects: map[string]*objectState{},
		logs:    newDecisionLogger(nil),
	}
	gateway.mu <- struct{}{}
	for _, option := range options {
		option(gateway)
	}
	return gateway
}

func WithDecisionLogHook(hook DecisionLogFunc) func(*Gateway) {
	return func(gateway *Gateway) { gateway.logs = newDecisionLogger(hook) }
}

func (g *Gateway) lock()   { <-g.mu }
func (g *Gateway) unlock() { g.mu <- struct{}{} }
