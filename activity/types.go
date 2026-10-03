package activity

import (
	"ontology/deadline"
	"ontology/retry"
)

type State int

const (
	Waiting State = iota
	Scheduled
	Running
	Terminal
)

type Reason string

const (
	ReasonSC        Reason = "sc"
	ReasonS2S       Reason = "s2s"
	ReasonS2C       Reason = "s2c"
	ReasonHB        Reason = "hb"
	ReasonApp       Reason = "app"
	ReasonCompleted Reason = "complete"
)

// Status 是 Status 操作的只读投影。
type Status struct {
	State    State
	Attempt  int
	Terminal bool
	Reason   Reason
	Time     int64
}

type act struct {
	id       []byte
	cfg      deadline.Config
	pol      retry.Policy
	t0       int64
	g        int64
	r        int64
	h        int64
	progress int64
	k        int
	state    State
	reason   Reason
	termAt   int64
	heap     *deadline.Heap
}

func (a *act) clone() *act {
	cp := *a
	cp.id = append([]byte(nil), a.id...)
	if a.heap == nil {
		cp.heap = nil
	} else {
		cp.heap = deadline.NewHeap(a.heap.Items()...)
	}
	return &cp
}
