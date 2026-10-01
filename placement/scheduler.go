package placement

import (
	"sort"
	"sync"
)

// Scheduler is a concurrent pod placement filter. All methods are safe to call
// from multiple goroutines; their effects are equivalent to some serial order.
type Scheduler struct {
	mu          sync.Mutex
	q           int
	nodes       map[string]string // node name -> zone
	pods        map[string]*podState
	reservedIDs int
}

// NewScheduler creates a Scheduler whose number of reserved pods never
// exceeds reservationQuota.
func NewScheduler(reservationQuota int) *Scheduler {
	if reservationQuota < 1 || reservationQuota > 1000 {
		panic("placement: reservation quota must be in [1,1000]")
	}
	return &Scheduler{
		q:     reservationQuota,
		nodes: make(map[string]string),
		pods:  make(map[string]*podState),
	}
}

// AddNode registers a node in the given zone.
func (s *Scheduler) AddNode(name, zone string) error {
	if name == "" || zone == "" {
		return reject(ReasonInvalidArgs)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes[name]; ok {
		return &Reject{Code: ReasonNodeExists, BlockingPod: name}
	}
	s.nodes[name] = zone
	return nil
}

// RemoveNode removes a node only when it hosts no placed or reserved pod.
func (s *Scheduler) RemoveNode(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes[name]; !ok {
		return &Reject{Code: ReasonNodeNotFound, BlockingPod: name}
	}
	for _, st := range s.pods {
		if st.node == name {
			return &Reject{Code: ReasonNodeNotEmpty, BlockingPod: name}
		}
	}
	delete(s.nodes, name)
	return nil
}

// Reserve validates placement on node and records pod as a reservation.
func (s *Scheduler) Reserve(pod *Pod, node string) error {
	if err := validatePod(pod); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.placeLocked(clonePod(pod), node, true)
}

// Place validates placement on node and records pod as committed immediately.
// Committed pods do not consume reservation quota.
func (s *Scheduler) Place(pod *Pod, node string) error {
	if err := validatePod(pod); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.placeLocked(clonePod(pod), node, false)
}

// Commit turns an existing reservation into a committed placement without
// re-validating it.
func (s *Scheduler) Commit(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.pods[id]
	if !ok {
		return &Reject{Code: ReasonPodNotFound, BlockingPod: id}
	}
	if !st.reserved {
		return &Reject{Code: ReasonNotReserved, BlockingPod: id}
	}
	st.reserved = false
	s.reservedIDs--
	return nil
}

// Cancel deletes a reservation.
func (s *Scheduler) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.pods[id]
	if !ok {
		return &Reject{Code: ReasonPodNotFound, BlockingPod: id}
	}
	if !st.reserved {
		return &Reject{Code: ReasonNotReserved, BlockingPod: id}
	}
	delete(s.pods, id)
	s.reservedIDs--
	return nil
}

// Remove deletes a committed pod. It never removes reservations and does not
// re-validate other pods.
func (s *Scheduler) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.pods[id]
	if !ok {
		return &Reject{Code: ReasonPodNotFound, BlockingPod: id}
	}
	if st.reserved {
		return &Reject{Code: ReasonStillReserved, BlockingPod: id}
	}
	delete(s.pods, id)
	return nil
}

// Relabel replaces the labels of a placed or reserved pod. Only the direction
// "existing pods repel the relabeled pod" is checked; its own terms and
// affinities are not re-evaluated.
func (s *Scheduler) Relabel(id string, labels map[string]string) error {
	for k := range labels {
		if k == "" {
			return reject(ReasonInvalidArgs)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.pods[id]
	if !ok {
		return &Reject{Code: ReasonPodNotFound, BlockingPod: id}
	}
	newLabels := cloneLabels(labels)
	if blocker := repellerLocked(s.pods, s.nodes, id, st.node, newLabels); blocker != "" {
		return &Reject{Code: ReasonRepelled, BlockingPod: blocker}
	}
	st.pod.Labels = newLabels
	return nil
}

// Feasible returns the names of every node where pod could currently be placed,
// sorted by byte order. It does not modify state.
func (s *Scheduler) Feasible(pod *Pod) ([]string, error) {
	if err := validatePod(pod); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pods[pod.ID]; ok {
		return nil, &Reject{Code: ReasonPodExists, BlockingPod: pod.ID}
	}
	candidate := clonePod(pod)
	names := make([]string, 0, len(s.nodes))
	for name := range s.nodes {
		if checkNodeLocked(s.pods, s.nodes, candidate, name) == nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// Reservations returns the current number of reserved pods.
func (s *Scheduler) Reservations() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reservedIDs
}

func (s *Scheduler) placeLocked(pod *Pod, node string, reserve bool) error {
	if _, ok := s.pods[pod.ID]; ok {
		return &Reject{Code: ReasonPodExists, BlockingPod: pod.ID}
	}
	if _, ok := s.nodes[node]; !ok {
		return &Reject{Code: ReasonNodeNotFound, BlockingPod: node}
	}
	if err := checkNodeLocked(s.pods, s.nodes, pod, node); err != nil {
		return err
	}
	if reserve && s.reservedIDs >= s.q {
		return reject(ReasonReservationsFull)
	}
	s.pods[pod.ID] = &podState{pod: pod, node: node, reserved: reserve}
	if reserve {
		s.reservedIDs++
	}
	return nil
}

func checkNodeLocked(pods map[string]*podState, zones map[string]string, x *Pod, node string) *Reject {
	for i, term := range x.Affinity {
		if !affinitySatisfied(pods, zones, x, term, node) {
			return &Reject{Code: ReasonAffinityUnsatisfied, Index: i}
		}
	}
	for i, term := range x.AntiAffinity {
		if blocker := firstInDomain(pods, zones, "", node, parseTopology(term.Topology), func(labels map[string]string) bool {
			return matches(term.Selector, labels)
		}); blocker != "" {
			return &Reject{Code: ReasonAntiAffinityConflict, Index: i, BlockingPod: blocker}
		}
	}
	if blocker := repellerLocked(pods, zones, "", node, x.Labels); blocker != "" {
		return &Reject{Code: ReasonRepelled, BlockingPod: blocker}
	}
	return nil
}

func affinitySatisfied(pods map[string]*podState, zones map[string]string, x *Pod, term AffinityTerm, node string) bool {
	topo := parseTopology(term.Topology)
	hasClusterMatch := false
	sameDomain := 0
	for _, st := range pods {
		if matches(term.Selector, st.pod.Labels) {
			hasClusterMatch = true
			if sameTopology(zones, st.node, node, topo) {
				sameDomain++
			}
		}
	}
	if sameDomain >= term.MinMatching {
		return true
	}
	return !hasClusterMatch && matches(term.Selector, x.Labels)
}

func repellerLocked(pods map[string]*podState, zones map[string]string, selfID, selfNode string, labels map[string]string) string {
	best := ""
	for qid, q := range pods {
		if qid == selfID {
			continue
		}
		for _, term := range q.pod.AntiAffinity {
			if matches(term.Selector, labels) &&
				sameTopology(zones, q.node, selfNode, parseTopology(term.Topology)) {
				if best == "" || qid < best {
					best = qid
				}
				break
			}
		}
	}
	return best
}

func firstInDomain(pods map[string]*podState, zones map[string]string, selfID, selfNode string, topo topology, pred func(map[string]string) bool) string {
	ids := make([]string, 0)
	for id, st := range pods {
		if id == selfID {
			continue
		}
		if sameTopology(zones, st.node, selfNode, topo) && pred(st.pod.Labels) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	sort.Strings(ids)
	return ids[0]
}

func sameTopology(zones map[string]string, a, b string, topo topology) bool {
	if topo == topologyNode {
		return a == b
	}
	return zones[a] == zones[b]
}

func parseTopology(v string) topology {
	if topology(v) == topologyZone {
		return topologyZone
	}
	return topologyNode
}

func matches(selector Selector, labels map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func validatePod(p *Pod) *Reject {
	if p == nil || p.ID == "" {
		return reject(ReasonInvalidArgs)
	}
	for k := range p.Labels {
		if k == "" {
			return reject(ReasonInvalidArgs)
		}
	}
	for _, term := range p.Affinity {
		if !validTerm(term.Selector, term.Topology) || term.MinMatching < 1 || term.MinMatching > 100 {
			return reject(ReasonInvalidArgs)
		}
	}
	for _, term := range p.AntiAffinity {
		if !validTerm(term.Selector, term.Topology) {
			return reject(ReasonInvalidArgs)
		}
	}
	return nil
}

func validTerm(selector Selector, topologyKey string) bool {
	if topologyKey != "node" && topologyKey != "zone" {
		return false
	}
	for k := range selector {
		if k == "" {
			return false
		}
	}
	return true
}

func clonePod(p *Pod) *Pod {
	return &Pod{
		ID:           p.ID,
		Labels:       cloneLabels(p.Labels),
		Affinity:     cloneAffinity(p.Affinity),
		AntiAffinity: cloneAntiAffinity(p.AntiAffinity),
	}
}

func cloneLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneAffinity(in []AffinityTerm) []AffinityTerm {
	if in == nil {
		return nil
	}
	out := make([]AffinityTerm, len(in))
	for i, term := range in {
		out[i] = AffinityTerm{Selector: cloneSelector(term.Selector), Topology: term.Topology, MinMatching: term.MinMatching}
	}
	return out
}

func cloneAntiAffinity(in []AntiAffinityTerm) []AntiAffinityTerm {
	if in == nil {
		return nil
	}
	out := make([]AntiAffinityTerm, len(in))
	for i, term := range in {
		out[i] = AntiAffinityTerm{Selector: cloneSelector(term.Selector), Topology: term.Topology}
	}
	return out
}

func cloneSelector(in Selector) Selector {
	out := make(Selector, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
