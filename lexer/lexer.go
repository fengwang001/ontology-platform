package lexer

import "ontology/cell"

type Event struct{ field cell.Field }
type Sink interface{}
type Limits struct{}
type Lexer struct{}
type Error struct{}
