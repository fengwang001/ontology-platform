package placement

import "fmt"

// Selector is a set of key=value requirements. An empty selector matches every pod.
type Selector map[string]string

// AffinityTerm requires at least MinMatching pods matching the selector within
// the same topology domain, unless the first-member exemption applies.
type AffinityTerm struct {
	Selector    Selector
	Topology    string
	MinMatching int
}

// AntiAffinityTerm forbids any pod matching the selector in the same topology
// domain.
type AntiAffinityTerm struct {
	Selector Selector
	Topology string
}

// Pod is an immutable pod specification supplied by the caller.
type Pod struct {
	ID           string
	Labels       map[string]string
	Affinity     []AffinityTerm
	AntiAffinity []AntiAffinityTerm
}

type topology string

const (
	topologyNode topology = "node"
	topologyZone topology = "zone"
)

type podState struct {
	pod      *Pod
	node     string
	reserved bool
}

// Reject is the error returned for every rejected operation. Code is one of
// the Reason* constants; Reason is exported so callers can branch precisely.
type Reject struct {
	Code        Reason
	Index       int // 0-based index of the first failing declared term
	BlockingPod string
}

// Reason identifies a rejection cause.
type Reason int

const (
	ReasonInvalidArgs Reason = iota + 1
	ReasonPodExists
	ReasonNodeNotFound
	ReasonAffinityUnsatisfied
	ReasonAntiAffinityConflict
	ReasonRepelled
	ReasonReservationsFull
	ReasonPodNotFound
	ReasonNotReserved
	ReasonStillReserved
	ReasonNodeExists
	ReasonNodeNotEmpty
)

func (e *Reject) Error() string {
	switch e.Code {
	case ReasonInvalidArgs:
		return "invalid arguments"
	case ReasonPodExists:
		return "pod already exists: " + e.BlockingPod
	case ReasonNodeNotFound:
		return "node does not exist: " + e.BlockingPod
	case ReasonAffinityUnsatisfied:
		return fmt.Sprintf("affinity term %d is not satisfied", e.Index)
	case ReasonAntiAffinityConflict:
		return fmt.Sprintf("anti-affinity term %d is blocked by pod %s", e.Index, e.BlockingPod)
	case ReasonRepelled:
		return "repelled by existing pod " + e.BlockingPod
	case ReasonReservationsFull:
		return fmt.Sprintf("reservation quota exhausted")
	case ReasonPodNotFound:
		return "pod does not exist: " + e.BlockingPod
	case ReasonNotReserved:
		return "pod is not reserved: " + e.BlockingPod
	case ReasonStillReserved:
		return "pod is still reserved: " + e.BlockingPod
	case ReasonNodeExists:
		return "node already exists: " + e.BlockingPod
	case ReasonNodeNotEmpty:
		return "node still hosts pods: " + e.BlockingPod
	default:
		return "rejected"
	}
}

func reject(code Reason) *Reject { return &Reject{Code: code} }
