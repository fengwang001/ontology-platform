package ontology

import "encoding/json"

// OpDTO 是链接变更的线上表示。
type OpDTO struct {
	TypeID string `json:"typeId"`
	Side   string `json:"side"` // "A" | "B"
	Other  string `json:"other"`
	Add    bool   `json:"add"`
}

// UpdateRequestDTO 是乐观写入请求的线上表示。
type UpdateRequestDTO struct {
	Instance string  `json:"instance"`
	Baseline uint64  `json:"baseline"`
	Ops      []OpDTO `json:"ops"`
}

// VerdictDTO 是单个约束判定依据的线上表示。
type VerdictDTO struct {
	TypeID    string `json:"typeId"`
	Side      string `json:"side"`
	Current   int    `json:"current"`
	Delta     int    `json:"delta"`
	Max       int    `json:"max"`
	Satisfied bool   `json:"satisfied"`
}

// AttemptDTO 是一次内部尝试的可重放记录。
type AttemptDTO struct {
	Index       int          `json:"index"`
	VersionRead uint64       `json:"versionRead"`
	Outcome     string       `json:"outcome"`
	Verdicts    []VerdictDTO `json:"verdicts"`
	Snapshot    []Link       `json:"snapshot"`
}

// UpdateResponseDTO 是最终响应；拒绝时 reason 取三类互斥代码之一。
type UpdateResponseDTO struct {
	Committed bool         `json:"committed"`
	Version   uint64       `json:"version,omitempty"`
	Reason    Code         `json:"reason,omitempty"`
	Message   string       `json:"message,omitempty"`
	Attempts  []AttemptDTO `json:"attempts"`
}

func sideFromString(s string) Side {
	switch s {
	case "A":
		return SideA
	case "B":
		return SideB
	default:
		return SideInvalid
	}
}

// ParseUpdate 把线上 DTO 转为内部请求。
func ParseUpdate(dto UpdateRequestDTO) (Request, error) {
	req := Request{Instance: dto.Instance, Baseline: dto.Baseline}
	for _, op := range dto.Ops {
		side := sideFromString(op.Side)
		if side == SideInvalid {
			return Request{}, &ErrInvalid{Msg: "side must be A or B: " + op.Side}
		}
		req.Ops = append(req.Ops, Op{TypeID: op.TypeID, Side: side, Other: op.Other, Add: op.Add})
	}
	return req, nil
}

// ToResponse 把内部结果转为线上 DTO。
func ToResponse(res *Result) UpdateResponseDTO {
	out := UpdateResponseDTO{
		Committed: res.Committed,
		Version:   res.Version,
		Attempts:  make([]AttemptDTO, 0, len(res.Attempts)),
	}
	if res.Reject != nil {
		out.Reason = res.Reject.Code
		out.Message = res.Reject.Message
	}
	for _, a := range res.Attempts {
		dto := AttemptDTO{
			Index:       a.Index,
			VersionRead: a.VersionRead,
			Outcome:     outcomeName(a.Outcome),
			Snapshot:    a.Snapshot,
			Verdicts:    make([]VerdictDTO, 0, len(a.Verdicts)),
		}
		for _, v := range a.Verdicts {
			dto.Verdicts = append(dto.Verdicts, VerdictDTO{
				TypeID: v.Constraint.TypeID, Side: v.Constraint.Side.String(),
				Current: v.Current, Delta: v.Delta, Max: v.Max, Satisfied: v.Satisfied,
			})
		}
		out.Attempts = append(out.Attempts, dto)
	}
	return out
}

func outcomeName(o AttemptOutcome) string {
	switch o {
	case OutcomeCommitted:
		return "COMMITTED"
	case OutcomeCardinality:
		return "CARDINALITY_VIOLATION"
	default:
		return "VERSION_CONFLICT"
	}
}

// MarshalResponse 便于 HTTP 层输出。
func MarshalResponse(res *Result) ([]byte, error) {
	return json.Marshal(ToResponse(res))
}
