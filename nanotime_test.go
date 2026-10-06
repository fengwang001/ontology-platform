package ontology

import "time"

func nanotime() int64 { return time.Now().UnixNano() }
