package ontology

import (
	"errors"
	"math/big"
	"sync"
)

var (
	ErrInvalidArguments = errors.New("invalid arguments")
	ErrStopped          = errors.New("sequential test is stopped")
	ErrDataRegression   = errors.New("cumulative data regressed")
)

type State int

const (
	Running State = iota
	Stopped
)

func (state State) String() string {
	switch state {
	case Running:
		return "running"
	case Stopped:
		return "stopped"
	default:
		return "unknown"
	}
}

type Conclusion int

const (
	NoConclusion Conclusion = iota - 1
	ContinueSampleTooSmall
	ContinueObservation
	ContinueRunning
	Winner
	Worse
	Futile
	RatioInvalid
)

func (conclusion Conclusion) String() string {
	switch conclusion {
	case NoConclusion:
		return "none"
	case ContinueSampleTooSmall:
		return "continue_sample_too_small"
	case ContinueObservation:
		return "continue_observation"
	case ContinueRunning:
		return "continue"
	case Winner:
		return "winner"
	case Worse:
		return "worse"
	case Futile:
		return "futile"
	case RatioInvalid:
		return "ratio_invalid"
	default:
		return "unknown"
	}
}

type LookResult struct {
	Conclusion     Conclusion
	Reason         string
	Accepted       bool
	EffectiveLook  bool
	LookIndex      int
	Boundary       int64
	BoundaryUsed   bool
	D              int64
	StatisticLeft  string
	StatisticRight string
}

type StatusSnapshot struct {
	State          State
	EffectiveLooks int
	LastCountedN   int64
	LastConclusion Conclusion
}

type SequentialABStopper struct {
	mu sync.Mutex

	rA       int64
	rB       int64
	nmin     int64
	minStep  int64
	nmax     int64
	boundary []int64
	tf       int64
	tau      int64

	state           State
	effectiveLooks  int
	lastCountedN    int64
	lastNA          int64
	lastCA          int64
	lastNB          int64
	lastCB          int64
	hasAcceptedLook bool
	lastConclusion  Conclusion
}

func NewSequentialABStopper(rA, rB, nmin, minStep, Nmax int64, boundaries []int64, tf, tau int64) (*SequentialABStopper, error) {
	if rA < 1 || rA > 100 ||
		rB < 1 || rB > 100 ||
		nmin < 1 || nmin > 1_000_000 ||
		minStep < 1 || minStep > 1_000_000 ||
		Nmax < 2 || Nmax > 2_000_000 ||
		len(boundaries) < 1 || len(boundaries) > 8 ||
		tf < 0 || tf > 1_000_000 ||
		tau < 0 || tau > 100 {
		return nil, ErrInvalidArguments
	}

	copiedBoundaries := make([]int64, len(boundaries))
	for i, boundary := range boundaries {
		if boundary < 1 || boundary > 1_000_000 {
			return nil, ErrInvalidArguments
		}
		copiedBoundaries[i] = boundary
	}

	return &SequentialABStopper{
		rA:             rA,
		rB:             rB,
		nmin:           nmin,
		minStep:        minStep,
		nmax:           Nmax,
		boundary:       copiedBoundaries,
		tf:             tf,
		tau:            tau,
		lastConclusion: NoConclusion,
	}, nil
}

func (s *SequentialABStopper) Look(nA, cA, nB, cB int64) (LookResult, error) {
	if nA < 0 || cA < 0 || cA > nA || nA > 1_000_000 ||
		nB < 0 || cB < 0 || cB > nB || nB > 1_000_000 {
		return LookResult{}, ErrInvalidArguments
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state == Stopped {
		return LookResult{}, ErrStopped
	}

	if s.hasAcceptedLook &&
		(nA < s.lastNA || cA < s.lastCA || nB < s.lastNB || cB < s.lastCB) {
		return LookResult{}, ErrDataRegression
	}

	s.lastNA = nA
	s.lastCA = cA
	s.lastNB = nB
	s.lastCB = cB
	s.hasAcceptedLook = true

	n := nA + nB
	result := LookResult{
		Accepted: true,
		D:        cB*nA - cA*nB,
	}

	weightedA := nA * s.rB
	weightedB := nB * s.rA
	weightedTotal := weightedA + weightedB
	if n >= 2*s.nmin && absInt64(weightedA-weightedB)*100 > s.tau*weightedTotal {
		result.Conclusion = RatioInvalid
		result.Reason = "allocation ratio exceeds tolerance"
		s.finish(result)
		return result, nil
	}

	if nA < s.nmin || nB < s.nmin {
		result.Conclusion = ContinueSampleTooSmall
		result.Reason = "at least one group is below nmin"
		s.lastConclusion = result.Conclusion
		return result, nil
	}

	if n-s.lastCountedN < s.minStep {
		result.Conclusion = ContinueObservation
		result.Reason = "increment since counted look is below minStep"
		s.lastConclusion = result.Conclusion
		return result, nil
	}

	s.effectiveLooks++
	s.lastCountedN = n
	result.EffectiveLook = true
	result.LookIndex = s.effectiveLooks
	result.Boundary = s.boundary[len(s.boundary)-1]
	if result.LookIndex <= len(s.boundary) {
		result.Boundary = s.boundary[result.LookIndex-1]
	}
	result.BoundaryUsed = true
	c := cA + cB

	if c == 0 || c == n {
		result.StatisticLeft = "0"
		result.StatisticRight = "0"

		if n >= s.nmax {
			result.Conclusion = Futile
			result.Reason = "sample limit reached; statistic is undefined"
			s.finish(result)
			return result, nil
		}

		if 2*n >= s.nmax && s.tf > 0 {
			result.Conclusion = Futile
			result.Reason = "futility boundary applies when statistic is undefined and Tf > 0"
			s.finish(result)
			return result, nil
		}

		result.Conclusion = ContinueRunning
		result.Reason = "statistic is undefined and treated as zero"
		s.lastConclusion = result.Conclusion
		return result, nil
	}

	left := new(big.Int).SetInt64(result.D)
	left.Mul(left, left)
	left.Mul(left, big.NewInt(n))
	left.Mul(left, big.NewInt(100))

	right := big.NewInt(result.Boundary)
	right.Mul(right, big.NewInt(nA))
	right.Mul(right, big.NewInt(nB))
	right.Mul(right, big.NewInt(c))
	right.Mul(right, big.NewInt(n-c))

	result.StatisticLeft = left.String()
	result.StatisticRight = right.String()

	if left.Cmp(right) >= 0 {
		if result.D > 0 {
			result.Conclusion = Winner
			result.Reason = "B is significantly better"
		} else {
			result.Conclusion = Worse
			result.Reason = "B is significantly worse"
		}
		s.finish(result)
		return result, nil
	}

	if n >= s.nmax {
		result.Conclusion = Futile
		result.Reason = "sample limit reached without significance"
		s.finish(result)
		return result, nil
	}

	futilityRight := big.NewInt(s.tf)
	futilityRight.Mul(futilityRight, big.NewInt(nA))
	futilityRight.Mul(futilityRight, big.NewInt(nB))
	futilityRight.Mul(futilityRight, big.NewInt(c))
	futilityRight.Mul(futilityRight, big.NewInt(n-c))

	if 2*n >= s.nmax && left.Cmp(futilityRight) < 0 {
		result.Conclusion = Futile
		result.Reason = "futility boundary reached"
		s.finish(result)
		return result, nil
	}

	result.Conclusion = ContinueRunning
	result.Reason = "no stopping boundary reached"
	s.lastConclusion = result.Conclusion
	return result, nil
}

func (s *SequentialABStopper) Status() StatusSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	return StatusSnapshot{
		State:          s.state,
		EffectiveLooks: s.effectiveLooks,
		LastCountedN:   s.lastCountedN,
		LastConclusion: s.lastConclusion,
	}
}

func (s *SequentialABStopper) finish(result LookResult) {
	s.state = Stopped
	s.lastConclusion = result.Conclusion
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
