package ontology

// Query is a shortest-path request.
type Query struct {
	Subject SubjectID
	From    ObjectID
	To      ObjectID
}

// Path is one complete route through the graph.
type Path struct {
	ObjectIDs []ObjectID
	LinkTypes []LinkType
	Cost      float64
}

// Status classifies a query outcome.
type Status uint8

const (
	// StatusReachable means an allowed complete path exists.
	StatusReachable Status = iota
	// StatusUnreachable means no allowed complete path exists and no
	// ambiguity prevents a definite answer.
	StatusUnreachable
	// StatusAmbiguous means at least one relevant candidate link has no
	// unique overlay result, so reachability cannot be decided.
	StatusAmbiguous
	// StatusInvalidSubject means the subject identifier is illegal.
	StatusInvalidSubject
	// StatusMissingObject means an endpoint object does not exist.
	StatusMissingObject
)

func (s Status) String() string {
	switch s {
	case StatusReachable:
		return "reachable"
	case StatusUnreachable:
		return "unreachable"
	case StatusAmbiguous:
		return "ambiguous"
	case StatusInvalidSubject:
		return "invalid-subject"
	case StatusMissingObject:
		return "missing-object"
	default:
		return "unknown"
	}
}

// Counters is the internal cost metric for one query. It counts only work
// performed for candidate links actually examined; it never iterates all
// groups or the whole graph.
type Counters struct {
	// LinkEvaluations counts candidate links run through the overlay rules.
	LinkEvaluations int
	// EndpointTypeLookups counts object-type lookups at edge endpoints.
	EndpointTypeLookups int
	// NodesSettled counts vertices finalized by the shortest-path search.
	NodesSettled int
	// Relaxations counts examined adjacency entries.
	Relaxations int
}

// StepDecision records the overlay basis for one examined candidate link.
type StepDecision struct {
	From     ObjectID
	To       ObjectID
	LinkType LinkType
	LinkCost float64
	Verdict  VerdictResult
}

// QueryResult is the outcome of one shortest-path query.
type QueryResult struct {
	Status       Status
	StateVersion int64
	GraphVersion int64
	Path         *Path
	Counters     Counters
	Steps        []StepDecision
}

// Decide evaluates one candidate link at the engine's latest committed
// permission version.
func (e *QueryEngine) Decide(subject SubjectID, lt LinkType, fromType, toType ObjectType) VerdictResult {
	return newResolver(e.state.Snapshot()).resolve(subject, lt, fromType, toType)
}

// Pinned is a query bound to immutable point-in-time snapshots of both the
// permission state and the graph. It is used to assert the start-time
// boundary explicitly: mutations after Pin are invisible to subsequent Runs.
type Pinned struct {
	engine *QueryEngine
	perm   *snapshot
	graph  *graphSnapshot
}

// Pin captures the current committed versions.
func (e *QueryEngine) Pin() *Pinned {
	return &Pinned{engine: e, perm: e.state.Snapshot(), graph: e.graph.snapshot()}
}

// PermVersion reports the pinned permission version.
func (p *Pinned) PermVersion() int64 { return p.perm.version }

// GraphVersion reports the pinned graph version.
func (p *Pinned) GraphVersion() int64 { return p.graph.version }

// Run executes a query against the pinned snapshots.
func (p *Pinned) Run(q Query) QueryResult {
	return p.engine.shortestPathAt(q, p.perm, p.graph)
}

// QueryEngine answers path queries against a graph and a permission state.
type QueryEngine struct {
	state *PermissionState
	graph *Graph
}

// NewQueryEngine wires a graph to its permission state.
func NewQueryEngine(state *PermissionState, graph *Graph) *QueryEngine {
	return &QueryEngine{state: state, graph: graph}
}
