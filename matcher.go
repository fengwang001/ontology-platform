package ontology

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrDuplicateCommit = errors.New("duplicate commit")
	ErrCommitStarted   = errors.New("commit already started")
	ErrTimeRegression  = errors.New("time regression")
)

type UsageLine struct {
	Family   int
	OnDemand int64
}

type CommitReport struct {
	ID      int
	Used    int64
	Unused  int64
	Covered int64
}

type HourReport struct {
	Hour    int
	Bill    int64
	Commits []CommitReport
}

type CommitMatcher struct {
	mu       sync.Mutex
	lastHour int
	commits  map[int]commit
	starts   map[int][]commit
	active   []commit
}

type commit struct {
	id       int
	family   int
	start    int
	hours    int
	hourly   int64
	upfront  int64
	discount int64
}

func NewCommitMatcher() *CommitMatcher {
	return &CommitMatcher{
		lastHour: -1,
		commits:  make(map[int]commit),
		starts:   make(map[int][]commit),
	}
}

func (m *CommitMatcher) AddCommit(id, family, start, hours int, hourly, upfront, discount int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id < 0 || id > 1_000_000 ||
		family < 0 || family > 99 ||
		start < 0 ||
		hours < 1 || hours > 100_000 ||
		hourly < 1 || hourly > 1_000_000_000 ||
		upfront < 0 || upfront > 1_000_000_000_000 ||
		discount < 1 || discount > 9_999 {
		return ErrInvalidArgument
	}
	if _, exists := m.commits[id]; exists {
		return ErrDuplicateCommit
	}
	if start <= m.lastHour {
		return ErrCommitStarted
	}

	m.commits[id] = commit{
		id:       id,
		family:   family,
		start:    start,
		hours:    hours,
		hourly:   hourly,
		upfront:  upfront,
		discount: discount,
	}
	m.starts[start] = append(m.starts[start], m.commits[id])
	return nil
}

func (m *CommitMatcher) Apply(t int, lines []UsageLine) ([]HourReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if t < 0 || t > m.lastHour+10_000 || len(lines) > 1_000 {
		return nil, ErrInvalidArgument
	}
	for _, line := range lines {
		if line.Family < 1 || line.Family > 99 || line.OnDemand < 1 || line.OnDemand > 1_000_000_000 {
			return nil, ErrInvalidArgument
		}
	}
	if t <= m.lastHour {
		return nil, ErrTimeRegression
	}

	reports := make([]HourReport, 0, t-m.lastHour)
	active := m.active
	for hour := m.lastHour + 1; hour <= t; hour++ {
		var hourLines []UsageLine
		if hour == t {
			hourLines = lines
		}
		starting := m.starts[hour]
		active = mergeActive(active, starting, hour)
		delete(m.starts, hour)
		reports = append(reports, processHour(hour, hourLines, active))
	}
	m.lastHour = t
	m.active = active
	return reports, nil
}

func processHour(hour int, lines []UsageLine, active []commit) HourReport {
	remaining := make([]int64, len(lines))
	for i, line := range lines {
		remaining[i] = line.OnDemand
	}
	report := HourReport{Hour: hour, Commits: make([]CommitReport, 0, len(active))}
	if len(lines) == 0 {
		for _, activeCommit := range active {
			report.Bill += activeCommit.hourly + amortization(activeCommit, hour)
			report.Commits = append(report.Commits, CommitReport{
				ID:     activeCommit.id,
				Unused: activeCommit.hourly,
			})
		}
		return report
	}

	prevGlobal := make([]int, len(lines))
	nextGlobal := make([]int, len(lines))
	prevFamily := make([][]int, 100)
	nextFamily := make([][]int, 100)
	for family := range prevFamily {
		prevFamily[family] = make([]int, len(lines))
		nextFamily[family] = make([]int, len(lines))
	}
	heads := make([]int, 100)
	tails := make([]int, 100)
	for family := range heads {
		heads[family] = -1
		tails[family] = -1
	}
	globalHead := -1
	globalTail := -1
	for i := range lines {
		prevGlobal[i] = -1
		nextGlobal[i] = -1
		for family := range prevFamily {
			prevFamily[family][i] = -1
			nextFamily[family][i] = -1
		}
		if globalTail == -1 {
			globalHead = i
		} else {
			prevGlobal[i] = globalTail
			nextGlobal[globalTail] = i
		}
		globalTail = i

		family := lines[i].Family
		if tails[family] == -1 {
			heads[family] = i
		} else {
			previous := tails[family]
			prevFamily[family][i] = previous
			nextFamily[family][previous] = i
		}
		tails[family] = i
	}

	removeLine := func(index int) {
		if previous := prevGlobal[index]; previous != -1 {
			nextGlobal[previous] = nextGlobal[index]
		} else {
			globalHead = nextGlobal[index]
		}
		if next := nextGlobal[index]; next != -1 {
			prevGlobal[next] = prevGlobal[index]
		} else {
			globalTail = prevGlobal[index]
		}

		family := lines[index].Family
		if previous := prevFamily[family][index]; previous != -1 {
			nextFamily[family][previous] = nextFamily[family][index]
		} else {
			heads[family] = nextFamily[family][index]
		}
		if next := nextFamily[family][index]; next != -1 {
			prevFamily[family][next] = prevFamily[family][index]
		}
	}

	for _, activeCommit := range active {
		hourAmortization := amortization(activeCommit, hour)
		report.Bill += activeCommit.hourly + hourAmortization

		remainingCapacity := activeCommit.hourly
		covered := int64(0)
		discountedFactor := 10_000 - activeCommit.discount
		head := globalHead
		if activeCommit.family != 0 {
			head = heads[activeCommit.family]
		}
		for index := head; index != -1 && remainingCapacity > 0; {
			next := nextGlobal[index]
			if activeCommit.family != 0 {
				next = nextFamily[activeCommit.family][index]
			}
			oldPrice := remaining[index]
			discountedPrice := ceilDiv(oldPrice*discountedFactor, 10_000)
			if remainingCapacity >= discountedPrice {
				remainingCapacity -= discountedPrice
				covered += oldPrice
				remaining[index] = 0
				removeLine(index)
			} else {
				partialPrice := remainingCapacity * 10_000 / discountedFactor
				remaining[index] -= partialPrice
				covered += partialPrice
				remainingCapacity = 0
			}
			index = next
		}

		report.Commits = append(report.Commits, CommitReport{
			ID:      activeCommit.id,
			Used:    activeCommit.hourly - remainingCapacity,
			Unused:  remainingCapacity,
			Covered: covered,
		})
	}

	for _, price := range remaining {
		report.Bill += price
	}
	return report
}

func ceilDiv(value, divisor int64) int64 {
	return (value + divisor - 1) / divisor
}

func lessCommit(left, right commit) bool {
	if left.discount != right.discount {
		return left.discount > right.discount
	}
	return left.id < right.id
}

func mergeActive(active, starting []commit, hour int) []commit {
	kept := make([]commit, 0, len(active))
	for _, activeCommit := range active {
		if hour < activeCommit.start+activeCommit.hours {
			kept = append(kept, activeCommit)
		}
	}
	sortedStarting := append([]commit(nil), starting...)
	sort.Slice(sortedStarting, func(i, j int) bool {
		return lessCommit(sortedStarting[i], sortedStarting[j])
	})
	merged := make([]commit, 0, len(kept)+len(sortedStarting))
	i, j := 0, 0
	for i < len(kept) && j < len(sortedStarting) {
		if lessCommit(kept[i], sortedStarting[j]) {
			merged = append(merged, kept[i])
			i++
		} else {
			merged = append(merged, sortedStarting[j])
			j++
		}
	}
	merged = append(merged, kept[i:]...)
	merged = append(merged, sortedStarting[j:]...)
	return merged
}

func amortization(candidate commit, hour int) int64 {
	base := candidate.upfront / int64(candidate.hours)
	if hour == candidate.start+candidate.hours-1 {
		return candidate.upfront - int64(candidate.hours-1)*base
	}
	return base
}
