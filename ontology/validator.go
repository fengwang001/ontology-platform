package ontology

import (
	"bytes"
	"sort"
	"sync"
)

type subjectKey struct {
	subject string
	key     string
}

type storedCert struct {
	cert      Certificate
	revokedAt int64
}

type Validator struct {
	mu       sync.RWMutex
	maxPath  int
	maxCerts int
	certs    map[string]*storedCert
	order    []string
	byIssuer map[subjectKey][]string
	trusted  map[string]struct{}
}

func New(maxPathLen, maxCerts int) (*Validator, error) {
	if maxPathLen < 1 || maxPathLen > 16 {
		return nil, invalid("maxPathLen", "must be between 1 and 16")
	}
	if maxCerts < 1 || maxCerts > 100_000 {
		return nil, invalid("maxCerts", "must be between 1 and 100000")
	}
	return &Validator{
		maxPath:  maxPathLen,
		maxCerts: maxCerts,
		certs:    make(map[string]*storedCert),
		byIssuer: make(map[subjectKey][]string),
		trusted:  make(map[string]struct{}),
	}, nil
}

func (v *Validator) Add(cert Certificate) error {
	if err := validateCertificate(cert); err != nil {
		return err
	}
	id := string(cert.ID)
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, exists := v.certs[id]; exists {
		return conflict("cert.ID")
	}
	if len(v.certs) >= v.maxCerts {
		return limitExceeded("maxCerts")
	}
	stored := cloneCertificate(cert)
	v.certs[id] = &storedCert{cert: stored, revokedAt: -1}
	v.order = append(v.order, id)
	key := subjectKey{subject: string(stored.Subject), key: string(stored.Key)}
	v.byIssuer[key] = insertCandidate(v.byIssuer[key], id, v.certs)
	return nil
}

func (v *Validator) Trust(id Bytes) error {
	if len(id) == 0 {
		return invalid("id", "must not be empty")
	}
	key := string(id)
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, exists := v.certs[key]; !exists {
		return notFound("id")
	}
	v.trusted[key] = struct{}{}
	return nil
}

func (v *Validator) Revoke(id Bytes, at int64) error {
	if len(id) == 0 {
		return invalid("id", "must not be empty")
	}
	if err := validateTime(at, "at"); err != nil {
		return err
	}
	key := string(id)
	v.mu.Lock()
	defer v.mu.Unlock()
	cert, exists := v.certs[key]
	if !exists {
		return notFound("id")
	}
	if cert.revokedAt == -1 || at < cert.revokedAt {
		cert.revokedAt = at
	}
	return nil
}

func (v *Validator) Verify(leafID Bytes, name string, now int64) (VerifyResult, error) {
	if len(leafID) == 0 {
		return VerifyResult{}, invalid("leafID", "must not be empty")
	}
	if err := validateRequestName(name); err != nil {
		return VerifyResult{}, err
	}
	if err := validateTime(now, "now"); err != nil {
		return VerifyResult{}, err
	}
	leafKey := string(leafID)
	v.mu.RLock()
	_, exists := v.certs[leafKey]
	if !exists {
		v.mu.RUnlock()
		return VerifyResult{}, notFound("leafID")
	}
	snapshot := snapshot{
		maxPath:  v.maxPath,
		certs:    make(map[string]*storedCert, len(v.certs)),
		byIssuer: make(map[subjectKey][]string, len(v.byIssuer)),
		trusted:  make(map[string]struct{}, len(v.trusted)),
	}
	for id, entry := range v.certs {
		cert := cloneCertificate(entry.cert)
		snapshot.certs[id] = &storedCert{cert: cert, revokedAt: entry.revokedAt}
	}
	for key, ids := range v.byIssuer {
		snapshot.byIssuer[key] = append([]string(nil), ids...)
	}
	for id := range v.trusted {
		snapshot.trusted[id] = struct{}{}
	}
	v.mu.RUnlock()

	search := &pathSearch{
		snapshot: snapshot,
		name:     name,
		now:      now,
		seen:     map[string]int{leafKey: 0},
	}
	search.path = append(search.path, leafKey)
	found := search.dfs()

	result := VerifyResult{NoPath: !search.structuralFound, candidates: search.examined}
	if found {
		result.Path = idPaths(search.successfulPath, snapshot)
	} else {
		result.Failure = search.firstFailure
	}
	return result, nil
}

func idPaths(ids []string, snap snapshot) []Bytes {
	pathIDs := make([]Bytes, len(ids))
	for idx, id := range ids {
		pathIDs[idx] = append(Bytes(nil), snap.certs[id].cert.ID...)
	}
	return pathIDs
}

func cloneCertificate(cert Certificate) Certificate {
	clone := cert
	clone.ID = append(Bytes(nil), cert.ID...)
	clone.Subject = append(Bytes(nil), cert.Subject...)
	clone.Issuer = append(Bytes(nil), cert.Issuer...)
	clone.Key = append(Bytes(nil), cert.Key...)
	clone.AuthKey = append(Bytes(nil), cert.AuthKey...)
	clone.Permitted = cloneStrings(cert.Permitted)
	clone.Excluded = cloneStrings(cert.Excluded)
	clone.SAN = cloneStrings(cert.SAN)
	return clone
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func insertCandidate(ids []string, id string, certs map[string]*storedCert) []string {
	position := sort.Search(len(ids), func(index int) bool {
		left := certs[ids[index]].cert
		right := certs[id].cert
		if left.NotAfter != right.NotAfter {
			return left.NotAfter < right.NotAfter
		}
		return bytes.Compare(left.ID, right.ID) > 0
	})
	ids = append(ids, "")
	copy(ids[position+1:], ids[position:])
	ids[position] = id
	return ids
}
