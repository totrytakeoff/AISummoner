package agent

import (
	"sync"

	"github.com/aisummoner/aisummoner/internal/id"
)

const defaultSubscriberBuffer = 32

type subscriber struct {
	id     string
	events chan Event
	closed bool
}

// eventHub deliberately drops and closes a slow subscriber instead of ever
// blocking a running Turn. Persisted state remains available through GET.
type eventHub struct {
	mu         sync.Mutex
	bySession  map[string]map[string]*subscriber
	bufferSize int
	closed     bool
}

func newEventHub(bufferSize int) *eventHub {
	if bufferSize <= 0 {
		bufferSize = defaultSubscriberBuffer
	}
	return &eventHub{bySession: make(map[string]map[string]*subscriber), bufferSize: bufferSize}
}

func (hub *eventHub) subscribe(sessionID string) (<-chan Event, func(), error) {
	subscriberID, err := id.New("sub")
	if err != nil {
		return nil, nil, err
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return nil, nil, ErrServiceClosed
	}
	value := &subscriber{id: subscriberID, events: make(chan Event, hub.bufferSize)}
	if hub.bySession[sessionID] == nil {
		hub.bySession[sessionID] = make(map[string]*subscriber)
	}
	hub.bySession[sessionID][subscriberID] = value
	var once sync.Once
	cancel := func() {
		once.Do(func() { hub.remove(sessionID, subscriberID) })
	}
	return value.events, cancel, nil
}

func (hub *eventHub) publish(event Event) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return
	}
	for subscriberID, value := range hub.bySession[event.SessionID] {
		select {
		case value.events <- event:
		default:
			close(value.events)
			value.closed = true
			delete(hub.bySession[event.SessionID], subscriberID)
		}
	}
	if len(hub.bySession[event.SessionID]) == 0 {
		delete(hub.bySession, event.SessionID)
	}
}

func (hub *eventHub) remove(sessionID, subscriberID string) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	values := hub.bySession[sessionID]
	value := values[subscriberID]
	if value == nil {
		return
	}
	delete(values, subscriberID)
	if !value.closed {
		close(value.events)
		value.closed = true
	}
	if len(values) == 0 {
		delete(hub.bySession, sessionID)
	}
}

func (hub *eventHub) count(sessionID string) int {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return len(hub.bySession[sessionID])
}

func (hub *eventHub) countAll() int {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	total := 0
	for _, values := range hub.bySession {
		total += len(values)
	}
	return total
}

func (hub *eventHub) closeSession(sessionID string) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	values := hub.bySession[sessionID]
	for _, value := range values {
		if !value.closed {
			close(value.events)
			value.closed = true
		}
	}
	delete(hub.bySession, sessionID)
}

func (hub *eventHub) close() {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return
	}
	hub.closed = true
	for _, values := range hub.bySession {
		for _, value := range values {
			if !value.closed {
				close(value.events)
				value.closed = true
			}
		}
	}
	hub.bySession = make(map[string]map[string]*subscriber)
}
