package ontology

import (
	"sort"
	"sync"
)

// PropertyPresence is the stable, observable tri-state of a property in a
// returned instance view.
type PropertyPresence string

const (
	// PresentReadable: the property is present with a value (raw or masked).
	PresentReadable PropertyPresence = "present"
	// AbsentUnreadable: the property is declared but the subject may not read
	// it. The key is omitted from the view — never rendered as zero/null.
	AbsentUnreadable PropertyPresence = "absent_unreadable"
	// AbsentNotStored: the property is declared but the instance has no value.
	AbsentNotStored PropertyPresence = "absent_not_stored"
	// AbsentNotDeclared: the property name is not part of the object type.
	AbsentNotDeclared PropertyPresence = "absent_not_declared"
)

// FieldView is one property in an adjudicated view.
type FieldView struct {
	Presence PropertyPresence
	Value    Value
	Masked   bool
}

// InstanceView is the final result of a read: present readable properties
// plus a per-property status table that makes the three absence reasons
// distinguishable even for callers that only inspect the status map.
type InstanceView struct {
	Type   string
	ID     string
	Fields map[string]FieldView
	Status map[string]PropertyPresence
	Basis  PolicyBasis
}

// StatusFor reports the externally observable status of any property name,
// including names the object type does not declare. It is the stable way to
// distinguish "declared + present", "declared + unreadable", "declared + not
// stored" and "not declared at all".
func (v *InstanceView) StatusFor(name string) PropertyPresence {
	if status, ok := v.Status[name]; ok {
		return status
	}
	return AbsentNotDeclared
}

// Config fixes the merge modes and default verdict of one adjudicator.
type Config struct {
	RowMode      MergeMode
	PropertyMode MergeMode
	DefaultRow   Effect
	DefaultRead  Effect
	DefaultWrite Effect
	WriteMode    WriteMode
}

// Adjudicator binds an object-type registry, policy catalog, instance store,
// audit log and immutable merge configuration behind one concurrency-safe
// decision point.
type Adjudicator struct {
	mu      sync.RWMutex
	types   map[string]*ObjectType
	catalog *PolicyCatalog
	store   *Store
	audit   *AuditLogger
	cfg     Config
}

// NewAdjudicator constructs an adjudicator. Defaults are deny-by-default with
// deny-overrides on both axes when fields are left zero.
func NewAdjudicator(cfg Config, catalog *PolicyCatalog, store *Store, audit *AuditLogger) *Adjudicator {
	if cfg.RowMode == "" {
		cfg.RowMode = DenyOverrides
	}
	if cfg.PropertyMode == "" {
		cfg.PropertyMode = DenyOverrides
	}
	if cfg.WriteMode == "" {
		cfg.WriteMode = WriteReject
	}
	return &Adjudicator{
		types:   map[string]*ObjectType{},
		catalog: catalog,
		store:   store,
		audit:   audit,
		cfg:     cfg,
	}
}

// RegisterType installs an object type definition.
func (a *Adjudicator) RegisterType(t *ObjectType) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t.propertyByName = nil
	t.LookupProperty("")
	delete(t.propertyByName, "")
	a.types[t.Name] = t
}

// Read adjudicates one read request against the current state.
func (a *Adjudicator) Read(subject, typeName, id string) (*InstanceView, *DecisionError) {
	return a.read(subject, typeName, id, true)
}

func (a *Adjudicator) read(subject, typeName, id string, useIndex bool) (*InstanceView, *DecisionError) {
	a.mu.RLock()
	ot, known := a.types[typeName]
	cfg := a.cfg
	store := a.store
	catalog := a.catalog
	a.mu.RUnlock()
	if !known {
		err := newError(ErrRowInvisible, typeName, id, "", "object type not found")
		a.record("read", subject, typeName, id, nil, nil, err, PolicyBasis{})
		return nil, err
	}

	rowCand, propCand := a.candidates(catalog, typeName, subject, useIndex)

	raw, exists := store.GetRaw(typeName, id)

	typeMap := map[string]DeclaredType{}
	for _, p := range ot.Properties {
		typeMap[p.Name] = p.Type
	}
	rv := decideRow(rowCand, cfg.RowMode, cfg.DefaultRow, raw, typeMap)
	basis := PolicyBasis{
		MatchedRow:      rowPolicyIDs(rv.matched),
		MatchedProperty: map[string][]string{},
		RowMode:         cfg.RowMode,
		PropertyMode:    cfg.PropertyMode,
		RowVerdict:      rv.effect,
		Touched:         len(rowCand) + len(propCand),
	}

	// Missing instance and policy-denied instance return the SAME error, so
	// existence cannot be inferred. Raw values are never used in the message.
	if !exists || !rv.visible {
		err := newError(ErrRowInvisible, typeName, id, "", "instance is not visible")
		a.record("read", subject, typeName, id, nil, nil, err, basis)
		return nil, err
	}

	view := &InstanceView{
		Type:   typeName,
		ID:     id,
		Fields: map[string]FieldView{},
		Status: map[string]PropertyPresence{},
		Basis:  basis,
	}
	for _, def := range ot.Properties {
		name := def.Name
		pv := decideProperty(propCand, name, cfg.PropertyMode, cfg.DefaultRead, cfg.DefaultWrite)
		if len(pv.basis) > 0 {
			basis.MatchedProperty[name] = pv.basis
		}
		if !pv.readable {
			// Declared but unreadable: key is omitted entirely. Whether the
			// raw value happens to be the zero value is irrelevant and the
			// raw/masked value never appears anywhere in the response.
			view.Status[name] = AbsentUnreadable
			continue
		}
		value, stored := raw.Values[name]
		if !stored || !raw.Present[name] {
			view.Status[name] = AbsentNotStored
			continue
		}
		masked := false
		if pv.mask != nil {
			value = pv.mask(value)
			masked = true
			if !value.Conforms(def.Type) {
				err := newError(ErrMaskTypeViolation, typeName, id, name,
					"mask output does not satisfy the declared type")
				a.record("read", subject, typeName, id, nil, nil, err, basis)
				return nil, err
			}
		}
		view.Status[name] = PresentReadable
		view.Fields[name] = FieldView{Presence: PresentReadable, Value: value, Masked: masked}
	}
	a.record("read", subject, typeName, id, nil, view, nil, basis)
	return view, nil
}

// WriteResult reports what a write did.
type WriteResult struct {
	Applied map[string]bool
	Dropped []string
	Version int64
	Basis   PolicyBasis
}

// Write adjudicates one write. Input order never affects the verdict.
func (a *Adjudicator) Write(subject, typeName, id string, values map[string]Value) (*WriteResult, *DecisionError) {
	return a.write(subject, typeName, id, values, true)
}

func (a *Adjudicator) write(subject, typeName, id string, values map[string]Value, useIndex bool) (*WriteResult, *DecisionError) {
	input := sortedInput(values)
	a.mu.RLock()
	ot, known := a.types[typeName]
	cfg := a.cfg
	store := a.store
	catalog := a.catalog
	a.mu.RUnlock()
	if !known {
		err := newError(ErrRowInvisible, typeName, id, "", "object type not found")
		a.record("write", subject, typeName, id, input, nil, err, PolicyBasis{})
		return nil, err
	}

	// Request-shape checks first: unknown property and raw value conformance
	// are independent of policy and are reported before any verdict, so they
	// can never mask (or be masked by) a policy decision.
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		def, ok := ot.LookupProperty(name)
		if !ok {
			err := newError(ErrUnknownProperty, typeName, id, name, "property is not declared")
			a.record("write", subject, typeName, id, input, nil, err, PolicyBasis{})
			return nil, err
		}
		if !values[name].Conforms(def.Type) {
			err := newError(ErrValueTypeViolation, typeName, id, name,
				"submitted value violates the declared type")
			a.record("write", subject, typeName, id, input, nil, err, PolicyBasis{})
			return nil, err
		}
	}

	rowCand, propCand := a.candidates(catalog, typeName, subject, useIndex)

	raw, exists := store.GetRaw(typeName, id)
	typeMap := map[string]DeclaredType{}
	for _, p := range ot.Properties {
		typeMap[p.Name] = p.Type
	}
	rv := decideRow(rowCand, cfg.RowMode, cfg.DefaultRow, raw, typeMap)
	basis := PolicyBasis{
		MatchedRow:      rowPolicyIDs(rv.matched),
		MatchedProperty: map[string][]string{},
		RowMode:         cfg.RowMode,
		PropertyMode:    cfg.PropertyMode,
		RowVerdict:      rv.effect,
		Touched:         len(rowCand) + len(propCand),
	}

	// Precedence 1: row invisibility (hides existence as well).
	if !exists || !rv.visible {
		err := newError(ErrRowInvisible, typeName, id, "", "instance is not visible")
		a.record("write", subject, typeName, id, input, nil, err, basis)
		return nil, err
	}

	perProperty := map[string]propertyVerdict{}
	var notWritable []string
	for _, name := range names {
		pv := decideProperty(propCand, name, cfg.PropertyMode, cfg.DefaultRead, cfg.DefaultWrite)
		perProperty[name] = pv
		if len(pv.basis) > 0 {
			basis.MatchedProperty[name] = pv.basis
		}
		if !pv.writable {
			notWritable = append(notWritable, name)
		}
	}

	// Precedence 2: any non-writable field beats a mask conflict in reject
	// mode. Drop mode simply excludes those fields from the accepted set.
	if len(notWritable) > 0 && cfg.WriteMode == WriteReject {
		err := newError(ErrPropertyNotWritable, typeName, id, notWritable[0],
			"property is not writable for this subject")
		a.record("write", subject, typeName, id, input, nil, err, basis)
		return nil, err
	}

	accepted := map[string]Value{}
	applied := map[string]bool{}
	var dropped []string
	for _, name := range names {
		pv := perProperty[name]
		if !pv.writable {
			dropped = append(dropped, name)
			continue
		}
		value := values[name]
		if pv.mask != nil {
			value = pv.mask(value)
			def, _ := ot.LookupProperty(name)
			if !value.Conforms(def.Type) {
				// Precedence 3: masked write value breaks the type contract.
				err := newError(ErrMaskTypeViolation, typeName, id, name,
					"mask output does not satisfy the declared type")
				a.record("write", subject, typeName, id, input, nil, err, basis)
				return nil, err
			}
		}
		accepted[name] = value
		applied[name] = true
	}

	// Only fully accepted fields reach the store; rejected/dropped fields
	// cannot change values, version or last-write time.
	version, _ := store.applyWrite(typeName, id, accepted)
	result := &WriteResult{
		Applied: applied,
		Dropped: dropped,
		Version: version,
		Basis:   basis,
	}
	a.record("write", subject, typeName, id, input, result, nil, basis)
	return result, nil
}

func (a *Adjudicator) candidates(catalog *PolicyCatalog, typeName, subject string, useIndex bool) ([]RowPolicy, []PropertyPolicy) {
	if useIndex {
		return catalog.RowCandidates(typeName, subject), catalog.PropertyCandidates(typeName, subject)
	}
	var rowCand []RowPolicy
	var propCand []PropertyPolicy
	for _, p := range catalog.scanRowPolicies(typeName) {
		if p.selects(subject) {
			rowCand = append(rowCand, p)
		}
	}
	for _, p := range catalog.scanPropertyPolicies(typeName) {
		if p.selects(subject) {
			propCand = append(propCand, p)
		}
	}
	return rowCand, propCand
}

func (a *Adjudicator) record(op, subject, typeName, id string, input any, output any, err *DecisionError, basis PolicyBasis) {
	if a.audit == nil {
		return
	}
	entry := AuditEntry{
		Op:      op,
		Subject: subject,
		Type:    typeName,
		ID:      id,
		Input:   input,
		Output:  output,
		Basis:   &basis,
	}
	if err != nil {
		entry.Error = err.Error()
	}
	a.audit.log(entry)
}

func rowPolicyIDs(policies []RowPolicy) []string {
	ids := make([]string, 0, len(policies))
	for _, p := range policies {
		ids = append(ids, p.ID)
	}
	return ids
}

// writeInput is the order-independent JSON representation of a write request.
type writeInput struct {
	Properties []string         `json:"properties"`
	Values     map[string]Value `json:"values"`
}

func sortedInput(values map[string]Value) writeInput {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return writeInput{Properties: names, Values: values}
}
