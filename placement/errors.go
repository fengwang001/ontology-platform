package placement

// Reason 标识可区分的拒绝原因。
type Reason string

const (
	ReasonInvalidArgument       Reason = "invalid_argument"
	ReasonPodExists             Reason = "pod_exists"
	ReasonNodeNotFound          Reason = "node_not_found"
	ReasonAffinityNotSatisfied  Reason = "affinity_not_satisfied"
	ReasonAntiAffinityConflict  Reason = "anti_affinity_conflict"
	ReasonRejectedByExistingPod Reason = "rejected_by_existing_pod"
	ReasonReservationFull       Reason = "reservation_full"
	ReasonPodNotFound           Reason = "pod_not_found"
	ReasonNotReserved           Reason = "pod_not_reserved"
	ReasonStillReserved         Reason = "pod_still_reserved"
	ReasonNodeExists            Reason = "node_exists"
	ReasonNodeInUse             Reason = "node_in_use"
)

// PlacementError 是带结构化拒绝原因的错误。
type PlacementError struct {
	Reason Reason
	// Detail 为人类可读说明。
	Detail string
	// TermIndex 为亲和/反亲和项的声明顺序下标（从 0 开始），无则为 -1。
	TermIndex int
	// BlockerID 为阻挡者（冲突/排斥）中字节序最小的 Pod ID。
	BlockerID string
}

func (e *PlacementError) Error() string {
	return string(e.Reason) + ": " + e.Detail
}

func errInvalid(detail string) *PlacementError {
	return &PlacementError{Reason: ReasonInvalidArgument, Detail: detail, TermIndex: -1}
}
