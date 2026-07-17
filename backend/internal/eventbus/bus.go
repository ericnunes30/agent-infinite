package eventbus

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Event struct {
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	At       time.Time `json:"at"`
	EntityID string    `json:"entityId"`
	Payload  any       `json:"payload"`
}

type Bus struct {
	mu          sync.Mutex
	next        uint64
	subscribers map[uint64]chan Event
	journalPath string
	journalErr  string
}

func New() *Bus { return &Bus{subscribers: make(map[uint64]chan Event)} }

func (b *Bus) SetJournal(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b.mu.Lock()
	b.journalPath = path
	b.journalErr = ""
	b.mu.Unlock()
	return nil
}

func (b *Bus) JournalHealth() (string, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.journalPath == "" {
		return "disabled", ""
	}
	if b.journalErr != "" {
		return "degraded", b.journalErr
	}
	return "healthy", ""
}

func (b *Bus) Emit(eventType, entityID string, payload any) {
	event := Event{ID: id(), Type: eventType, At: time.Now().UTC(), EntityID: entityID, Payload: payload}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.journalPath != "" {
		data, err := json.Marshal(event)
		if err == nil {
			data = append(data, '\n')
			var file *os.File
			file, err = os.OpenFile(b.journalPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err == nil {
				_, err = file.Write(data)
				closeErr := file.Close()
				if err == nil {
					err = closeErr
				}
			}
			if err != nil {
				b.journalErr = err.Error()
			}
		} else {
			b.journalErr = err.Error()
		}
	}
	for subscriberID, subscriber := range b.subscribers {
		select {
		case subscriber <- event:
		default:
			delete(b.subscribers, subscriberID)
			close(subscriber)
		}
	}
}

func (b *Bus) Subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subscriberID := b.next
	b.next++
	channel := make(chan Event, 128)
	b.subscribers[subscriberID] = channel
	return channel, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if existing, ok := b.subscribers[subscriberID]; ok {
			delete(b.subscribers, subscriberID)
			close(existing)
		}
	}
}

func id() string {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data)
}
