package kanban

type Column struct {
	Name  string
	Limit int
}

type BoardConfig struct {
	Columns       []Column
	AssigneeLimit int
}

type Card struct {
	ID        string
	Assignee  string
	Column    int
	Version   int64
	Expedited bool
}

type MoveRequest struct {
	BoardID       string
	User          string
	Card          string
	To            int
	ExpectVersion int64
	Expedite      bool
	Now           int64
}

type ReopenRequest struct {
	BoardID       string
	User          string
	Card          string
	ExpectVersion int64
	Expedite      bool
	Now           int64
}

type CreateCardRequest struct {
	BoardID  string
	User     string
	Card     string
	Assignee string
	Now      int64
}

type AssigneeRequest struct {
	BoardID       string
	User          string
	Card          string
	Assignee      string
	ExpectVersion int64
	Now           int64
}

type DependencyRequest struct {
	BoardID       string
	User          string
	Card          string
	Prerequisite  string
	ExpectVersion int64
	Now           int64
}

type ColumnLimitRequest struct {
	BoardID string
	User    string
	Column  int
	Limit   int
	Now     int64
}

type MoveResult struct {
	Card   Card
	Reason string
}

type CardResult struct {
	Card   Card
	Reason string
}

type BoardSnapshot struct {
	Config          BoardConfig
	Cards           map[string]Card
	ColumnOccupancy []int
	AssigneeWIP     map[string]int
	ExpeditedCardID string
	LastNow         int64
}

type Decision struct {
	Operation string
	BoardID   string
	Input     any
	Output    any
	Err       error
	Reason    string
}

type Logger interface {
	Log(Decision)
}

type LoggerFunc func(Decision)

func (f LoggerFunc) Log(decision Decision) { f(decision) }
