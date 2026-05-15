package state

import (
	"sync"
	"time"

	"snt-bot/internal/ai"
)

type Phase int

const (
	PhaseIdle    Phase = iota
	PhaseAdding        // waiting for AI extraction loop
	PhaseBalance       // waiting for user to enter N
	PhaseExport        // waiting for user to enter N
)

type State struct {
	Phase      Phase
	History    []ai.Msg
	LastMsg    time.Time
	RetryCount int // AI validation retry counter; reset on fresh user input
}

type Manager struct {
	mu      sync.Mutex
	states  map[int64]*State
	timeout time.Duration
}

func NewManager(timeoutMinutes int) *Manager {
	m := &Manager{
		states:  make(map[int64]*State),
		timeout: time.Duration(timeoutMinutes) * time.Minute,
	}
	go m.gc()
	return m
}

func (m *Manager) Get(userID int64) *State {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.states[userID]
	if !ok {
		s = &State{Phase: PhaseIdle}
		m.states[userID] = s
	}
	s.LastMsg = time.Now()
	return s
}

func (m *Manager) Set(userID int64, s *State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s.LastMsg = time.Now()
	m.states[userID] = s
}

func (m *Manager) Clear(userID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.states, userID)
}

func (m *Manager) gc() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		cutoff := time.Now().Add(-m.timeout)
		for id, s := range m.states {
			if s.LastMsg.Before(cutoff) {
				delete(m.states, id)
			}
		}
		m.mu.Unlock()
	}
}
