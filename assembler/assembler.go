package assembler

import "sync"

type OpKind int

const (
	Pad OpKind = iota
	JMP
	JZ
)

func (kind OpKind) String() string {
	switch kind {
	case Pad:
		return "PAD"
	case JMP:
		return "JMP"
	case JZ:
		return "JZ"
	default:
		return "UNKNOWN"
	}
}

type Form int

const (
	NoForm Form = iota
	ShortForm
	LongForm
)

func (form Form) String() string {
	switch form {
	case NoForm:
		return "NONE"
	case ShortForm:
		return "SHORT"
	case LongForm:
		return "LONG"
	default:
		return "UNKNOWN"
	}
}

type Record struct {
	Kind     OpKind
	PadBytes int
	Label    string
}

type Entry struct {
	Index    int
	Kind     OpKind
	PadBytes int
	Label    string
	Start    int
	Size     int
	Form     Form
	Offset   int
	Target   int
}

type Result struct {
	Entries     []Entry
	Labels      map[string]int
	TotalLength int
	Rounds      int
}

type Snapshot struct {
	Operations []Record
	Labels     map[string]int
}

type Assembler struct {
	mu     sync.RWMutex
	ops    []Record
	labels map[string]int
}

func New() *Assembler {
	return &Assembler{labels: make(map[string]int)}
}

func (a *Assembler) AppendPad(n int) error {
	if n < 1 || n > 1000 {
		return InvalidPadSizeError{Size: n}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.ops = append(a.ops, Record{Kind: Pad, PadBytes: n})
	return nil
}

func (a *Assembler) AppendJump(kind OpKind, label string) error {
	if kind != JMP && kind != JZ {
		return ErrInvalidJumpKind
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.ops = append(a.ops, Record{Kind: kind, Label: label})
	return nil
}

func (a *Assembler) DefineLabel(name string) error {
	if name == "" {
		return EmptyLabelNameError{}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.labels[name]; exists {
		return LabelAlreadyDefinedError{Name: name}
	}
	a.labels[name] = len(a.ops)
	return nil
}

func (a *Assembler) Snapshot() Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()

	operations := append([]Record(nil), a.ops...)
	labels := make(map[string]int, len(a.labels))
	for name, index := range a.labels {
		labels[name] = index
	}
	return Snapshot{Operations: operations, Labels: labels}
}

func (a *Assembler) Assemble() (*Result, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return assemble(a.ops, a.labels)
}

func (a *Assembler) Len() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.ops)
}
