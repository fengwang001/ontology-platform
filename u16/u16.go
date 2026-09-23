package u16

type Endian uint8

const (
	LittleEndian Endian = iota
	BigEndian
)

type Event struct {
	R       rune
	Size    int
	Invalid bool
}

type Decoder struct{}

func NewDecoder(e Endian) *Decoder { return &Decoder{} }

func (d *Decoder) Feed(b byte) (Event, bool) { return Event{}, false }

func (d *Decoder) PendingLen() int { return 0 }

func Encode(r rune, e Endian) []byte { return nil }
