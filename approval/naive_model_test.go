package approval

type naiveStage struct {
	def             StageDefinition
	status          string
	startedAt       *int
	passedAt        *int
	failedAt        *int
	segmentStartAt  *int
	correctionDueAt *int
	remaining       int
	corrections     int
	overdue         bool
}

type naiveModel struct {
	calendar Calendar
	defs     []StageDefinition
	stages   map[string]*naiveStage
	actors   map[string]string
	clock    int
	finalAt  *int
	overall  string
}

func newNaiveModel(defs []StageDefinition, holidays []int, acceptedAt int) *naiveModel {
	model := &naiveModel{
		calendar: NewCalendar(holidays),
		defs:     defs,
		stages:   map[string]*naiveStage{},
		actors:   map[string]string{},
		clock:    acceptedAt,
		overall:  OverallProcessing,
	}
	for _, def := range defs {
		model.stages[def.ID] = &naiveStage{def: def, status: StageNotStarted, remaining: def.TimeLimit}
		model.actors["actor-"+def.Department] = def.Department
	}
	model.cascade(acceptedAt)
	return model
}

func naiveNextWorkday(calendar Calendar, day int) int {
	for next := day + 1; ; next++ {
		if calendar.IsWorkday(next) {
			return next
		}
	}
}

func naiveNth(calendar Calendar, after, count int) int {
	day := after
	for count > 0 {
		day++
		if calendar.IsWorkday(day) {
			count--
		}
	}
	return day
}

func naiveWorkdays(calendar Calendar, start, end int) int {
	count := 0
	for day := start; day <= end; day++ {
		if calendar.IsWorkday(day) {
			count++
		}
	}
	return count
}

func copyInt(value *int) *int {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func (m *naiveModel) clone() *naiveModel {
	copied := *m
	copied.stages = map[string]*naiveStage{}
	for id, stage := range m.stages {
		stageCopy := *stage
		stageCopy.startedAt = copyInt(stage.startedAt)
		stageCopy.passedAt = copyInt(stage.passedAt)
		stageCopy.failedAt = copyInt(stage.failedAt)
		stageCopy.segmentStartAt = copyInt(stage.segmentStartAt)
		stageCopy.correctionDueAt = copyInt(stage.correctionDueAt)
		copied.stages[id] = &stageCopy
	}
	copied.finalAt = copyInt(m.finalAt)
	return &copied
}

func (m *naiveModel) due(stage *naiveStage) *int {
	if stage.segmentStartAt == nil || stage.remaining <= 0 {
		return nil
	}
	due := naiveNth(m.calendar, *stage.segmentStartAt, stage.remaining)
	return &due
}

func (m *naiveModel) pass(stage *naiveStage, day int) {
	stage.status = StagePassed
	stage.passedAt = &day
	stage.segmentStartAt = nil
	stage.correctionDueAt = nil
}

func (m *naiveModel) fail(stage *naiveStage, day int) {
	stage.status = StageFailed
	stage.failedAt = &day
	stage.segmentStartAt = nil
	stage.correctionDueAt = nil
}

func (m *naiveModel) hasFailure() bool {
	for _, stage := range m.stages {
		if stage.status == StageFailed {
			return true
		}
	}
	return false
}

func (m *naiveModel) cascade(day int) []string {
	changed := []string{}
	progress := true
	for progress {
		progress = false
		for _, def := range m.defs {
			stage := m.stages[def.ID]
			if stage.status != StageNotStarted {
				continue
			}
			if m.hasFailure() {
				stage.status = StageTerminated
				changed = append(changed, def.ID)
				progress = true
				continue
			}
			startAt := m.clock
			ready := len(def.Prerequisites) == 0
			if !ready {
				startAt, ready = 0, true
				for _, prerequisiteID := range def.Prerequisites {
					prerequisite := m.stages[prerequisiteID]
					if prerequisite.status != StagePassed || prerequisite.passedAt == nil {
						ready = false
						break
					}
					if *prerequisite.passedAt > startAt {
						startAt = *prerequisite.passedAt
					}
				}
			}
			if ready && startAt <= day {
				stage.status = StageProcessing
				stage.startedAt = &startAt
				stage.segmentStartAt = &startAt
				stage.remaining = def.TimeLimit
				changed = append(changed, def.ID)
				progress = true
			}
		}
	}
	return changed
}

func (m *naiveModel) settle(targetID string, day int) {
	pending := map[string]struct{}{targetID: {}}
	for len(pending) > 0 {
		var id string
		for id = range pending {
			break
		}
		delete(pending, id)
		stage := m.stages[id]
		if stage.status == StageCorrecting && stage.correctionDueAt != nil && day > *stage.correctionDueAt {
			m.fail(stage, naiveNextWorkday(m.calendar, *stage.correctionDueAt))
			for _, changed := range m.cascade(day) {
				pending[changed] = struct{}{}
			}
		}
		if stage.status == StageProcessing && stage.def.AutoPassOnTime {
			due := m.due(stage)
			if due != nil && day > *due && m.calendar.IsWorkday(day) {
				m.pass(stage, naiveNextWorkday(m.calendar, *due))
				for _, changed := range m.cascade(day) {
					pending[changed] = struct{}{}
				}
			}
		}
	}
	m.refreshFinal()
}

func (m *naiveModel) settleAll(day int) {
	changed := true
	for changed {
		changed = false
		for _, stage := range m.stages {
			if stage.status == StageCorrecting && stage.correctionDueAt != nil && day > *stage.correctionDueAt {
				m.fail(stage, naiveNextWorkday(m.calendar, *stage.correctionDueAt))
				m.cascade(day)
				changed = true
			}
			if stage.status == StageProcessing && stage.def.AutoPassOnTime {
				due := m.due(stage)
				if due != nil && day > *due && m.calendar.IsWorkday(day) {
					m.pass(stage, naiveNextWorkday(m.calendar, *due))
					m.cascade(day)
					changed = true
				}
			}
		}
	}
	m.refreshFinal()
}

func (m *naiveModel) refreshFinal() {
	allPassed := true
	active := 0
	hasFailure := false
	last := 0
	for _, stage := range m.stages {
		switch stage.status {
		case StagePassed:
			if stage.passedAt != nil && *stage.passedAt > last {
				last = *stage.passedAt
			}
		case StageFailed:
			hasFailure = true
			if stage.failedAt != nil && *stage.failedAt > last {
				last = *stage.failedAt
			}
		case StageProcessing, StageCorrecting:
			active++
			allPassed = false
		default:
			allPassed = false
		}
	}
	if allPassed {
		m.overall = OverallGranted
		m.finalAt = &last
	} else if hasFailure && active == 0 {
		m.overall = OverallDenied
		m.finalAt = &last
	}
}

func (m *naiveModel) prerequisitesPassed(stage *naiveStage) bool {
	for _, prerequisiteID := range stage.def.Prerequisites {
		if m.stages[prerequisiteID].status != StagePassed {
			return false
		}
	}
	return true
}

func (m *naiveModel) command(kind string, cmd StageCommand) error {
	if cmd.LicenseID == "" || cmd.StageID == "" || cmd.Actor == "" || cmd.Day <= 0 {
		return ErrInvalidArgument
	}
	if cmd.Day < m.clock {
		return ErrClockMovedBack
	}
	if cmd.LicenseID != "L" {
		return ErrNotFound
	}
	stage, ok := m.stages[cmd.StageID]
	if !ok {
		return ErrNotFound
	}
	if m.actors[cmd.Actor] != stage.def.Department {
		return ErrForbidden
	}
	if m.finalAt != nil {
		return ErrInvalidState
	}
	m.clock = cmd.Day
	m.settle(stage.def.ID, cmd.Day)
	if m.finalAt != nil || stage.status != StageProcessing {
		return ErrInvalidState
	}
	if !m.prerequisitesPassed(stage) {
		return ErrPrerequisiteNotPassed
	}
	switch kind {
	case "pass":
		due := m.due(stage)
		stage.overdue = due != nil && cmd.Day > *due && m.calendar.IsWorkday(cmd.Day)
		m.pass(stage, cmd.Day)
	case "fail":
		due := m.due(stage)
		stage.overdue = due != nil && cmd.Day > *due && m.calendar.IsWorkday(cmd.Day)
		m.fail(stage, cmd.Day)
	case "request":
		if stage.corrections >= stage.def.CorrectionLimit {
			return ErrCorrectionLimit
		}
		used := naiveWorkdays(m.calendar, naiveNextWorkday(m.calendar, *stage.segmentStartAt), cmd.Day)
		stage.remaining -= used
		if stage.remaining < 0 {
			stage.remaining = 0
		}
		stage.corrections++
		stage.status = StageCorrecting
		deadline := naiveNth(m.calendar, cmd.Day, stage.def.CorrectionDays)
		stage.correctionDueAt = &deadline
		stage.segmentStartAt = nil
	}
	m.cascade(cmd.Day)
	m.refreshFinal()
	return nil
}

func (m *naiveModel) submit(cmd StageCommand) error {
	if cmd.LicenseID == "" || cmd.StageID == "" || cmd.Actor == "" || cmd.Day <= 0 {
		return ErrInvalidArgument
	}
	if cmd.Day < m.clock {
		return ErrClockMovedBack
	}
	stage, ok := m.stages[cmd.StageID]
	if !ok {
		return ErrNotFound
	}
	if m.actors[cmd.Actor] != stage.def.Department {
		return ErrForbidden
	}
	m.clock = cmd.Day
	m.settle(stage.def.ID, cmd.Day)
	if m.finalAt != nil || stage.status != StageCorrecting || cmd.Day > *stage.correctionDueAt {
		return ErrInvalidState
	}
	stage.status = StageProcessing
	stage.segmentStartAt = &cmd.Day
	stage.correctionDueAt = nil
	m.cascade(cmd.Day)
	m.refreshFinal()
	return nil
}

func (m *naiveModel) viewAt(stageID string, day int) StageView {
	projection := m.clone()
	projection.settleAll(day)
	stage := projection.stages[stageID]
	view := StageView{
		ID: stageID, Department: stage.def.Department, Status: stage.status, Overdue: stage.overdue,
		StartedAt: stage.startedAt, PassedAt: stage.passedAt, FailedAt: stage.failedAt,
		CorrectionDeadline: stage.correctionDueAt,
	}
	if stage.status == StageProcessing || stage.status == StageCorrecting {
		if stage.status == StageCorrecting {
			remaining := stage.remaining
			view.RemainingWorkdays = &remaining
		}
		due := projection.due(stage)
		if due != nil {
			switch {
			case day < *due:
				remaining := naiveWorkdays(projection.calendar, day, *due)
				view.RemainingWorkdays = &remaining
			case day == *due:
				remaining := 1
				view.RemainingWorkdays = &remaining
			default:
				remaining := 0
				view.RemainingWorkdays = &remaining
				if projection.calendar.IsWorkday(day) {
					view.Overdue = true
				}
			}
		}
	}
	return view
}
