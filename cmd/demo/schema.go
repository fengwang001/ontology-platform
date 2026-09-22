package main

import "ontology/message"

func newDemoSchema() *message.Schema {
	inner := message.NewSchema(message.Field{Number: 1, Kind: message.KindVarint})
	outer := message.NewSchema(message.Field{Number: 1, Kind: message.KindMessage, Schema: inner})
	addr := message.NewSchema(
		message.Field{Number: 1, Kind: message.KindVarint},
		message.Field{Number: 2, Kind: message.KindBytes},
	)
	return message.NewSchema(
		message.Field{Number: 1, Kind: message.KindVarint},
		message.Field{Number: 2, Kind: message.KindBytes},
		message.Field{Number: 3, Kind: message.KindVarint, Repeated: true},
		message.Field{Number: 4, Kind: message.KindMessage, Schema: addr},
		message.Field{Number: 5, Kind: message.KindMessage, Schema: outer},
	)
}
