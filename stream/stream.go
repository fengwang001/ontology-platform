package stream

type Encoding int

const (
	UTF8 Encoding = iota
	UTF16LE
	UTF16BE
)

type Config struct {
	From       Encoding
	To         Encoding
	Strict     bool
	MaxOutput  int
	KeepInputBOM bool
	EmitOutputBOM bool
}

type Stats struct {
	Valid         int
	Invalid       int
	InvalidBytes  int
	BOMBytes      int
	Consumed      int
	InputBytes    int
	Checks        int
}

type Transcoder struct{}

func New(cfg Config) *Transcoder {
	return &Transcoder{}
}

func (t *Transcoder) Write(p []byte) (int, error) {
	return 0, nil
}

func (t *Transcoder) Close() error { return nil }

func (t *Transcoder) Output() []byte { return nil }

func (t *Transcoder) Stats() Stats { return Stats{} }

func (t *Transcoder) Pending() int { return 0 }
