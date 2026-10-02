package ontology

import "strings"

type snapshot struct {
	maxPath  int
	certs    map[string]*storedCert
	byIssuer map[subjectKey][]string
	trusted  map[string]struct{}
}

type pathSearch struct {
	snapshot        snapshot
	name            string
	now             int64
	structuralFound bool
	path            []string
	seen            map[string]int
	examined        int
	successfulPath  []string
	firstFailure    *VerifyFailure
}

func (search *pathSearch) dfs() bool {
	currentID := search.path[len(search.path)-1]
	if _, trusted := search.snapshot.trusted[currentID]; trusted {
		search.structuralFound = true
		_, failure := validatePath(search.snapshot, search.path, search.name, search.now)
		if failure == nil {
			search.successfulPath = append([]string(nil), search.path...)
		}
		if failure != nil && search.firstFailure == nil {
			search.firstFailure = failure
		}
		return failure == nil
	}
	current := search.snapshot.certs[currentID].cert
	candidates := search.snapshot.byIssuer[subjectKey{
		subject: string(current.Issuer),
		key:     string(current.AuthKey),
	}]
	for _, candidateID := range candidates {
		if candidateID == currentID {
			continue
		}
		search.examined++
		if _, seen := search.seen[candidateID]; seen {
			continue
		}
		if len(search.path) >= search.snapshot.maxPath {
			continue
		}
		search.seen[candidateID] = len(search.path)
		search.path = append(search.path, candidateID)
		accepted := search.dfs()
		search.path = search.path[:len(search.path)-1]
		delete(search.seen, candidateID)
		if accepted {
			return true
		}
	}
	return false
}

func validatePath(snap snapshot, ids []string, name string, now int64) ([]Bytes, *VerifyFailure) {
	path := make([]Certificate, len(ids))
	pathIDs := make([]Bytes, len(ids))
	for idx, id := range ids {
		path[idx] = snap.certs[id].cert
		pathIDs[idx] = append(Bytes(nil), path[idx].ID...)
	}
	fail := func(reason FailureReason, idx int) *VerifyFailure {
		return &VerifyFailure{Reason: reason, CertIdx: idx, Path: pathIDs}
	}

	for idx, cert := range path {
		if now < cert.NotBefore {
			return pathIDs, fail(FailureNotYetValid, idx)
		}
		if now >= cert.NotAfter {
			return pathIDs, fail(FailureExpired, idx)
		}
		if revokedAt := snap.certs[ids[idx]].revokedAt; revokedAt >= 0 && now >= revokedAt {
			return pathIDs, fail(FailureRevoked, idx)
		}
	}
	for idx := 1; idx < len(path); idx++ {
		if !path[idx].IsCA {
			return pathIDs, fail(FailureNotCA, idx)
		}
	}
	for idx := 1; idx < len(path); idx++ {
		if path[idx].PathLen >= 0 {
			nonSelfIssued := 0
			for inner := 1; inner < idx; inner++ {
				if !bytesEqual(path[inner].Subject, path[inner].Issuer) {
					nonSelfIssued++
				}
			}
			if nonSelfIssued > path[idx].PathLen {
				return pathIDs, fail(FailurePathLenExceeded, idx)
			}
		}
	}
	for idx := 1; idx < len(path); idx++ {
		for _, subtree := range path[idx].Excluded {
			if subtreeMatch(name, subtree) {
				return pathIDs, fail(FailureExcludedDNS, idx)
			}
		}
		if len(path[idx].Permitted) > 0 {
			permitted := false
			for _, subtree := range path[idx].Permitted {
				if subtreeMatch(name, subtree) {
					permitted = true
					break
				}
			}
			if !permitted {
				return pathIDs, fail(FailurePermittedDNS, idx)
			}
		}
	}
	for _, san := range path[0].SAN {
		if sanMatch(name, san) {
			return pathIDs, nil
		}
	}
	return pathIDs, fail(FailureSANMismatch, 0)
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for idx := range left {
		if left[idx] != right[idx] {
			return false
		}
	}
	return true
}

func subtreeMatch(name, subtree string) bool {
	if strings.HasPrefix(subtree, ".") {
		return strings.HasSuffix(name, subtree)
	}
	return name == subtree || strings.HasSuffix(name, "."+subtree)
}

func sanMatch(name, san string) bool {
	if strings.HasPrefix(san, "*.") {
		suffix := san[2:]
		dot := strings.IndexByte(name, '.')
		return dot > 0 && name[dot+1:] == suffix
	}
	return name == san
}
