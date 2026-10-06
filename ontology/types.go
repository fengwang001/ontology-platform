package ontology

type Rename struct {
	OldPath string
	NewPath string
}

type CommitInput struct {
	ID        string
	ParentIDs []string
	Files     map[string]string
	Renames   []Rename
}

type Attribution struct {
	CommitID string
	Path     string
	Line     int
	Ignored  bool
}

type commit struct {
	id      string
	parents []*commit
	files   map[string]string
	renames map[string]string
}
