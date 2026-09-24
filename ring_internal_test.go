package ontology

// locateLive returns the owner of key and verifies, within the same read
// lock acquisition, that the returned node is on the ring at that instant.
// It exists for concurrency tests where checking membership after Locate
// returns would itself race with a concurrent Remove.
func (r *Ring) locateLive(key string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.ring) == 0 {
		return "", false
	}
	h := hashString(key)
	i := sort.Search(len(r.ring), func(i int) bool {
		return r.ring[i].pos >= h
	})
	if i == len(r.ring) {
		i = 0
	}
	node := r.ring[i].node
	_, present := r.nodes[node]
	return node, present
}
