package termination

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
)

var (
	ErrInvalidRing       = errors.New("ring must contain at least one process")
	ErrInvalidProcess    = errors.New("invalid process id")
	ErrIdleSender        = errors.New("idle process cannot send")
	ErrSelfMessage       = errors.New("process cannot send to itself")
	ErrMessageNotFound   = errors.New("injected message does not exist")
	ErrAlreadyDelivered  = errors.New("injected message was already delivered")
	ErrWrongDestination  = errors.New("injected message is addressed to another process")
	ErrNotTokenHolder    = errors.New("caller is not the token holder")
	ErrActiveTokenHolder = errors.New("active token holder cannot pass the token")
	ErrTerminated        = errors.New("computation has already terminated")
)

type State bool

const (
	Idle   State = false
	Active State = true
)

type Color bool

const (
	White Color = false
	Black Color = true
)

type Message struct {
	ID   uint64
	From int
	To   int
}

type Announcement struct {
	Round uint64
}

type Logger interface {
	Printf(format string, args ...any)
}

type ProcessSnapshot struct {
	State   State
	Color   Color
	Counter int64
}

type Snapshot struct {
	Round            uint64
	Processes        []ProcessSnapshot
	TokenHolder      int
	TokenColor       Color
	TokenAccumulator int64
	BaselineComplete bool
	PendingMessages  int
	Announced        bool
}

type Option func(*config)

type config struct {
	logger  Logger
	actives map[int]bool
}

func WithLogger(logger Logger) Option {
	return func(c *config) {
		c.logger = logger
	}
}

func WithActiveProcesses(processes ...int) Option {
	return func(c *config) {
		for _, process := range processes {
			c.actives[process] = true
		}
	}
}

type Detector struct {
	mu              sync.Mutex
	n               int
	logger          Logger
	states          []State
	colors          []Color
	counters        []int64
	messages        map[uint64]Message
	delivered       map[uint64]Message
	nextID          uint64
	holder          int
	tokenColor      Color
	tokenCount      int64
	round           uint64
	launched        bool
	circuitComplete bool
	announced       bool
}

func New(n int, options ...Option) (*Detector, error) {
	if n < 1 {
		return nil, ErrInvalidRing
	}

	cfg := config{actives: make(map[int]bool)}
	for _, option := range options {
		option(&cfg)
	}
	for process := range cfg.actives {
		if !valid(n, process) {
			return nil, fmt.Errorf("%w: active=%d", ErrInvalidProcess, process)
		}
	}
	logger := cfg.logger
	if logger == nil {
		logger = log.New(os.Stdout, "termination: ", log.LstdFlags|log.Lmicroseconds)
	}

	detector := &Detector{
		n:         n,
		logger:    logger,
		states:    make([]State, n),
		colors:    make([]Color, n),
		counters:  make([]int64, n),
		messages:  make(map[uint64]Message),
		delivered: make(map[uint64]Message),
		holder:    0,
	}
	for process := range cfg.actives {
		detector.states[process] = Active
	}
	return detector, nil
}

func (d *Detector) Send(from, to int) (Message, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.checkMutable(); err != nil {
		d.reject("send", fmt.Sprintf("from=%d to=%d", from, to), err)
		return Message{}, err
	}
	if !valid(d.n, from) || !valid(d.n, to) {
		err := fmt.Errorf("%w: from=%d to=%d", ErrInvalidProcess, from, to)
		d.reject("send", fmt.Sprintf("from=%d to=%d", from, to), err)
		return Message{}, err
	}
	if from == to {
		err := fmt.Errorf("%w: process=%d", ErrSelfMessage, from)
		d.reject("send", fmt.Sprintf("from=%d to=%d", from, to), err)
		return Message{}, err
	}
	if d.states[from] == Idle {
		err := fmt.Errorf("%w: process=%d", ErrIdleSender, from)
		d.reject("send", fmt.Sprintf("from=%d to=%d", from, to), err)
		return Message{}, err
	}

	d.nextID++
	message := Message{ID: d.nextID, From: from, To: to}
	d.messages[message.ID] = message
	d.counters[from]++
	d.logger.Printf("input send from=%d to=%d -> output message=%d; reason=counter[%d]=%d,pending=%d", from, to, message.ID, from, d.counters[from], len(d.messages))
	return message, nil
}

func (d *Detector) Deliver(message Message, to int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	stored, pending := d.messages[message.ID]
	previous, wasDelivered := d.delivered[message.ID]
	if !pending {
		var err error
		if wasDelivered {
			err = fmt.Errorf("%w: message=%+v delivered=%+v", ErrAlreadyDelivered, message, previous)
		} else {
			err = fmt.Errorf("%w: message=%+v", ErrMessageNotFound, message)
		}
		d.reject("deliver", fmt.Sprintf("message=%+v to=%d", message, to), err)
		return err
	}
	if stored != message {
		err := fmt.Errorf("%w: message=%+v", ErrMessageNotFound, message)
		d.reject("deliver", fmt.Sprintf("message=%+v to=%d", message, to), err)
		return err
	}
	if !valid(d.n, to) {
		err := fmt.Errorf("%w: destination=%d", ErrInvalidProcess, to)
		d.reject("deliver", fmt.Sprintf("message=%+v to=%d", message, to), err)
		return err
	}
	if stored.To != to {
		err := fmt.Errorf("%w: message=%d destination=%d recipient=%d", ErrWrongDestination, message.ID, stored.To, to)
		d.reject("deliver", fmt.Sprintf("message=%+v to=%d", message, to), err)
		return err
	}
	if err := d.checkMutable(); err != nil {
		d.reject("deliver", fmt.Sprintf("message=%+v to=%d", message, to), err)
		return err
	}

	delete(d.messages, message.ID)
	d.delivered[message.ID] = stored
	d.counters[stored.From]--
	d.states[to] = Active
	d.colors[to] = Black
	d.logger.Printf("input deliver message=%d from=%d to=%d -> output accepted; reason=counter[%d]=%d,pending=%d,process[%d]=active/black", message.ID, stored.From, to, stored.From, d.counters[stored.From], len(d.messages), to)
	return nil
}

func (d *Detector) BecomeIdle(process int) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !valid(d.n, process) {
		err := fmt.Errorf("%w: process=%d", ErrInvalidProcess, process)
		d.reject("become idle", fmt.Sprintf("process=%d", process), err)
		return err
	}
	if err := d.checkMutable(); err != nil {
		d.reject("become idle", fmt.Sprintf("process=%d", process), err)
		return err
	}
	if d.states[process] == Idle {
		d.logger.Printf("input become-idle process=%d -> output unchanged; reason=already idle,round=%d", process, d.round)
		return nil
	}

	d.states[process] = Idle
	d.logger.Printf("input become-idle process=%d -> output idle; reason=round=%d,pending=%d", process, d.round, len(d.messages))
	return nil
}

func (d *Detector) PassToken(holder int) (Announcement, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !valid(d.n, holder) {
		err := fmt.Errorf("%w: process=%d", ErrInvalidProcess, holder)
		d.reject("pass token", fmt.Sprintf("holder=%d", holder), err)
		return Announcement{}, false, err
	}
	if d.holder != holder {
		err := fmt.Errorf("%w: caller=%d holder=%d", ErrNotTokenHolder, holder, d.holder)
		d.reject("pass token", fmt.Sprintf("holder=%d", holder), err)
		return Announcement{}, false, err
	}
	if d.states[holder] == Active {
		err := fmt.Errorf("%w: process=%d", ErrActiveTokenHolder, holder)
		d.reject("pass token", fmt.Sprintf("holder=%d", holder), err)
		return Announcement{}, false, err
	}
	if err := d.checkMutable(); err != nil {
		d.reject("pass token", fmt.Sprintf("holder=%d", holder), err)
		return Announcement{}, false, err
	}

	if holder != 0 {
		d.tokenCount += d.counters[holder]
		if d.colors[holder] == Black {
			d.tokenColor = Black
		}
		d.colors[holder] = White
		d.holder = predecessor(holder, d.n)
		d.logger.Printf("input pass-token holder=%d -> output holder=%d; reason=accumulator=%d,token=%s,process whitened", holder, d.holder, d.tokenCount, colorName(d.tokenColor))
		return Announcement{}, false, nil
	}

	if !d.launched {
		d.launchRound(1)
		if d.n == 1 {
			d.circuitComplete = true
			return d.checkReturnedToken()
		}
		d.holder = predecessor(0, d.n)
		d.logger.Printf("input pass-token holder=0 -> output holder=%d; reason=round=%d started,token=white/accumulator=0", d.holder, d.round)
		return Announcement{}, false, nil
	}

	return d.checkReturnedToken()
}

func (d *Detector) Snapshot() Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()

	snapshot := Snapshot{
		Round:            d.round,
		Processes:        make([]ProcessSnapshot, d.n),
		TokenHolder:      d.holder,
		TokenColor:       d.tokenColor,
		TokenAccumulator: d.tokenCount,
		BaselineComplete: d.circuitComplete,
		PendingMessages:  len(d.messages),
		Announced:        d.announced,
	}
	for i := range d.states {
		snapshot.Processes[i] = ProcessSnapshot{State: d.states[i], Color: d.colors[i], Counter: d.counters[i]}
	}
	return snapshot
}

func (d *Detector) checkReturnedToken() (Announcement, bool, error) {
	total := d.tokenCount + d.counters[0]
	terminated := d.circuitComplete && d.colors[0] == White && d.tokenColor == White && total == 0
	d.logger.Printf("input pass-token holder=0 -> output check; reason=round=%d,initiator=idle/%s,token=%s,accumulator=%d,counter[0]=%d,total=%d,pending=%d", d.round, colorName(d.colors[0]), colorName(d.tokenColor), d.tokenCount, d.counters[0], total, len(d.messages))
	if !terminated {
		previousRound := d.round
		d.launchRound(previousRound + 1)
		d.circuitComplete = true
		d.holder = predecessor(0, d.n)
		d.logger.Printf("input pass-token holder=0 -> output holder=%d; reason=round=%d inconclusive,new round=%d started", d.holder, previousRound, d.round)
		return Announcement{}, false, nil
	}

	d.announced = true
	announcement := Announcement{Round: d.round}
	d.logger.Printf("input pass-token holder=0 -> output announced round=%d; reason=all checks passed,pending=%d", announcement.Round, len(d.messages))
	return announcement, true, nil
}

func (d *Detector) launchRound(round uint64) {
	d.round = round
	d.launched = true
	d.circuitComplete = false
	d.tokenColor = White
	d.tokenCount = 0
	d.colors[0] = White
}

func (d *Detector) checkMutable() error {
	if d.announced {
		return ErrTerminated
	}
	return nil
}

func (d *Detector) reject(operation, input string, err error) {
	d.logger.Printf("input %s %s -> output rejected; reason=%v", operation, input, err)
}

func valid(n, process int) bool {
	return process >= 0 && process < n
}

func predecessor(process, n int) int {
	if process == 0 {
		return n - 1
	}
	return process - 1
}

func colorName(color Color) string {
	if color == Black {
		return "black"
	}
	return "white"
}

func (state State) String() string {
	if state == Active {
		return "active"
	}
	return "idle"
}

func (color Color) String() string {
	return colorName(color)
}
