package store

// Lists are stored as []string. LPUSH copies the slice, so it is O(n);
// a deque would make both ends O(1), but a slice is enough for now.

// LPush inserts values at the head of the list at key, one by one, so
// LPUSH k a b c gives [c b a]. It creates the list if needed and returns
// its new length.
func (s *Store) LPush(key string, values ...string) (int, error) {
	return s.push(key, values, true)
}

// RPush appends values to the tail of the list at key and returns its new length.
func (s *Store) RPush(key string, values ...string) (int, error) {
	return s.push(key, values, false)
}

func (s *Store) push(key string, values []string, head bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.getLocked(key, s.now())
	list, err := asList(e, ok)
	if err != nil {
		return 0, err
	}

	if head {
		// Each value goes in front of the previous one, so they end up reversed
		newList := make([]string, 0, len(values)+len(list))
		for i := len(values) - 1; i >= 0; i-- {
			newList = append(newList, values[i])
		}
		list = append(newList, list...)
	} else {
		list = append(list, values...)
	}

	e.value = list
	s.data[key] = e
	return len(list), nil
}

// LPop removes and returns the first element of the list at key.
// ok is false if the list does not exist. Popping the last element deletes the key.
func (s *Store) LPop(key string) (value string, ok bool, err error) {
	return s.pop(key, true)
}

// RPop removes and returns the last element of the list at key.
func (s *Store) RPop(key string) (value string, ok bool, err error) {
	return s.pop(key, false)
}

func (s *Store) pop(key string, head bool) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.getLocked(key, s.now())
	list, err := asList(e, ok)
	if err != nil || len(list) == 0 {
		return "", false, err
	}

	var v string
	if head {
		v, list = list[0], list[1:]
	} else {
		v, list = list[len(list)-1], list[:len(list)-1]
	}

	// Redis has no empty lists: the key disappears with its last element
	if len(list) == 0 {
		delete(s.data, key)
	} else {
		e.value = list
		s.data[key] = e
	}
	return v, true, nil
}

// LLen returns the length of the list at key, or 0 if it does not exist.
func (s *Store) LLen(key string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	e, ok := s.liveLocked(key, s.now())
	list, err := asList(e, ok)
	return len(list), err
}

// LRange returns the elements from start to stop, both inclusive.
// Negative indexes count from the end: -1 is the last element.
// Out-of-range indexes are clamped, as in Redis.
func (s *Store) LRange(key string, start, stop int) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	e, ok := s.liveLocked(key, s.now())
	list, err := asList(e, ok)
	if err != nil {
		return nil, err
	}

	n := len(list)
	if start < 0 {
		start = max(n+start, 0)
	}
	if stop < 0 {
		stop = n + stop
	}
	stop = min(stop, n-1)
	if start > stop {
		return []string{}, nil
	}

	// A copy: the caller uses it after the lock is released, while other
	// clients may change the list
	return append([]string(nil), list[start:stop+1]...), nil
}

// asList returns the list in e, nil if the key does not exist, or
// ErrWrongType if it holds something else.
func asList(e entry, ok bool) ([]string, error) {
	if !ok {
		return nil, nil
	}
	list, isList := e.value.([]string)
	if !isList {
		return nil, ErrWrongType
	}
	return list, nil
}
