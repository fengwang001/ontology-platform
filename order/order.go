package order

import "errors"

var ErrInvalidArgument = errors.New("invalid argument")

const (
	Ban = iota
	Pick
)

type Step struct {
	Kind      int
	Team      int
	PickIndex int
}

func New(n, b1, c, b2 int) ([]Step, error) {
	if n < 1 || n > 5 || b1 < 0 || b1 > 5 || c < 0 || c > 2*n || b2 < 0 || b2 > 5 {
		return nil, ErrInvalidArgument
	}
	if c == 2*n && b2 != 0 {
		return nil, ErrInvalidArgument
	}

	pickTeam := func(index int) int {
		switch index % 4 {
		case 0, 3:
			return 1
		default:
			return 2
		}
	}

	steps := make([]Step, 0, 2*b1+2*n+2*b2)
	for i := 0; i < 2*b1; i++ {
		steps = append(steps, Step{Kind: Ban, Team: 1 + i%2})
	}
	for i := 0; i < c; i++ {
		steps = append(steps, Step{Kind: Pick, Team: pickTeam(i), PickIndex: i})
	}
	if b2 > 0 {
		first := pickTeam(c)
		for i := 0; i < 2*b2; i++ {
			team := first
			if i%2 != 0 {
				team = 3 - first
			}
			steps = append(steps, Step{Kind: Ban, Team: team})
		}
	}
	for i := c; i < 2*n; i++ {
		steps = append(steps, Step{Kind: Pick, Team: pickTeam(i), PickIndex: i})
	}
	return steps, nil
}
