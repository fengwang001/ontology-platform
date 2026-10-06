package incremental

import "sort"

type entry struct {
	decl      Declaration
	signature SignatureResult
}

type Registry struct {
	entries map[DeclID]*entry
}

func NewRegistry() *Registry { return &Registry{entries: map[DeclID]*entry{}} }

type ResultCache struct {
	signatures      map[DeclID]SignatureResult
	implementations map[DeclID]ImplementationResult
}

func NewResultCache() *ResultCache {
	return &ResultCache{
		signatures:      map[DeclID]SignatureResult{},
		implementations: map[DeclID]ImplementationResult{},
	}
}

func ensureEntries(entries map[DeclID]*entry) map[DeclID]*entry {
	if entries == nil {
		return map[DeclID]*entry{}
	}
	return entries
}

func (c *ResultCache) signature(id DeclID) (SignatureResult, bool) {
	result, ok := c.signatures[id]
	return cloneSignatureResult(result), ok
}

func (c *ResultCache) putSignature(id DeclID, result SignatureResult) {
	c.signatures[id] = cloneSignatureResult(result)
}

func (c *ResultCache) invalidateSignature(id DeclID) {
	delete(c.signatures, id)
}

func (c *ResultCache) implementation(id DeclID) (ImplementationResult, bool) {
	result, ok := c.implementations[id]
	return cloneImplementationResult(result), ok
}

func (c *ResultCache) putImplementation(id DeclID, result ImplementationResult) {
	c.implementations[id] = cloneImplementationResult(result)
}

func (c *ResultCache) invalidateImplementation(id DeclID) {
	delete(c.implementations, id)
}

func cloneImplementationResult(result ImplementationResult) ImplementationResult {
	result.Basis = cloneBasis(result.Basis)
	return result
}

func (r *Registry) put(id DeclID, decl Declaration) {
	r.entries = ensureEntries(r.entries)
	r.entries[id] = &entry{
		decl:      decl,
		signature: SignatureResult{Present: decl.Present},
	}
}

func (r *Registry) declaration(id DeclID) (Declaration, bool) {
	e, ok := r.entries[id]
	if !ok {
		return Declaration{ID: id}, false
	}
	return e.decl, true
}

func (r *Registry) setDeclaration(id DeclID, decl Declaration) {
	if _, ok := r.entries[id]; !ok {
		r.put(id, decl)
		return
	}
	r.entries[id].decl = decl
}

func (r *Registry) deleteDeclaration(id DeclID) {
	if e, ok := r.entries[id]; ok {
		e.decl = Declaration{ID: id}
	}
}

func (r *Registry) get(id DeclID) (*entry, bool) {
	e, ok := r.entries[id]
	return e, ok
}

func (r *Registry) snapshotSignature(id DeclID) (SignatureResult, bool) {
	e, ok := r.entries[id]
	if !ok {
		return SignatureResult{}, false
	}
	return e.signature, true
}

func (r *Registry) commitSignature(id DeclID, result SignatureResult) bool {
	e, ok := r.entries[id]
	if !ok {
		return false
	}
	changed := !result.Present || !e.signature.Present || !result.Signature.Equal(e.signature.Signature)
	if changed {
		result.Version = e.signature.Version + 1
	} else {
		result.Version = e.signature.Version
	}
	e.signature = cloneSignatureResult(result)
	return changed
}

func (r *Registry) commitSignatureForce(id DeclID, result SignatureResult, changed bool) {
	e, ok := r.entries[id]
	if !ok {
		return
	}
	if changed {
		result.Version = e.signature.Version + 1
	} else {
		result.Version = e.signature.Version
	}
	e.signature = cloneSignatureResult(result)
}

func (r *Registry) currentSignature(id DeclID) SignatureResult {
	if e, ok := r.entries[id]; ok {
		return cloneSignatureResult(e.signature)
	}
	return SignatureResult{Present: false, Signature: MissingSignature(id)}
}

func (r *Registry) signatureVersion(id DeclID) int {
	if e, ok := r.entries[id]; ok {
		return e.signature.Version
	}
	return 0
}

func (r *Registry) basisCurrent(basis Basis) bool {
	for id, expected := range basis {
		e, ok := r.entries[id]
		present := ok && e.signature.Present
		if present != expected.Present {
			return false
		}
		if present && e.signature.Version != expected.Version {
			return false
		}
	}
	return true
}

func cloneSignatureResult(result SignatureResult) SignatureResult {
	result.Basis = cloneBasis(result.Basis)
	return result
}

func cloneBasis(basis Basis) Basis {
	if len(basis) == 0 {
		return Basis{}
	}
	cloned := make(Basis, len(basis))
	for id, entry := range basis {
		cloned[id] = entry
	}
	return cloned
}

func sortedBasis(basis Basis) []DeclID {
	ids := make([]DeclID, 0, len(basis))
	for id := range basis {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
