package stream

type Encoding int

type Config struct {
	From, To Encoding
	Strict   bool
	KeepBOM  bool
	Limit    int
}

type Stats struct {
	Scalars     int64
	Invalid     int64
	InvalidBytes int64
	BOMBytes    int64
	Consumed    int64
	Checks      int64
}

type Transcoder struct{}

func New(c Config) *Transcoder { return &Transcoder{} }

func (t *Transcoder) Write(p []byte) (int, error) { return len(p), nil }

func (t *Transcoder) Close() error { return nil }

func (t *Transcoder) Output() []byte { return nil }

func (t *Transcoder) Stats() Stats { return Stats{} }

func (t *Transcoder) Consumed() int64 { return 0 }

func (t *Transcoder) Pending() int { return 0 }
