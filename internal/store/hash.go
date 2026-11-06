package store

// HSet sets fields in the hash at key, given as field1, value1, ...,
// creating the hash if needed. It returns how many fields are new.
// It panics if pairs has odd length; the command layer checks it first.
func (s *Store) HSet(key string, pairs ...string) (int, error) {
	if len(pairs)%2 != 0 {
		panic("store: HSet needs field-value pairs")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.getLocked(key, s.now())
	h, err := asHash(e, ok)
	if err != nil {
		return 0, err
	}
	if h == nil {
		h = make(map[string]string)
	}

	added := 0
	for i := 0; i < len(pairs); i += 2 {
		if _, exists := h[pairs[i]]; !exists {
			added++
		}
		h[pairs[i]] = pairs[i+1]
	}

	e.value = h
	s.data[key] = e
	return added, nil
}

// HGet returns the value of field in the hash at key.
func (s *Store) HGet(key, field string) (value string, ok bool, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	e, exists := s.liveLocked(key, s.now())
	h, err := asHash(e, exists)
	if err != nil {
		return "", false, err
	}
	value, ok = h[field] // reading a nil map is fine
	return value, ok, nil
}

// HDel removes fields from the hash at key and returns how many existed.
// Removing the last field deletes the key.
func (s *Store) HDel(key string, fields ...string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.getLocked(key, s.now())
	h, err := asHash(e, ok)
	if err != nil {
		return 0, err
	}

	n := 0
	for _, f := range fields {
		if _, exists := h[f]; exists {
			delete(h, f)
			n++
		}
	}
	if ok && len(h) == 0 {
		delete(s.data, key)
	}
	return n, nil
}

// HGetAll returns all fields and values as field1, value1, ...
// The order is random, as in Redis.
func (s *Store) HGetAll(key string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	e, ok := s.liveLocked(key, s.now())
	h, err := asHash(e, ok)
	if err != nil {
		return nil, err
	}

	pairs := make([]string, 0, 2*len(h))
	for f, v := range h {
		pairs = append(pairs, f, v)
	}
	return pairs, nil
}

// HLen returns the number of fields in the hash at key.
func (s *Store) HLen(key string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	e, ok := s.liveLocked(key, s.now())
	h, err := asHash(e, ok)
	return len(h), err
}

// asHash returns the hash in e, nil if the key does not exist, or
// ErrWrongType if it holds something else.
func asHash(e entry, ok bool) (map[string]string, error) {
	if !ok {
		return nil, nil
	}
	h, isHash := e.value.(map[string]string)
	if !isHash {
		return nil, ErrWrongType
	}
	return h, nil
}
