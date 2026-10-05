// Package binding stores the forwarding-equivalence-class to label bindings
// and their owners: per-fec owner sets (normal, stale, or placeholder),
// per-client ownership indexes, and the client liveness state machine.
// It performs no validation and is not safe for concurrent use; the manager
// serializes access and checks parameters.
package binding

// ClientState is the liveness state of a client.
type ClientState int

const (
	// StateNormal is the default: the client is up and not recovering.
	StateNormal ClientState = iota
	// StateOffline follows ClientDown; the client may not operate.
	StateOffline
	// StateRecovering follows ClientUp; stale ownerships may be refreshed.
	StateRecovering
)

// PlaceholderID keys the virtual placeholder owner installed on every still
// bound fec after a manager restore. Real clients are always >= 1.
const PlaceholderID = 0

// Owner is one ownership relation between a client and a fec.
type Owner struct {
	Client      int
	Stale       bool   // marked by ClientDown, refreshed by Bind
	Deadline    uint64 // stale removal or placeholder expiry moment
	Placeholder bool   // virtual owner installed by restore
}

// Binding is one fec-to-label binding with its owner set.
type Binding struct {
	Label  int
	Owners map[int]*Owner // keyed by client ID; placeholder keyed by 0
}

// Table holds all bindings, the per-client indexes, and client states.
type Table struct {
	Q        int // per-client ownership limit
	byFec    map[string]*Binding
	byClient map[int]map[string]struct{}
	states   map[int]ClientState
}

// NewTable returns an empty binding table with ownership limit q.
func NewTable(q int) *Table {
	return &Table{
		Q:        q,
		byFec:    make(map[string]*Binding),
		byClient: make(map[int]map[string]struct{}),
		states:   make(map[int]ClientState),
	}
}

// State returns the client's liveness state (StateNormal if never seen).
func (t *Table) State(client int) ClientState {
	return t.states[client]
}

// SetState records the client's liveness state.
func (t *Table) SetState(client int, s ClientState) {
	t.states[client] = s
}

// Get returns the binding for fec, if any.
func (t *Table) Get(fec string) (*Binding, bool) {
	b, ok := t.byFec[fec]
	return b, ok
}

// Owner returns the ownership of client on fec, or nil.
func (t *Table) Owner(fec string, client int) *Owner {
	if b, ok := t.byFec[fec]; ok {
		return b.Owners[client]
	}
	return nil
}

// Count returns how many ownerships (normal and stale) client holds.
func (t *Table) Count(client int) int {
	return len(t.byClient[client])
}

// FecsOf returns the set of fecs client currently owns; nil when empty. The
// result must not be mutated.
func (t *Table) FecsOf(client int) map[string]struct{} {
	return t.byClient[client]
}

// Bindings returns a snapshot of the fec-to-label mapping.
func (t *Table) Bindings() map[string]int {
	out := make(map[string]int, len(t.byFec))
	for fec, b := range t.byFec {
		out[fec] = b.Label
	}
	return out
}

// AddBinding creates a new binding for fec with one normal owner.
func (t *Table) AddBinding(fec string, label, client int) {
	t.byFec[fec] = &Binding{
		Label:  label,
		Owners: map[int]*Owner{client: {Client: client}},
	}
	t.addClientFec(client, fec)
}

// SetBinding creates an ownerless binding; used while replaying a log.
func (t *Table) SetBinding(fec string, label int) {
	t.byFec[fec] = &Binding{Label: label, Owners: make(map[int]*Owner)}
}

// RemoveBinding drops the binding for fec and returns its label.
func (t *Table) RemoveBinding(fec string) int {
	b := t.byFec[fec]
	for client, o := range b.Owners {
		if !o.Placeholder {
			t.removeClientFec(client, fec)
		}
	}
	delete(t.byFec, fec)
	return b.Label
}

// AddOwner adds a normal ownership of client on the bound fec.
func (t *Table) AddOwner(fec string, client int) {
	t.byFec[fec].Owners[client] = &Owner{Client: client}
	t.addClientFec(client, fec)
}

// AddPlaceholder installs the virtual placeholder owner on the bound fec.
func (t *Table) AddPlaceholder(fec string, deadline uint64) {
	t.byFec[fec].Owners[PlaceholderID] = &Owner{
		Client:      PlaceholderID,
		Deadline:    deadline,
		Placeholder: true,
	}
}

// RemoveOwner drops the ownership of client on fec. When the last owner
// leaves, the binding is removed and its label returned with emptied=true.
func (t *Table) RemoveOwner(fec string, client int) (label int, emptied bool) {
	b, ok := t.byFec[fec]
	if !ok {
		return 0, false
	}
	o, ok := b.Owners[client]
	if !ok {
		return 0, false
	}
	delete(b.Owners, client)
	if !o.Placeholder {
		t.removeClientFec(client, fec)
	}
	if len(b.Owners) == 0 {
		delete(t.byFec, fec)
		return b.Label, true
	}
	return 0, false
}

// Refresh turns a stale ownership back to normal.
func (t *Table) Refresh(fec string, client int) {
	if o := t.Owner(fec, client); o != nil {
		o.Stale = false
		o.Deadline = 0
	}
}

// MarkStale marks every normal ownership of client stale with the given
// deadline; already stale ownerships keep their original deadline. It
// returns the fecs whose ownership changed.
func (t *Table) MarkStale(client int, deadline uint64) []string {
	var changed []string
	for fec := range t.byClient[client] {
		if o := t.byFec[fec].Owners[client]; o != nil && !o.Stale {
			o.Stale = true
			o.Deadline = deadline
			changed = append(changed, fec)
		}
	}
	return changed
}

// StaleFecs lists the fecs on which client's ownership is stale.
func (t *Table) StaleFecs(client int) []string {
	var out []string
	for fec := range t.byClient[client] {
		if o := t.byFec[fec].Owners[client]; o != nil && o.Stale {
			out = append(out, fec)
		}
	}
	return out
}

func (t *Table) addClientFec(client int, fec string) {
	m := t.byClient[client]
	if m == nil {
		m = make(map[string]struct{})
		t.byClient[client] = m
	}
	m[fec] = struct{}{}
}

func (t *Table) removeClientFec(client int, fec string) {
	m := t.byClient[client]
	if m == nil {
		return
	}
	delete(m, fec)
	if len(m) == 0 {
		delete(t.byClient, client)
	}
}
