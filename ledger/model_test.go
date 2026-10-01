package ledger

// naiveModel 是按题目规则直接写成的逐步朴素模拟，用于与 Ledger 对照。
// 每个操作结束后与 Ledger 的可观测状态应当完全一致。
type naiveModel struct {
	prefetch int

	nextSeq int
	maxTag  int

	messages map[int]string // 入队序号 -> 消息
	state    map[int]string // 入队序号 -> "queued" | "unacked" | "settled"
	redeliv  map[int]bool   // 入队序号 -> 是否曾投递过（决定下次重投标志）

	queue   []int       // 队列中的入队序号，始终保持升序
	unacked map[int]int // 投递标签 -> 入队序号
	dropped int
}

func newNaiveModel(prefetch int) *naiveModel {
	return &naiveModel{
		prefetch: prefetch,
		messages: map[int]string{},
		state:    map[int]string{},
		redeliv:  map[int]bool{},
		unacked:  map[int]int{},
	}
}

func (m *naiveModel) enqueue(msg string) int {
	m.nextSeq++
	seq := m.nextSeq
	m.messages[seq] = msg
	m.state[seq] = "queued"
	m.queue = insertSorted(m.queue, seq)
	return seq
}

type naiveDelivery struct {
	tag       int
	seq       int
	message   string
	redeliver bool
}

func (m *naiveModel) deliver() (naiveDelivery, ErrReason, bool) {
	if len(m.unacked) >= m.prefetch {
		return naiveDelivery{}, ErrPrefetchFull, false
	}
	if len(m.queue) == 0 {
		return naiveDelivery{}, ErrQueueEmpty, false
	}
	seq := m.queue[0]
	m.queue = m.queue[1:]
	m.state[seq] = "unacked"
	m.maxTag++
	tag := m.maxTag
	m.unacked[tag] = seq
	d := naiveDelivery{
		tag:       tag,
		seq:       seq,
		message:   m.messages[seq],
		redeliver: m.redeliv[seq],
	}
	m.redeliv[seq] = true
	return d, 0, true
}

func (m *naiveModel) collect(tag int, multiple bool) ([]int, ErrReason, bool) {
	if tag == 0 || tag > m.maxTag {
		return nil, ErrTagIllegal, false
	}
	if !multiple {
		if _, ok := m.unacked[tag]; !ok {
			return nil, ErrAlreadySettled, false
		}
		return []int{tag}, 0, true
	}
	var tags []int
	for t := range m.unacked {
		if t <= tag {
			tags = append(tags, t)
		}
	}
	if len(tags) == 0 {
		return nil, ErrRangeEmpty, false
	}
	sortInts(tags)
	return tags, 0, true
}

func (m *naiveModel) settle(tags []int) {
	for _, t := range tags {
		seq := m.unacked[t]
		delete(m.unacked, t)
		m.state[seq] = "settled"
	}
}

func (m *naiveModel) ack(tag int, multiple bool) (ErrReason, bool) {
	tags, reason, ok := m.collect(tag, multiple)
	if !ok {
		return reason, false
	}
	m.settle(tags)
	return 0, true
}

func (m *naiveModel) nack(tag int, multiple bool, requeue bool) (ErrReason, bool) {
	tags, reason, ok := m.collect(tag, multiple)
	if !ok {
		return reason, false
	}
	for _, t := range tags {
		seq := m.unacked[t]
		delete(m.unacked, t)
		if requeue {
			m.state[seq] = "queued"
			m.queue = insertSorted(m.queue, seq)
		} else {
			m.state[seq] = "settled"
			m.dropped++
		}
	}
	return 0, true
}

func insertSorted(s []int, v int) []int {
	i := 0
	for i < len(s) && s[i] < v {
		i++
	}
	s = append(s, 0)
	copy(s[i+1:], s[i:])
	s[i] = v
	return s
}

func sortInts(s []int) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
