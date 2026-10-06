package kanban

type cardState struct {
	id        string
	assignee  string
	column    int
	version   int64
	expedited bool
}

type board struct {
	config          BoardConfig
	cards           map[string]*cardState
	columnOccupancy []int
	assigneeWIP     map[string]int
	expeditedCardID string
	dependencies    *dependencyGraph
	lastNow         int64
}

func newBoard(config BoardConfig) *board {
	b := &board{
		config:          config,
		cards:           make(map[string]*cardState),
		columnOccupancy: make([]int, len(config.Columns)),
		assigneeWIP:     make(map[string]int),
		dependencies:    newDependencyGraph(),
	}
	return b
}

func (b *board) todoColumn() int { return 0 }

func (b *board) doneColumn() int { return len(b.config.Columns) - 1 }

func (b *board) isProgress(column int) bool {
	return column > b.todoColumn() && column < b.doneColumn()
}

func (b *board) addCard(id, assignee string) {
	card := &cardState{
		id:       id,
		assignee: assignee,
		column:   b.todoColumn(),
		version:  1,
	}
	b.cards[id] = card
	b.dependencies.addCard(id)
	b.columnOccupancy[b.todoColumn()]++
}

func (b *board) placeCard(card *cardState, to int, expedited bool) {
	from := card.column
	if b.isProgress(from) {
		b.assigneeWIP[card.assignee]--
	}
	b.columnOccupancy[from]--

	card.column = to
	card.expedited = expedited
	b.columnOccupancy[to]++
	if b.isProgress(to) {
		b.assigneeWIP[card.assignee]++

		if expedited {
			b.expeditedCardID = card.id
		}
	} else if card.id == b.expeditedCardID {
		b.expeditedCardID = ""
	}
}

func (b *board) changeAssignee(card *cardState, assignee string) {
	if b.isProgress(card.column) {
		b.assigneeWIP[card.assignee]--
		b.assigneeWIP[assignee]++
	}
	card.assignee = assignee
}

func (b *board) snapshot(card *cardState) Card {
	return Card{
		ID:        card.id,
		Assignee:  card.assignee,
		Column:    card.column,
		Version:   card.version,
		Expedited: card.expedited,
	}
}
