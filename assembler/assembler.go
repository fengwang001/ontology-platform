package assembler

import (
	"errors"
	"sync"
)

type Op uint8

const (
	PAD Op = iota + 1
	JMP
	JZ
)

type Form uint8

const (
	NoForm Form = iota
	ShortForm
	LongForm
)

type Item struct {
	Op    Op
	Bytes int
	Label string
}

type Instruction struct {
	Index  int
	Op     Op
	Label  string
	Start  int
	Length int
	Form   Form
	Offset int
}

type Layout struct {
	Instructions []Instruction
	Labels       map[string]int
	TotalLength  int
}

type Snapshot struct {
	Items  []Item
	Labels map[string]int
}

var (
	ErrInvalidPadSize = errors.New("invalid PAD size")
	ErrEmptyLabel     = errors.New("invalid label: name is empty")
	ErrLabelDefined   = errors.New("label is already defined")
	ErrUndefinedLabel = errors.New("undefined jump label")
)

type Assembler struct {
	mu     sync.RWMutex
	items  []Item
	labels map[string]int
}

func New() *Assembler {
	return &Assembler{labels: make(map[string]int)}
}

func (a *Assembler) AddPad(n int) error {
	if n < 1 || n > 1000 {
		return ErrInvalidPadSize
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.items = append(a.items, Item{Op: PAD, Bytes: n})
	return nil
}

func (a *Assembler) AddJMP(label string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.items = append(a.items, Item{Op: JMP, Label: label})
	return nil
}

func (a *Assembler) AddJZ(label string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.items = append(a.items, Item{Op: JZ, Label: label})
	return nil
}

func (a *Assembler) DefineLabel(name string) error {
	if name == "" {
		return ErrEmptyLabel
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.labels[name]; ok {
		return ErrLabelDefined
	}
	a.labels[name] = len(a.items)
	return nil
}

func (a *Assembler) Snapshot() Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneSnapshot(a.items, a.labels)
}

func (a *Assembler) Assemble() (*Layout, error) {
	a.mu.RLock()
	snapshot := Snapshot{
		Items:  cloneItems(a.items),
		Labels: cloneLabels(a.labels),
	}
	a.mu.RUnlock()
	return Assemble(snapshot)
}

func cloneSnapshot(items []Item, labels map[string]int) Snapshot {
	return Snapshot{
		Items:  cloneItems(items),
		Labels: cloneLabels(labels),
	}
}

func cloneItems(items []Item) []Item {
	if len(items) == 0 {
		return []Item{}
	}
	cloned := make([]Item, len(items))
	copy(cloned, items)
	return cloned
}

func cloneLabels(labels map[string]int) map[string]int {
	cloned := make(map[string]int, len(labels))
	for name, index := range labels {
		cloned[name] = index
	}
	return cloned
}
