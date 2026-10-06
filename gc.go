package retention

func (s *Store) Collect(now int64) (GCResult, error) {
	if now < 0 {
		return GCResult{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceClock(now); err != nil {
		return GCResult{}, err
	}
	return s.collectLocked(now), nil
}

func (s *Store) ExpireAndCollect(now int64) (GCResult, error) {
	if now < 0 {
		return GCResult{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceClock(now); err != nil {
		return GCResult{}, err
	}
	s.expireLocked(now)
	return s.collectLocked(now), nil
}

func (s *Store) collectLocked(now int64) GCResult {
	liveCommits := make(map[ID]bool)
	var frontier []ID

	seed := func(commit ID) {
		if commit != "" && s.commits[commit] != nil && !liveCommits[commit] {
			liveCommits[commit] = true
			frontier = append(frontier, commit)
		}
	}

	expiredRoots := s.temporaryExpiredRoots(now)
	for commit, count := range s.pins {
		if count-expiredRoots[commit] > 0 {
			seed(commit)
		}
	}

	liveContents := make(map[ID]bool)
	for len(frontier) > 0 {
		commitID := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		commit := s.commits[commitID]
		for _, contentID := range commit.Contents {
			liveContents[contentID] = true
		}
		for _, parent := range commit.Parents {
			seed(parent)
		}
	}

	result := GCResult{}
	for id, commit := range s.commits {
		if liveCommits[id] || now-commit.Written < s.policy.FreshGrace {
			continue
		}
		delete(s.commits, id)
		s.deadCommits[id] = true
		result.CommitsDeleted++
	}
	for id, content := range s.content {
		if liveContents[id] || now-content.Written < s.policy.FreshGrace {
			continue
		}
		delete(s.content, id)
		s.deadContents[id] = true
		result.ContentsDeleted++
		result.BytesDeleted += content.Size
	}
	return result
}
