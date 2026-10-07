package room

// matchPlayer 为对局名单成员。中途退出者保留在名单中，
// 但不再在室、取消上报资格，仍可被上报为胜者。
type matchPlayer struct {
	id       string
	inRoom   bool
	reported bool
	winner   string
}

// match 为对局名单与上报计票。
type match struct {
	order   []string // 开局时在室玩家，按加入次序
	players map[string]*matchPlayer
	inRoom  int64 // 仍在室的对局玩家数
	reports int64 // 已上报人数
	work    *int64
}

func newMatch(work *int64) *match {
	return &match{players: make(map[string]*matchPlayer), work: work}
}

// begin 以当前在室成员（按加入次序）建立对局名单。
func (m *match) begin(ordered []*member) {
	m.order = m.order[:0]
	for _, mem := range ordered {
		*m.work++
		m.order = append(m.order, mem.id)
		m.players[mem.id] = &matchPlayer{id: mem.id, inRoom: true}
	}
	m.inRoom = int64(len(m.order))
	m.reports = 0
}

// quit 把玩家标记为中途退出：保留在名单中，取消上报资格。
func (m *match) quit(id string) {
	p := m.players[id]
	if p != nil && p.inRoom {
		p.inRoom = false
		m.inRoom--
	}
}

// record 记录（或改报）一次上报，返回是否为首次上报。
func (m *match) record(id, winner string) bool {
	p := m.players[id]
	first := !p.reported
	if first {
		p.reported = true
		m.reports++
	}
	p.winner = winner
	return first
}

// tally 统计在室已上报者的票型。
// 返回是否全体一致、一致胜者（有上报时有效）与上报人数。
// 扫描上界为 U<=20，不随历史事件数增长。
func (m *match) tally() (unanimous bool, winner string, count int64) {
	unanimous = true
	first := true
	for _, id := range m.order {
		*m.work++
		p := m.players[id]
		if !p.inRoom || !p.reported {
			continue
		}
		count++
		if first {
			winner = p.winner
			first = false
		} else if p.winner != winner {
			unanimous = false
		}
	}
	return unanimous, winner, count
}
