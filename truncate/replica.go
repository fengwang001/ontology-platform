package truncate

// Entry is a single log record carrying the generation of the leader that
// accepted it. Logs are addressed by zero-based offsets.
type Entry struct {
	Generation int
	Data       string
}

// genMark records that this replica transitioned into Generation at Start.
type genMark struct {
	Generation int
	Start      int
}

// Replica is one copy of the shared log.
type Replica struct {
}

func newReplica(initialGeneration int) *Replica { return &Replica{} }

// Cluster holds a named set of replicas and tracks the current leader.
type Cluster struct {
}

func NewCluster() *Cluster { return &Cluster{} }

var _ = Entry{}

