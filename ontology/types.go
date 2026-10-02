package ontology

// NumLevels is the number of file levels (0..6).
const NumLevels = 7

// File is a single table file in some version.
type File struct {
	Level    int
	Num      uint64
	Smallest []byte
	Largest  []byte
}

// Del identifies a deleted file by level and number.
type Del struct {
	Level int
	Num   uint64
}

// Edit is a version edit.
type Edit struct {
	Adds      []File
	Dels      []Del
	LogNumber *uint64
	NextFile  *uint64
	LastSeq   *uint64
}

// Version is a complete database version.
type Version struct {
	Files     [NumLevels][]File
	LogNumber uint64
	NextFile  uint64
	LastSeq   uint64
}

// Record is one appended manifest record.
type Record struct {
	IsSnapshot bool
	Snapshot   *Version
	Edit       *Edit
	Torn       bool
}

// Disk is an in-memory simulation of durable storage.
type Disk struct {
	Manifests map[uint64][]Record
	Current   uint64
}
