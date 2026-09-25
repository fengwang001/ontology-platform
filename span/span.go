package span

type Map struct{}

func (m *Map) Add(orig, out int, deleted bool) {}

func (m *Map) TrimRetainedSuffix(n int) bool { return false }

func (m *Map) ToOrig(o int) int { return 0 }

func (m *Map) ToOut(i int) int { return 0 }

func (m *Map) Segments() int { return 0 }

func (m *Map) LastCheck() int { return 0 }

func Shift(m *Map, origDelta, outDelta int) *Map { return m }

func Join(parts ...*Map) *Map { return &Map{} }
