package main

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type presenceEvent struct {
	Name   string
	Online bool
}

type presenceTracker struct {
	mu     sync.Mutex
	delay  time.Duration
	online map[string]bool
	timers map[string]*time.Timer
	names  map[string]string
	events chan presenceEvent
}

func newPresenceTracker(delay time.Duration) *presenceTracker {
	return &presenceTracker{
		delay:  delay,
		online: make(map[string]bool),
		timers: make(map[string]*time.Timer),
		names:  make(map[string]string),
		events: make(chan presenceEvent, 32),
	}
}

func (p *presenceTracker) Events() <-chan presenceEvent {
	return p.events
}

func (p *presenceTracker) Update(online []string) []presenceEvent {
	now := make(map[string]string)
	for _, name := range online {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		now[key] = trimmed
	}

	var events []presenceEvent

	p.mu.Lock()
	for key, name := range now {
		p.names[key] = name
		if t, ok := p.timers[key]; ok {
			t.Stop()
			delete(p.timers, key)
		}
		if !p.online[key] {
			p.online[key] = true
			events = append(events, presenceEvent{Name: name, Online: true})
		}
	}

	for key, isOnline := range p.online {
		if !isOnline {
			continue
		}
		if _, ok := now[key]; ok {
			continue
		}
		if _, ok := p.timers[key]; ok {
			continue
		}
		p.scheduleOfflineLocked(key)
	}
	p.mu.Unlock()

	return events
}

func (p *presenceTracker) CurrentOnline() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	var online []string
	for key, isOnline := range p.online {
		if !isOnline {
			continue
		}
		name := p.names[key]
		if name == "" {
			name = key
		}
		online = append(online, name)
	}
	sort.Strings(online)
	return online
}

func (p *presenceTracker) scheduleOfflineLocked(key string) {
	if p.delay <= 0 {
		p.online[key] = false
		name := p.names[key]
		p.emit(presenceEvent{Name: name, Online: false})
		return
	}

	var timer *time.Timer
	timer = time.AfterFunc(p.delay, func() {
		p.fireOffline(key, timer)
	})
	p.timers[key] = timer
}

func (p *presenceTracker) fireOffline(key string, timer *time.Timer) {
	p.mu.Lock()
	current, ok := p.timers[key]
	if !ok || current != timer {
		p.mu.Unlock()
		return
	}
	delete(p.timers, key)
	if !p.online[key] {
		p.mu.Unlock()
		return
	}
	p.online[key] = false
	name := p.names[key]
	p.mu.Unlock()

	p.emit(presenceEvent{Name: name, Online: false})
}

func (p *presenceTracker) emit(evt presenceEvent) {
	select {
	case p.events <- evt:
	default:
	}
}
