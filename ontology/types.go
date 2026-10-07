package ontology

type DeleteAction string

const (
	Cascade  DeleteAction = "cascade"
	SetNull  DeleteAction = "set_null"
	Restrict DeleteAction = "restrict"
)

type LinkTypeConfig struct {
	Name             string
	OnDelete         DeleteAction
	PreserveOnExists bool
}

type Link struct {
	Type   string
	Source string
	Target string
}

type Object struct {
	ID string
}

type DeleteResult struct {
	DeletedObjects []string
	RemovedLinks   []Link
	NullifiedLinks []Link
	Steps          []DeleteStep
}

type DeleteStep struct {
	Kind     string
	ObjectID string
	Reason   string
	Link     Link
	Rule     string
}

type LogEntry struct {
	RequestID      int64        `json:"request_id"`
	RootObjectID   string       `json:"root_object_id"`
	DeletedObjects []string     `json:"deleted_objects,omitempty"`
	RemovedLinks   []Link       `json:"removed_links,omitempty"`
	NullifiedLinks []Link       `json:"nullified_links,omitempty"`
	Steps          []DeleteStep `json:"steps,omitempty"`
	DedupProbes    int          `json:"dedup_probes,omitempty"`
	Error          string       `json:"error,omitempty"`
	Committed      bool         `json:"committed"`
}
