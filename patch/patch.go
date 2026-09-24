package patch

import "errors"

import "ontology/udiff"

var (
	ErrContext = errors.New("patch: context mismatch")
	ErrOffset  = errors.New("patch: no match within fuzz")
)

type ApplyError struct {
	Hunk  int
	Kind  error
	Reason string
}

func (e *ApplyError) Error() string { return "" }

func Apply(src []byte, p *udiff.Patch, fuzz int, reverse bool) ([]byte, error) { return nil, nil }

func Make(a, b []byte, maxD, ctx int) (*udiff.Patch, error) { return nil, nil }

type Commit struct {
	Seq  int
	Text []byte
}

type Store struct{ docs map[string]*doc }

type doc struct {
	version int
	text    []byte
	log     []Commit
}

func NewStore() *Store { return nil }

func (s *Store) ApplyDoc(name string, p *udiff.Patch, fuzz int) (int, error) { return 0, nil }

func (s *Store) Snapshot(name string) (int, []byte, []Commit) { return 0, nil, nil }
