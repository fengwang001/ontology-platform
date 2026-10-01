package eventbus

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
)

type scenario struct {
	name     string
	queue    int
	actions  []action
	handlers map[string]handlerSpec
}

type action struct {
	op       string
	topic    string
	id       string
	priority int
	nested   []action
}

type handlerSpec struct {
	panic_ bool
	stop   bool
}

type output struct {
	sequence   uint64
	dispatched uint64
	err        string
	calls      []string
	errors     []string
}

func TestEventBusMatchesNaiveSimulation(t *testing.T) {
	scenarios := []scenario{
		{
			name:  "breadth-first snapshot priority unsubscribe and dynamic subscription",
			queue: 8,
			actions: []action{
				{op: "subscribe", topic: "root", id: "low", priority: 1},
				{
					op:       "subscribe",
					topic:    "root",
					id:       "high",
					priority: 10,
					nested: []action{
						{op: "unsubscribe", topic: "root", id: "middle"},
						{op: "subscribe", topic: "root", id: "new", priority: 50},
						{op: "publish", topic: "child"},
					},
				},
				{op: "subscribe", topic: "root", id: "middle", priority: 5},
				{op: "subscribe", topic: "root", id: "late", priority: 99},
				{op: "subscribe", topic: "child", id: "existing", priority: 1},
				{op: "unsubscribe", topic: "root", id: "late"},
				{op: "subscribe", topic: "root", id: "removed", priority: 20},
				{op: "unsubscribe", topic: "root", id: "removed"},
				{op: "publish", topic: "root"},
				{op: "publish", topic: "root"},
			},
			handlers: map[string]handlerSpec{
				"low": {}, "high": {}, "middle": {}, "late": {},
				"existing": {}, "removed": {}, "new": {},
			},
		},
		{
			name:  "stop propagation skips later handlers but not queued events",
			queue: 4,
			actions: []action{
				{
					op:       "subscribe",
					topic:    "topic",
					id:       "stopper",
					priority: 10,
					nested:   []action{{op: "publish", topic: "other"}},
				},
				{op: "subscribe", topic: "topic", id: "after", priority: 1},
				{op: "subscribe", topic: "topic", id: "queued", priority: 2},
				{op: "subscribe", topic: "other", id: "other-handler", priority: 1},
				{op: "publish", topic: "topic"},
			},
			handlers: map[string]handlerSpec{
				"stopper":       {stop: true},
				"after":         {},
				"queued":        {},
				"other-handler": {},
			},
		},
		{
			name:  "panics are isolated and recorded",
			queue: 4,
			actions: []action{
				{op: "subscribe", topic: "topic", id: "bad", priority: 10},
				{op: "subscribe", topic: "topic", id: "good", priority: 5},
				{op: "publish", topic: "topic"},
				{op: "publish", topic: "topic"},
			},
			handlers: map[string]handlerSpec{
				"bad":  {panic_: true},
				"good": {},
			},
		},
		{
			name:  "same priority follows subscription order",
			queue: 4,
			actions: []action{
				{op: "subscribe", topic: "topic", id: "first", priority: 3},
				{op: "subscribe", topic: "topic", id: "second", priority: 3},
				{op: "subscribe", topic: "topic", id: "third", priority: 3},
				{op: "publish", topic: "topic"},
			},
			handlers: map[string]handlerSpec{
				"first": {}, "second": {}, "third": {},
			},
		},
		{
			name:  "queue exactly full and invalid operations are rejected atomically",
			queue: 1,
			actions: []action{
				{op: "subscribe", topic: "root", id: "two", priority: 2},
				{
					op:       "subscribe",
					topic:    "root",
					id:       "one",
					priority: 1,
					nested: []action{
						{op: "publish", topic: "child"},
						{op: "publish", topic: "overflow"},
					},
				},
				{op: "subscribe", topic: "child", id: "child-handler"},
				{op: "publish", topic: "root"},
				{op: "subscribe", topic: "root", id: "two", priority: 2},
				{op: "unsubscribe", topic: "root", id: "missing"},
				{op: "publish", topic: ""},
				{op: "publish", topic: "root"},
			},
			handlers: map[string]handlerSpec{
				"two": {}, "one": {}, "child-handler": {},
			},
		},
	}

	for _, testCase := range scenarios {
		t.Run(testCase.name, func(t *testing.T) {
			actual := runRealBus(testCase)
			expected := runNaiveSimulation(testCase)
			t.Logf("input=%+v", testCase)
			t.Logf("actual=%+v basis=global sequence, FIFO pending queue, snapshot at each event start", actual)
			t.Logf("expected=%+v basis=independent step-by-step naive simulation", expected)
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("actual and naive simulation differ\nactual:  %+v\nexpected: %+v", actual, expected)
			}
		})
	}
}

func TestStopPropagationOutsideDispatch(t *testing.T) {
	bus := New(4)
	err := bus.StopPropagation()
	t.Logf("input=StopPropagation outside dispatch output=%q basis=no current dispatcher or event", errString(err))
	if !errors.Is(err, ErrNoDispatch) {
		t.Fatalf("expected ErrNoDispatch, got %v", err)
	}
}

func TestConcurrentSubscriptionPublishAndUnsubscribe(t *testing.T) {
	bus := New(64)
	var waiter sync.WaitGroup
	for index := 0; index < 16; index++ {
		id := fmt.Sprintf("handler-%02d", index)
		if err := bus.Subscribe("concurrent", id, index, func(Event) {}); err != nil {
			t.Fatalf("subscribe %s: %v", id, err)
		}
	}

	for index := 0; index < 12; index++ {
		waiter.Add(1)
		go func(number int) {
			defer waiter.Done()
			if _, _, err := bus.Publish("concurrent", number); err != nil {
				t.Errorf("publish %d: %v", number, err)
			}
		}(index)
	}
	waiter.Wait()

	if err := bus.Unsubscribe("concurrent", "handler-00"); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	t.Logf("input=16 subscribers and 12 concurrent publishes output=%d error records basis=one dispatcher and no handler calls under lock", len(bus.Errors()))
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func runRealBus(testCase scenario) []output {
	bus := New(testCase.queue)
	outputs := make([]output, 0)
	calls := make([]string, 0)

	var run func(action)
	run = func(act action) {
		switch act.op {
		case "subscribe":
			subscriptionAction := act
			err := bus.Subscribe(act.topic, act.id, act.priority, func(event Event) {
				calls = append(calls, fmt.Sprintf("%s:%s:%d", event.Topic, act.id, event.Sequence()))
				spec := testCase.handlers[act.id]
				if spec.stop {
					if stopErr := event.StopPropagation(); stopErr != nil {
						panic(stopErr)
					}
				}
				if spec.panic_ {
					panic("intentional handler failure")
				}
				for _, nested := range subscriptionAction.nested {
					run(nested)
				}
			})
			outputs = append(outputs, snapshotOutput(0, 0, err, calls, errorRecordStrings(bus.Errors())))
		case "unsubscribe":
			err := bus.Unsubscribe(act.topic, act.id)
			outputs = append(outputs, snapshotOutput(0, 0, err, calls, errorRecordStrings(bus.Errors())))
		case "publish":
			sequence, dispatched, err := bus.Publish(act.topic, nil)
			outputs = append(outputs, snapshotOutput(sequence, dispatched, err, calls, errorRecordStrings(bus.Errors())))
			if err != nil {
				return
			}
			for _, nested := range act.nested {
				run(nested)
			}
		}
	}

	for _, act := range testCase.actions {
		run(act)
	}
	return outputs
}

type naiveSubscription struct {
	id       string
	priority int
	order    uint64
}

type naiveEvent struct {
	topic    string
	sequence uint64
}

func runNaiveSimulation(testCase scenario) []output {
	outputs := make([]output, 0)
	subscriptions := make(map[string][]naiveSubscription)
	pending := make([]naiveEvent, 0)
	calls := make([]string, 0)
	errorRecords := make([]string, 0)
	var nextSequence uint64
	var nextOrder uint64
	dispatching := false

	snapshot := func(outputs *[]output) {
		*outputs = append(*outputs, snapshotOutput(0, 0, nil, calls, errorRecords))
	}
	active := func(topic string, order uint64) bool {
		for _, candidate := range subscriptions[topic] {
			if candidate.order == order {
				return true
			}
		}
		return false
	}
	handlersFor := func(topic string) []naiveSubscription {
		result := append([]naiveSubscription(nil), subscriptions[topic]...)
		sort.SliceStable(result, func(left int, right int) bool {
			if result[left].priority != result[right].priority {
				return result[left].priority > result[right].priority
			}
			return result[left].order < result[right].order
		})
		return result
	}

	var run func(action)
	var drain func() uint64

	run = func(act action) {
		switch act.op {
		case "subscribe":
			if act.topic == "" {
				outputs = append(outputs, snapshotOutput(0, 0, ErrEmptyTopic, calls, errorRecords))
				return
			}
			for _, candidate := range subscriptions[act.topic] {
				if candidate.id == act.id {
					outputs = append(outputs, snapshotOutput(0, 0, ErrDuplicateSubscription, calls, errorRecords))
					return
				}
			}
			nextOrder++
			subscriptions[act.topic] = append(subscriptions[act.topic], naiveSubscription{
				id:       act.id,
				priority: act.priority,
				order:    nextOrder,
			})
			snapshot(&outputs)
		case "unsubscribe":
			if act.topic == "" {
				outputs = append(outputs, snapshotOutput(0, 0, ErrEmptyTopic, calls, errorRecords))
				return
			}
			entries := subscriptions[act.topic]
			for index, candidate := range entries {
				if candidate.id == act.id {
					subscriptions[act.topic] = append(entries[:index], entries[index+1:]...)
					if len(subscriptions[act.topic]) == 0 {
						delete(subscriptions, act.topic)
					}
					snapshot(&outputs)
					return
				}
			}
			outputs = append(outputs, snapshotOutput(0, 0, ErrSubscriptionNotFound, calls, errorRecords))
		case "publish":
			if act.topic == "" {
				outputs = append(outputs, snapshotOutput(0, 0, ErrEmptyTopic, calls, errorRecords))
				return
			}
			if dispatching && len(pending) >= testCase.queue {
				outputs = append(outputs, snapshotOutput(0, 0, ErrQueueFull, calls, errorRecords))
				return
			}

			nextSequence++
			sequence := nextSequence
			pending = append(pending, naiveEvent{topic: act.topic, sequence: sequence})
			if dispatching {
				outputs = append(outputs, snapshotOutput(sequence, 0, nil, calls, errorRecords))
				return
			}

			dispatching = true
			dispatched := drain()
			dispatching = false
			outputs = append(outputs, snapshotOutput(sequence, dispatched, nil, calls, errorRecords))
		}
	}

	drain = func() uint64 {
		var dispatched uint64
		for len(pending) > 0 {
			current := pending[0]
			pending = pending[1:]
			handlers := handlersFor(current.topic)
			stopped := false

			for _, handler := range handlers {
				if stopped || !active(current.topic, handler.order) {
					continue
				}

				calls = append(calls, fmt.Sprintf("%s:%s:%d", current.topic, handler.id, current.sequence))
				spec := testCase.handlers[handler.id]
				if spec.stop {
					stopped = true
				}
				if spec.panic_ {
					errorRecords = append(errorRecords, fmt.Sprintf("%s:%d", handler.id, current.sequence))
				}

				var nested []action
				for _, act := range testCase.actions {
					if act.op == "subscribe" && act.topic == current.topic && act.id == handler.id {
						nested = act.nested
						break
					}
				}
				for _, item := range nested {
					run(item)
				}
			}
			dispatched++
		}
		return dispatched
	}

	for _, act := range testCase.actions {
		run(act)
	}
	return outputs
}

func snapshotOutput(sequence uint64, dispatched uint64, err error, calls []string, records []string) output {
	return output{
		sequence:   sequence,
		dispatched: dispatched,
		err:        errString(err),
		calls:      append([]string(nil), calls...),
		errors:     append([]string(nil), records...),
	}
}

func errorRecordStrings(records []ErrorRecord) []string {
	result := make([]string, 0, len(records))
	for _, record := range records {
		result = append(result, fmt.Sprintf("%s:%d", record.HandlerID, record.Sequence))
	}
	return result
}
