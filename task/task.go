package task

type Task struct {
	ID string
	C  int
	T  int
}

func (t Task) Valid() bool {
	return t.ID != "" && t.C > 0 && t.T > 0 && t.C <= t.T
}
