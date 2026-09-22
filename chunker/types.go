package chunker

import "time"

type Clock func() time.Time

type Extension struct {
	Key   string
	Value string
}

type Item struct {
	Payload    []byte
	Extensions []Extension
}

type Config struct {
	MinSize  int
	MaxSize  int
	Window   time.Duration
	LineCost func(payloadLen int, extensionLen int) int
	Clock    Clock
}

type Chunker struct{}

type Snapshot struct{}
