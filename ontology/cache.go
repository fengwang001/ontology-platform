package ontology

type entry struct {
	value  string
	exists bool
}

type Cache struct{}

func NewCache(source *Source, maxTrackedKeys int) *Cache {
	return nil
}

func (c *Cache) Apply(ev ChangeEvent) bool {
	return false
}

func (c *Cache) BeginRead(key string) (StateToken, error) {
	return StateToken{}, nil
}

func (c *Cache) CompleteRead(tok StateToken) (string, bool, error) {
	return "", false, nil
}

func (c *Cache) Query(key string) (string, bool, error) {
	return "", false, nil
}

func (c *Cache) Fence(key string) (int64, bool) {
	return 0, false
}

func (c *Cache) Lookup(key string) (string, bool, bool) {
	return "", false, false
}

func (c *Cache) OutstandingTokens() int {
	return 0
}

func (c *Cache) TrackedKeys() int {
	return 0
}
