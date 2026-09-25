package patch

// Store is an in-memory versioned multi-document store.
type Store struct{}

// NewStore creates an empty store seeded with name at version 0.
func NewStore(name string, content []byte) *Store { return &Store{} }

// Result records one Apply attempt for the commit log.
type Result struct {
	Doc     string
	Version int
	OK      bool
	Patch   []byte
}

// Commit applies p against the current consistent snapshot, atomically
// replacing content and bumping the version on success.
func (s *Store) Commit(name string, p []byte, o Options) (Result, error) {
	return Result{}, nil
}

// Get returns the current content and version of a document.
func (s *Store) Get(name string) ([]byte, int) { return nil, 0 }

// Log returns the ordered commit log.
func (s *Store) Log() []Result { return nil }

// Replay serially applies every successful log entry from the seed content.
func (s *Store) Replay(seed []byte, o Options) ([]byte, error) { return nil, nil }
