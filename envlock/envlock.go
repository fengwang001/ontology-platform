package envlock

type GrantDecision int

const (
	GrantReject GrantDecision = iota
	GrantAcquire
)

type Locker struct {
	running int64
	queue   []int64
}

func NewLocker() *Locker {
	return &Locker{running: 0}
}

func (l *Locker) Enqueue(id int64) {
	l.queue = append(l.queue, id)
}

func (l *Locker) Remove(id int64) bool {
	for index, queued := range l.queue {
		if queued == id {
			l.queue = append(l.queue[:index], l.queue[index+1:]...)
			return true
		}
	}
	return false
}

func (l *Locker) Release() {
	l.running = 0
}

func (l *Locker) Running() bool {
	return l.running != 0
}

func (l *Locker) RunningID() int64 {
	return l.running
}

func (l *Locker) Queue() []int64 {
	queue := make([]int64, len(l.queue))
	copy(queue, l.queue)
	return queue
}

func (l *Locker) Grant(decide func(id int64) GrantDecision) {
	if l.Running() {
		return
	}
	for len(l.queue) > 0 {
		id := l.queue[0]
		l.queue = l.queue[1:]
		if decide(id) == GrantAcquire {
			l.running = id
			return
		}
	}
}
