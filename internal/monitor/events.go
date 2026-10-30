package monitor

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Event kinds.
const (
	KindConnected = "connected" // first contact with a UPS
	KindCommLost  = "comm_lost" // the server or the UPS stopped answering
	KindCommBack  = "comm_back" // answers again
	KindStatus    = "status"    // ups.status changed
	KindCountdown = "countdown" // the grace period started
	KindCancelled = "cancelled" // the countdown ended without a shutdown
	KindShutdown  = "shutdown"  // the shutdown was ordered
	KindDryRun    = "dry_run"   // the shutdown would have been ordered
	KindError     = "error"     // the shutdown failed
)

// Event is one line of the log the page shows. The page builds the sentence from the kind and
// the fields, in the user's language.
type Event struct {
	At      time.Time `json:"at"`
	UPS     string    `json:"ups,omitempty"`
	Label   string    `json:"label,omitempty"`
	Kind    string    `json:"kind"`
	Status  string    `json:"status,omitempty"`
	Prev    string    `json:"prev,omitempty"`
	Charge  *float64  `json:"charge,omitempty"`
	Runtime *float64  `json:"runtime,omitempty"`
	Reasons []string  `json:"reasons,omitempty"`
	GraceS  int       `json:"grace_s,omitempty"`
	DryRun  bool      `json:"dry_run,omitempty"`
	Action  string    `json:"action,omitempty"`
	Test    bool      `json:"test,omitempty"`
	Message string    `json:"message,omitempty"`
}

// Log is the ring of the last events, written to a file after every change so the page can say
// why the system went down after it comes back.
type Log struct {
	mu     sync.Mutex
	path   string
	max    int
	events []Event
	// OnAdd is called after every event.
	OnAdd func(Event)
}

// NewLog opens the log at path ("" keeps it in memory only).
func NewLog(path string, max int) *Log {
	l := &Log{path: path, max: max}
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &l.events)
		}
	}
	return l
}

// Add appends an event.
func (l *Log) Add(e Event) {
	l.mu.Lock()
	l.events = append(l.events, e)
	if len(l.events) > l.max {
		l.events = l.events[len(l.events)-l.max:]
	}
	l.save()
	cb := l.OnAdd
	l.mu.Unlock()
	if cb != nil {
		cb(e)
	}
}

func (l *Log) save() {
	if l.path == "" {
		return
	}
	b, err := json.Marshal(l.events)
	if err != nil {
		return
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, l.path)
}

// List answers the events, newest last.
func (l *Log) List() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.events))
	copy(out, l.events)
	return out
}

// Clear empties the log.
func (l *Log) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = nil
	l.save()
}
