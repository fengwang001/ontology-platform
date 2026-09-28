package truncate

// Cluster methods (skeleton; implementations are added incrementally).

func (c *Cluster) AddReplica(name string, initialGeneration int) error {
	_ = name
	_ = initialGeneration
	return nil
}

func (c *Cluster) BecomeLeader(name string, generation int) error {
	_ = name
	_ = generation
	return nil
}

func (c *Cluster) LeaderAppend(name string, generation int, data string) error {
	_ = name
	_ = generation
	_ = data
	return nil
}

func (c *Cluster) FollowerReplicate(follower string, generation int, data string) error {
	_ = follower
	_ = generation
	_ = data
	return nil
}

func (c *Cluster) EntryAt(name string, offset int) (Entry, error) {
	_ = name
	_ = offset
	return Entry{}, nil
}

func (c *Cluster) LogEnd(name string) (int, error) {
	_ = name
	return 0, nil
}

func (c *Cluster) LeaderEndForGeneration(follower string, generation int) (int, error) {
	_ = follower
	_ = generation
	return 0, nil
}

func (c *Cluster) TruncateAt(name string, cutPoint int) error {
	_ = name
	_ = cutPoint
	return nil
}

func (c *Cluster) RecoverFollower(follower string) (int, error) {
	_ = follower
	return 0, nil
}

