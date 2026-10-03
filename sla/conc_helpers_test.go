package sla

import "ontology/cal"

func newConCal() *cal.Calendar { return cal.New(540, 1080, 0b0011111) }

func newConMgr(c *cal.Calendar) *Manager { return NewManager(c) }

func conID(g int) []byte { return []byte{byte('a' + g)} }

func startCon(m *Manager, g int, now int64) error {
	return m.Start(conID(g), 100000, now)
}

func pauseCon(m *Manager, g int, now int64) error { return m.Pause(conID(g), now) }

func resumeCon(m *Manager, g int, now int64) error { return m.Resume(conID(g), now) }

func elapsedCon(m *Manager, g int, now int64) (int64, error) {
	return m.Elapsed(conID(g), now)
}

func addConHol(c *cal.Calendar, day, now int64) error {
	return c.AddHoliday(day, now)
}
