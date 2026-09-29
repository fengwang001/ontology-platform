package fulljoin

func ins(side Side, key, id string) Change {
	return Change{Kind: '+', Side: side, Row: Row{Key: key, ID: id, Val: id}}
}

func del(side Side, key, id string) Change {
	return Change{Kind: '-', Side: side, Row: Row{Key: key, ID: id, Val: id}}
}
