package lexer

func (m *Machine) fromFS(b byte, pos int) bool  { return false }
func (m *Machine) fromU(b byte, pos int) bool   { return false }
func (m *Machine) fromQ(b byte, pos int) bool   { return false }
func (m *Machine) fromQS(b byte, pos int) bool  { return false }
func (m *Machine) fromCR(b byte, pos int) bool  { return false }
func (m *Machine) beginField(pos int, quoted bool) {}
func (m *Machine) endField(pos int, rec bool) bool { return false }
func (m *Machine) appendByte(b byte, pos int) bool { return false }
