package ontology

import "strconv"

type ID interface {
	idNode()
	String() string
}

type Event interface {
	eventNode()
	String() string
}

type idZero struct{}
type idOne struct{}
type idPair struct {
	left  ID
	right ID
}

type eventInt struct {
	value int
}

type eventPair struct {
	value int
	left  Event
	right Event
}

func (idZero) idNode() {}
func (idOne) idNode()  {}
func (idPair) idNode() {}

func (eventInt) eventNode()  {}
func (eventPair) eventNode() {}

func (idZero) String() string { return "0" }
func (idOne) String() string  { return "1" }
func (p idPair) String() string {
	return "(" + p.left.String() + "," + p.right.String() + ")"
}

func (e eventInt) String() string { return strconv.Itoa(e.value) }
func (e eventPair) String() string {
	return "(" + strconv.Itoa(e.value) + "," + e.left.String() + "," + e.right.String() + ")"
}

func normalizeID(left, right ID) ID {
	if _, ok := left.(idZero); ok {
		if _, ok := right.(idZero); ok {
			return idZero{}
		}
	}
	if _, ok := left.(idOne); ok {
		if _, ok := right.(idOne); ok {
			return idOne{}
		}
	}
	return idPair{left: left, right: right}
}

func forkID(id ID) (ID, ID) {
	switch id := id.(type) {
	case idZero:
		return idZero{}, idZero{}
	case idOne:
		return normalizeID(idOne{}, idZero{}), normalizeID(idZero{}, idOne{})
	case idPair:
		_, leftZero := id.left.(idZero)
		_, rightZero := id.right.(idZero)
		switch {
		case !leftZero && rightZero:
			leftFirst, leftSecond := forkID(id.left)
			return normalizeID(leftFirst, idZero{}), normalizeID(leftSecond, idZero{})
		case leftZero:
			rightFirst, rightSecond := forkID(id.right)
			return normalizeID(idZero{}, rightFirst), normalizeID(idZero{}, rightSecond)
		default:
			return normalizeID(id.left, idZero{}), normalizeID(idZero{}, id.right)
		}
	}
	panic("unsupported identity tree")
}

func sumID(left, right ID) (ID, bool) {
	if _, ok := left.(idZero); ok {
		return right, true
	}
	if _, ok := right.(idZero); ok {
		return left, true
	}
	leftPair, leftIsPair := left.(idPair)
	rightPair, rightIsPair := right.(idPair)
	if leftIsPair && rightIsPair {
		sumLeft, ok := sumID(leftPair.left, rightPair.left)
		if !ok {
			return nil, false
		}
		sumRight, ok := sumID(leftPair.right, rightPair.right)
		if !ok {
			return nil, false
		}
		return normalizeID(sumLeft, sumRight), true
	}
	return nil, false
}

func eventValue(event Event) int {
	switch event := event.(type) {
	case eventInt:
		return event.value
	case eventPair:
		return event.value
	}
	panic("unsupported event tree")
}

func minEvent(event Event) int {
	switch event := event.(type) {
	case eventInt:
		return event.value
	case eventPair:
		leftMin := minEvent(event.left)
		rightMin := minEvent(event.right)
		if leftMin < rightMin {
			return event.value + leftMin
		}
		return event.value + rightMin
	}
	panic("unsupported event tree")
}

func maxEvent(event Event) int {
	switch event := event.(type) {
	case eventInt:
		return event.value
	case eventPair:
		leftMax := maxEvent(event.left)
		rightMax := maxEvent(event.right)
		if leftMax > rightMax {
			return event.value + leftMax
		}
		return event.value + rightMax
	}
	panic("unsupported event tree")
}

func liftEvent(amount int, event Event) Event {
	switch event := event.(type) {
	case eventInt:
		return eventInt{value: event.value + amount}
	case eventPair:
		return eventPair{value: event.value + amount, left: event.left, right: event.right}
	}
	panic("unsupported event tree")
}

func subtractEvent(amount int, event Event) Event {
	return liftEvent(-amount, event)
}

func normalizeEvent(value int, left, right Event) Event {
	leftInt, leftIsInt := left.(eventInt)
	rightInt, rightIsInt := right.(eventInt)
	if leftIsInt && rightIsInt && leftInt.value == rightInt.value {
		return eventInt{value: value + leftInt.value}
	}
	offset := minEvent(left)
	if rightMin := minEvent(right); rightMin < offset {
		offset = rightMin
	}
	return eventPair{
		value: value + offset,
		left:  subtractEvent(offset, left),
		right: subtractEvent(offset, right),
	}
}

func fillEvent(id ID, event Event) Event {
	if _, ok := id.(idZero); ok {
		return event
	}
	if event, ok := event.(eventInt); ok {
		return event
	}
	eventNode := event.(eventPair)
	if _, ok := id.(idOne); ok {
		return eventInt{value: maxEvent(event)}
	}
	idNode := id.(idPair)
	switch {
	case isIDOne(idNode.left):
		filledRight := fillEvent(idNode.right, eventNode.right)
		base := maxEvent(eventNode.left)
		if rightMin := minEvent(filledRight); rightMin > base {
			base = rightMin
		}
		return normalizeEvent(eventNode.value, eventInt{value: base}, filledRight)
	case isIDOne(idNode.right):
		filledLeft := fillEvent(idNode.left, eventNode.left)
		base := maxEvent(eventNode.right)
		if leftMin := minEvent(filledLeft); leftMin > base {
			base = leftMin
		}
		return normalizeEvent(eventNode.value, filledLeft, eventInt{value: base})
	default:
		return normalizeEvent(
			eventNode.value,
			fillEvent(idNode.left, eventNode.left),
			fillEvent(idNode.right, eventNode.right),
		)
	}
}

func isIDOne(id ID) bool {
	_, ok := id.(idOne)
	return ok
}

func growEvent(id ID, event Event) (Event, int) {
	if one, ok := id.(idOne); ok && one == (idOne{}) {
		if event, ok := event.(eventInt); ok {
			return eventInt{value: event.value + 1}, 0
		}
	}
	if event, ok := event.(eventInt); ok {
		grown, cost := growEvent(id, eventPair{value: event.value, left: eventInt{value: 0}, right: eventInt{value: 0}})
		return grown, cost + 1000000
	}
	eventNode := event.(eventPair)
	idNode := id.(idPair)
	_, leftZero := idNode.left.(idZero)
	_, rightZero := idNode.right.(idZero)
	switch {
	case leftZero:
		grownRight, cost := growEvent(idNode.right, eventNode.right)
		return normalizeEvent(eventNode.value, eventNode.left, grownRight), cost + 1
	case rightZero:
		grownLeft, cost := growEvent(idNode.left, eventNode.left)
		return normalizeEvent(eventNode.value, grownLeft, eventNode.right), cost + 1
	default:
		grownLeft, leftCost := growEvent(idNode.left, eventNode.left)
		grownRight, rightCost := growEvent(idNode.right, eventNode.right)
		if leftCost < rightCost {
			return normalizeEvent(eventNode.value, grownLeft, eventNode.right), leftCost + 1
		}
		return normalizeEvent(eventNode.value, eventNode.left, grownRight), rightCost + 1
	}
}

func asEventPair(event Event) eventPair {
	if event, ok := event.(eventPair); ok {
		return event
	}
	value := eventValue(event)
	return eventPair{value: value, left: eventInt{value: 0}, right: eventInt{value: 0}}
}

func joinEvent(left, right Event) Event {
	leftInt, leftIsInt := left.(eventInt)
	rightInt, rightIsInt := right.(eventInt)
	if leftIsInt && rightIsInt {
		if leftInt.value > rightInt.value {
			return eventInt{value: leftInt.value}
		}
		return eventInt{value: rightInt.value}
	}
	leftNode := asEventPair(left)
	rightNode := asEventPair(right)
	if leftNode.value > rightNode.value {
		leftNode, rightNode = rightNode, leftNode
	}
	difference := rightNode.value - leftNode.value
	return normalizeEvent(
		leftNode.value,
		joinEvent(leftNode.left, liftEvent(difference, rightNode.left)),
		joinEvent(leftNode.right, liftEvent(difference, rightNode.right)),
	)
}

func eventLE(left, right Event) bool {
	leftNode, leftIsNode := left.(eventPair)
	rightNode, rightIsNode := right.(eventPair)
	if !leftIsNode && !rightIsNode {
		return eventValue(left) <= eventValue(right)
	}
	if !leftIsNode {
		return eventValue(left) <= eventValue(right)
	}
	if !rightIsNode {
		limit := eventValue(right)
		return leftNode.value <= limit &&
			eventLE(liftEvent(leftNode.value, leftNode.left), eventInt{value: limit}) &&
			eventLE(liftEvent(leftNode.value, leftNode.right), eventInt{value: limit})
	}
	return leftNode.value <= rightNode.value &&
		eventLE(liftEvent(leftNode.value, leftNode.left), liftEvent(rightNode.value, rightNode.left)) &&
		eventLE(liftEvent(leftNode.value, leftNode.right), liftEvent(rightNode.value, rightNode.right))
}
