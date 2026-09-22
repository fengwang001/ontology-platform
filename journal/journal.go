package journal

import "fmt"
import "sync"

type Direction int

const (
	Forward Direction = iota
	Compensation
)

func (d Direction) String() string {
	if d == Forward {
		return "fwd"
	}
	return "comp"
}

type Result int

const (
	RSuccess Result = iota
	RFail
	RUnknown
)

type Record struct {
	Kind     int
	Seq      int
	Index    int
	Dir      Direction
	Res      Result
	Err      string
	At       int64
	IdemKey  string
	Terminal int
}

const (
	KindStep = iota
	KindEnd
)

type Journal struct {
	mu         sync.Mutex
	instances  map[string][]Record
	maxEntries int
}

var ErrJournalFull = fmt.Errorf("journal: max entries exceeded")

func New(maxEntries int) *Journal {
	return &Journal{instances: map[string][]Record{}, maxEntries: maxEntries}
}

// Append appends one record for instance. It fails before mutating anything
// when the configured entry limit is reached, so a rejected append never
// leaves a half-written record.
func (j *Journal) Append(instance string, r Record) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	recs := j.instances[instance]
	if len(recs) >= j.maxEntries {
		return fmt.Errorf("%w: instance %q has %d/%d records",
			ErrJournalFull, instance, len(recs), j.maxEntries)
	}
	r.Seq = len(recs)
	recs = append(recs, r)
	j.instances[instance] = recs
	return nil
}

// Read returns a defensive copy of all records of instance in append order.
func (j *Journal) Read(instance string) []Record {
	j.mu.Lock()
	defer j.mu.Unlock()
	src := j.instances[instance]
	out := make([]Record, len(src))
	copy(out, src)
	return out
}

func (j *Journal) Len(instance string) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.instances[instance])
}

// Last returns the most recently appended record. It is an O(1) tail read and
// lets Resume detect a terminal instance without scanning the full log.
func (j *Journal) Last(instance string) (Record, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	recs := j.instances[instance]
	if len(recs) == 0 {
		return Record{}, false
	}
	return recs[len(recs)-1], true
}

func (j *Journal) Instances() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	names := make([]string, 0, len(j.instances))
	for name := range j.instances {
		names = append(names, name)
	}
	return names
}

func (j *Journal) Has(instance string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	_, ok := j.instances[instance]
	return ok
}
