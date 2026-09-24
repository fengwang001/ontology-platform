// Package api is the public front door to the copy-on-write table.
// It depends only on snapshot; rejected operations change no state.
package api

import (
	"errors"

	"ontology/snapshot"
)

// Handle is a pinned, immutable snapshot handle.
type Handle = snapshot.Handle

// Distinct, decidable sentinel errors for every rejection path.
var (
	ErrEmptyKey    = errors.New("api: key must not be empty")
	ErrEmptyValue  = errors.New("api: value must not be empty")
	ErrKeyTooLong  = errors.New("api: key length exceeds maxKeyLen")
	ErrTooManyKeys = errors.New("api: distinct key count exceeds maxKeys")
)

// API validates inputs and delegates storage to snapshot.Store.
type API struct {
	maxKeyLen int
	maxKeys   int
	st        *snapshot.Store
}

// New builds an API with the given key-length and key-count limits.
func New(maxKeyLen, maxKeys int) *API {
	return &API{maxKeyLen: maxKeyLen, maxKeys: maxKeys, st: snapshot.NewStore()}
}

// Update validates before touching any state; on rejection the current
// version, its id, and the atomic pointer are all left exactly as they
// were, and a distinct sentinel error is returned.
func (a *API) Update(k, v string) error {
	switch {
	case k == "":
		return ErrEmptyKey
	case v == "":
		return ErrEmptyValue
	case len(k) > a.maxKeyLen:
		return ErrKeyTooLong
	}
	// Capacity is decided under the write lock against the live version,
	// before any clone or atomic store, so a refusal publishes nothing.
	if !a.st.UpdateChecked(k, v, a.maxKeys) {
		return ErrTooManyKeys
	}
	return nil
}

// Read returns the value and presence of k from one loaded version.
func (a *API) Read(k string) (string, bool) { return a.st.Read(k) }

// ReadKeys returns every requested key from one shared version.
func (a *API) ReadKeys(ks []string) map[string]string { return a.st.ReadKeys(ks) }

// Snapshot returns a handle pinned to the current immutable version.
func (a *API) Snapshot() *Handle { return a.st.Snapshot() }

// SelfCheck replays a built-in sequence covering all four invariants.
// It mutates the receiver; call it on a freshly constructed API.
func (a *API) SelfCheck() error {
	if err := a.st.SelfCheck(); err != nil {
		return err
	}
	b := New(8, 4)
	must := func(err, want error) error {
		if !errors.Is(err, want) {
			return err
		}
		return nil
	}
	_ = b.Update("a", "1")
	before := b.Snapshot().ID()
	if e := must(b.Update("", "x"), ErrEmptyKey); e != nil {
		return e
	}
	if e := must(b.Update("x", ""), ErrEmptyValue); e != nil {
		return e
	}
	if e := must(b.Update("toolongkey", "x"), ErrKeyTooLong); e != nil {
		return e
	}
	_ = b.Update("b", "2")
	_ = b.Update("c", "3")
	_ = b.Update("d", "4") // now 4 distinct keys == maxKeys
	if e := must(b.Update("e", "5"), ErrTooManyKeys); e != nil {
		return e
	}
	after := b.Snapshot()
	if after.ID() != before+3 || after.Len() != 4 {
		return errors.New("rejected update changed state")
	}
	if v, _ := b.Read("d"); v != "4" { // still usable after rejections
		return errors.New("table unusable after rejected updates")
	}
	return nil
}
