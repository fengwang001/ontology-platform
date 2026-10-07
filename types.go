package ontology

// Direction models how a link instance may be traversed.
type Direction int

const (
	// Directed allows traversal only from source to target.
	Directed Direction = iota
	// Bidirectional allows traversal in both directions.
	Bidirectional
)

// ObjectType defines a category of objects.
type ObjectType struct {
	ID   string
	Name string
}

// LinkType defines a category of links.
type LinkType struct {
	ID            string
	Name          string
	Direction     Direction
	AllowSelfLoop bool
	AllowParallel bool
	SourceTypeID  string
	TargetTypeID  string
	endpointTyped bool
}
