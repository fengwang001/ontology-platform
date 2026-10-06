package alarm

import (
	"bytes"
	"math/rand"
	"reflect"
	"testing"
)

func TestRandomizedComparisonWithNaiveModel(t *testing.T) {
	cfg := Config{
		HighManualDuration: 8,
		LowManualDuration:  4,
		ChatterWindow:      10,
		ChatterCount:       3,
		ChatterDuration:    5,
	}
	configs := []PointConfig{
		{ID: "E", Priority: Emergency},
		{ID: "H", Priority: High, Conditions: []string{"startup"}},
		{ID: "L", Priority: Low, Conditions: []string{"maintenance"}},
	}
	var logs bytes.Buffer
	service, err := NewService(cfg, configs)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	service.WithLogger(&logs)
	model := newNaiveModel(cfg, configs)
	random := rand.New(rand.NewSource(20261006))
	var clock int64

	for iteration := 0; iteration < 5000; iteration++ {
		clock += int64(random.Intn(4))
		event := randomEvent(random, clock, iteration)
		_, modelKind, _ := model.apply(event)
		var serviceError error
		var serviceResult Result
		if event.name == "condition" {
			serviceError = service.SetCondition(ConditionUpdate{
				At:        event.at,
				Condition: event.condition,
				Active:    event.active,
			})
			if serviceError == nil {
				model.clock = event.at
			}
		} else {
			op := Operation{
				At:       event.at,
				PointID:  event.point,
				Role:     event.role,
				Duration: event.duration,
				Reason:   event.reason,
				Ticket:   event.ticket,
			}
			serviceResult, serviceError = callEvent(service, event.name, op)
		}

		if modelKind == 255 {
			if serviceError != nil {
				t.Fatalf("iteration %d event %+v unexpected service error %v\nlogs:\n%s", iteration, event, serviceError, logs.String())
			}
		} else {
			if serviceError == nil {
				t.Fatalf("iteration %d event %+v expected error kind %d\nlogs:\n%s", iteration, event, modelKind, logs.String())
			}
			alarmError, ok := serviceError.(*Error)
			if !ok || alarmError.Kind != modelKind {
				t.Fatalf("iteration %d event %+v error mismatch model=%d service=%v\nlogs:\n%s", iteration, event, modelKind, serviceError, logs.String())
			}
			continue
		}

		serviceList, err := service.ActiveAlarms(clock)
		if err != nil {
			t.Fatalf("iteration %d ActiveAlarms() error = %v", iteration, err)
		}
		model.query(clock)
		modelList := model.list()
		if !reflect.DeepEqual(serviceList, modelList) {
			t.Fatalf("iteration %d list mismatch\nservice=%+v\nnaive=%+v\nL=%+v\nevent=%+v\nlogs:\n%s",
				iteration, serviceList, modelList, model.points["L"], event, logs.String())
		}
		if random.Intn(5) == 0 {
			duration := int64(1 + random.Intn(20))
			got, err := service.AlarmRate(clock, duration)
			if err != nil {
				t.Fatalf("iteration %d AlarmRate() error = %v", iteration, err)
			}
			model.query(clock)
			want := model.rate(clock, duration)
			if got != want {
				start := len(logs.Bytes()) - 5000
				if start < 0 {
					start = 0
				}
				t.Fatalf("iteration %d rate at %d/%d = %d, want %d, event %+v, result=%+v, L=%+v, H=%+v, expiries=%v, shown=%v, naive appearances=%v\nrecent logs:\n%s", iteration, clock, duration, got, want, event, serviceResult, *model.points["L"], *model.points["H"], model.expiries, model.shown, model.appearances, logs.Bytes()[start:])
			}
		}
	}
}

func randomEvent(random *rand.Rand, clock int64, iteration int) naiveEvent {
	points := []string{"E", "H", "L", "missing"}
	names := []string{"trigger", "return", "acknowledge", "suppress", "release", "disable", "enable", "condition"}
	name := names[random.Intn(len(names))]
	if iteration < 30 {
		name = names[random.Intn(4)]
	}
	role := Operator
	if random.Intn(3) == 0 {
		role = Engineer
	}
	event := naiveEvent{
		name:     name,
		at:       clock,
		point:    points[random.Intn(len(points))],
		role:     role,
		duration: int64(1 + random.Intn(12)),
		reason:   "random reason",
		ticket:   "CHG",
	}
	if name == "condition" {
		event.point = ""
		if random.Intn(8) == 0 {
			event.condition = "unknown"
		} else {
			event.condition = []string{"startup", "maintenance"}[random.Intn(2)]
		}
		event.active = random.Intn(2) == 0
	}
	if random.Intn(15) == 0 {
		event.invalid = true
		event.at = -1
	}
	return event
}

func callEvent(service *Service, name string, op Operation) (Result, error) {
	switch name {
	case "trigger":
		return service.Trigger(op)
	case "return":
		return service.ReturnToNormal(op)
	case "acknowledge":
		return service.Acknowledge(op)
	case "suppress":
		return service.Suppress(op)
	case "release":
		return service.ReleaseSuppression(op)
	case "disable":
		return service.Disable(op)
	case "enable":
		return service.Enable(op)
	default:
		return Result{}, newError(InvalidArgument, name, "unknown event")
	}
}
