package certselector

type KeyType int

const (
	KeyTypeEC KeyType = iota + 1
	KeyTypeRSA
)

type MatchSource int

const (
	MatchExact MatchSource = iota + 1
	MatchWildcard
	MatchDefault
)

type Certificate struct {
	ID        string
	Names     []string
	KeyType   KeyType
	NotBefore int64
	NotAfter  int64
}

type Selection struct {
	Certificate Certificate
	Source      MatchSource
}

type SelectInput struct {
	Name     string
	KeyTypes map[KeyType]bool
	Now      int64
}
