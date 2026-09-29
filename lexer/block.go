package lexer

type BK int

const (
	BStart BK = iota
	BAppend
	BEndField
	BEndRecord
)

type BEvent struct {
	Kind   BK
	Data   []byte
	Quoted bool
	Off    int
}

type Block struct {
	Events []BEvent
	End    State
	Open   bool
	Bytes  int64
}

type blockSink struct{ block *Block }

func (s blockSink) Start(off int) error {
	s.block.Events = append(s.block.Events, BEvent{Kind: BStart, Off: off})
	return nil
}
func (s blockSink) Append(data []byte) error {
	s.block.Events = append(s.block.Events, BEvent{Kind: BAppend, Data: data})
	return nil
}
func (s blockSink) EndField(off int, quoted bool) {
	s.block.Events = append(s.block.Events, BEvent{Kind: BEndField, Quoted: quoted, Off: off})
}
func (s blockSink) EndRecord(off int) {
	s.block.Events = append(s.block.Events, BEvent{Kind: BEndRecord, Off: off})
}

func ScanBlock(p []byte, start State, open bool) (Block, error) {
	m := New(Limits{})
	m.State, m.open, m.saw, m.quoted = start, open, true, open
	blk := Block{}
	err := m.run(p, 0, false, blockSink{&blk})
	blk.End, blk.Open, blk.Bytes = m.State, m.open, m.processed
	return blk, err
}
