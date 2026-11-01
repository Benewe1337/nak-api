package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxStale is how long old data is still shown (flagged stale) while a
// refresh runs — also what keeps the UI usable during CIS maintenance.
const (
	maxStale   = 14 * 24 * time.Hour
	maxEntries = 600
)

// freshness says how long a tool's result counts as current. Messages and
// notifications change by the minute, grades and the study plan rarely.
func freshness(tool string) time.Duration {
	switch {
	case strings.HasPrefix(tool, "moodle_conversation"), tool == "moodle_notifications", tool == "moodle_dashboard":
		return time.Minute
	case tool == "nak_dashboard", tool == "nak_deadlines", tool == "moodle_upcoming",
		strings.HasPrefix(tool, "moodle_assignment"), tool == "moodle_whats_new", strings.HasPrefix(tool, "moodle_forum"),
		tool == "moodle_quizzes", tool == "moodle_choices", tool == "moodle_scheduler", tool == "cis_list_klausuren", tool == "cis_list_seminars",
		tool == "cis_transcript_grades", tool == "mensa_account":
		return 5 * time.Minute
	case tool == "nak_agenda", tool == "cis_timetable", tool == "moodle_course_contents", tool == "moodle_courses",
		tool == "moodle_find", tool == "nak_module", tool == "moodle_completion", tool == "moodle_grades", tool == "moodle_course_info":
		return 30 * time.Minute
	}
	return 2 * time.Hour
}

type entry struct {
	Res json.RawMessage `json:"res"`
	At  time.Time       `json:"at"`
	// Path is set for downloads; the entry is only valid while the file exists.
	Path string `json:"path,omitempty"`
}

type flight struct {
	done chan struct{}
	res  json.RawMessage
	at   time.Time
	err  error
}

type store struct {
	mu      sync.Mutex
	entries map[string]entry
	flights map[string]*flight
	file    string
	dirty   bool
	timer   *time.Timer
}

func newStore(file string) *store {
	s := &store{entries: map[string]entry{}, flights: map[string]*flight{}, file: file}
	if file == "" {
		return s
	}
	if b, err := os.ReadFile(file); err == nil {
		var saved struct {
			V       int              `json:"v"`
			Entries map[string]entry `json:"entries"`
		}
		if json.Unmarshal(b, &saved) == nil && saved.V == 1 && saved.Entries != nil {
			s.entries = saved.Entries
		}
	}
	return s
}

func (s *store) get(key string) (entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	return e, ok
}

func (s *store) put(key string, e entry) {
	s.mu.Lock()
	s.entries[key] = e
	if len(s.entries) > maxEntries {
		s.evictLocked()
	}
	s.markDirtyLocked()
	s.mu.Unlock()
}

func (s *store) clear() {
	s.mu.Lock()
	s.entries = map[string]entry{}
	s.markDirtyLocked()
	s.mu.Unlock()
}

func (s *store) evictLocked() {
	type kv struct {
		k  string
		at time.Time
	}
	all := make([]kv, 0, len(s.entries))
	for k, e := range s.entries {
		all = append(all, kv{k, e.At})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	for _, x := range all[:len(all)-maxEntries] {
		delete(s.entries, x.k)
	}
}

// markDirtyLocked schedules a save; bursts of updates cost one write.
func (s *store) markDirtyLocked() {
	if s.file == "" {
		return
	}
	s.dirty = true
	if s.timer == nil {
		s.timer = time.AfterFunc(3*time.Second, func() { s.save() })
	}
}

func (s *store) save() error {
	s.mu.Lock()
	s.timer = nil
	if s.file == "" || !s.dirty {
		s.mu.Unlock()
		return nil
	}
	b, err := json.Marshal(map[string]any{"v": 1, "entries": s.entries})
	s.dirty = false
	s.mu.Unlock()
	if err != nil {
		return err
	}
	// The cache holds personal data (grades, messages): owner-only, and
	// written via rename so a crash never leaves half a file.
	if err := os.MkdirAll(filepath.Dir(s.file), 0o700); err != nil {
		return err
	}
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.file)
}

// fetch runs fn once per key at a time; concurrent callers share the result.
func (s *store) fetch(key string, now func() time.Time, fn func() (any, error)) (json.RawMessage, time.Time, error) {
	s.mu.Lock()
	if f, ok := s.flights[key]; ok {
		s.mu.Unlock()
		<-f.done
		return f.res, f.at, f.err
	}
	f := &flight{done: make(chan struct{})}
	s.flights[key] = f
	s.mu.Unlock()

	res, err := fn()
	f.at = now()
	if err == nil {
		f.res, f.err = json.Marshal(res)
	} else {
		f.err = err
	}
	s.mu.Lock()
	delete(s.flights, key)
	s.mu.Unlock()
	close(f.done)
	return f.res, f.at, f.err
}
