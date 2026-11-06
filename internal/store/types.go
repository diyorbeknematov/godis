package store

import (
	"errors"
	"time"
)

// ErrWrongType is returned when a command meets a key of another type,
// e.g. GET on a list.
var ErrWrongType = errors.New("store: wrong type")

// Type returns the name of the type stored at key, as the TYPE command
// shows it, or "none" if the key does not exist.
func (s *Store) Type(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	e, ok := s.liveLocked(key, s.now())
	if !ok {
		return "none"
	}
	switch e.value.(type) {
	case string:
		return "string"
	case []string:
		return "list"
	case map[string]string:
		return "hash"
	case map[string]struct{}:
		return "set"
	}
	panic("store: unknown value type")
}

// liveLocked returns the entry for key if it exists and has not expired.
// Unlike getLocked it never deletes, so a read lock is enough.
func (s *Store) liveLocked(key string, now time.Time) (entry, bool) {
	e, ok := s.data[key]
	if !ok || e.expired(now) {
		return entry{}, false
	}
	return e, true
}

func asString(e entry) (string, error) {
	v, ok := e.value.(string)
	if !ok {
		return "", ErrWrongType
	}
	return v, nil
}
