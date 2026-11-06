package store

import "strconv"

// Dump returns commands that rebuild the current data from an empty store:
// one command per key, plus PEXPIREAT for containers with a timeout.
// Expired keys are left out. This is what an AOF rewrite writes, so 1000
// SETs to one key become a single SET.
//
// Values are copied while the read lock is held, so the result stays
// valid after Dump returns, whatever clients do next.
func (s *Store) Dump() [][]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.now()
	cmds := make([][]string, 0, len(s.data))
	for key, e := range s.data {
		if e.expired(now) {
			continue
		}

		var deadline string
		if !e.expiresAt.IsZero() {
			deadline = strconv.FormatInt(e.expiresAt.UnixMilli(), 10)
		}

		switch v := e.value.(type) {
		case string:
			cmd := []string{"SET", key, v}
			if deadline != "" {
				// SET carries its own deadline, no second command needed
				cmd = append(cmd, "PXAT", deadline)
			}
			cmds = append(cmds, cmd)
			continue

		case []string:
			cmds = append(cmds, append([]string{"RPUSH", key}, v...))

		case map[string]string:
			cmd := make([]string, 0, 2+2*len(v))
			cmd = append(cmd, "HSET", key)
			for f, val := range v {
				cmd = append(cmd, f, val)
			}
			cmds = append(cmds, cmd)

		case map[string]struct{}:
			cmd := make([]string, 0, 2+len(v))
			cmd = append(cmd, "SADD", key)
			for m := range v {
				cmd = append(cmd, m)
			}
			cmds = append(cmds, cmd)

		default:
			panic("store: unknown value type")
		}

		if deadline != "" {
			cmds = append(cmds, []string{"PEXPIREAT", key, deadline})
		}
	}
	return cmds
}
