package store

// SAdd adds members to the set at key, creating it if needed, and returns
// how many were not in the set before.
func (s *Store) SAdd(key string, members ...string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.getLocked(key, s.now())
	set, err := asSet(e, ok)
	if err != nil {
		return 0, err
	}
	if set == nil {
		set = make(map[string]struct{})
	}

	added := 0
	for _, m := range members {
		if _, exists := set[m]; !exists {
			set[m] = struct{}{}
			added++
		}
	}

	e.value = set
	s.data[key] = e
	return added, nil
}

// SRem removes members from the set at key and returns how many were in it.
// Removing the last member deletes the key.
func (s *Store) SRem(key string, members ...string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.getLocked(key, s.now())
	set, err := asSet(e, ok)
	if err != nil {
		return 0, err
	}

	n := 0
	for _, m := range members {
		if _, exists := set[m]; exists {
			delete(set, m)
			n++
		}
	}
	if ok && len(set) == 0 {
		delete(s.data, key)
	}
	return n, nil
}

// SIsMember reports whether member is in the set at key.
func (s *Store) SIsMember(key, member string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	e, ok := s.liveLocked(key, s.now())
	set, err := asSet(e, ok)
	if err != nil {
		return false, err
	}
	_, exists := set[member]
	return exists, nil
}

// SMembers returns all members of the set at key, in random order.
func (s *Store) SMembers(key string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	e, ok := s.liveLocked(key, s.now())
	set, err := asSet(e, ok)
	if err != nil {
		return nil, err
	}

	members := make([]string, 0, len(set))
	for m := range set {
		members = append(members, m)
	}
	return members, nil
}

// SCard returns the number of members in the set at key.
func (s *Store) SCard(key string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	e, ok := s.liveLocked(key, s.now())
	set, err := asSet(e, ok)
	return len(set), err
}

// asSet returns the set in e, nil if the key does not exist, or
// ErrWrongType if it holds something else.
func asSet(e entry, ok bool) (map[string]struct{}, error) {
	if !ok {
		return nil, nil
	}
	set, isSet := e.value.(map[string]struct{})
	if !isSet {
		return nil, ErrWrongType
	}
	return set, nil
}
