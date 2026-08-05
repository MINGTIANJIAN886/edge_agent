package profile

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"
)

// ProbeManager registers capability probes, executes them with an
// in-flight guard and caches the latest result per capability.
type ProbeManager struct {
	mu       sync.Mutex
	probes   map[string]CapabilityProbe
	results  map[string]CapabilityResult
	inflight map[string]bool
	store    *Store
	subs     map[chan struct{}]struct{}
}

func NewProbeManager(store *Store) *ProbeManager {
	m := &ProbeManager{
		probes:   make(map[string]CapabilityProbe),
		results:  make(map[string]CapabilityResult),
		inflight: make(map[string]bool),
		store:    store,
		subs:     make(map[chan struct{}]struct{}),
	}
	if store != nil {
		if p, err := store.Load(); err == nil && p != nil {
			for name, res := range p.Capabilities {
				m.results[name] = res
			}
			log.Printf("profile: loaded %d cached results from %s", len(m.results), store.Path)
		}
	}
	return m
}

func (m *ProbeManager) Register(p CapabilityProbe) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.probes[p.Name()] = p
	log.Printf("profile: registered probe %q", p.Name())
}

func (m *ProbeManager) Names() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.probes))
	for n := range m.probes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Probe runs one capability probe. If the same probe is already running
// the cached result is returned instead (guards shared resources such as
// a camera). Results are persisted and broadcast to subscribers.
func (m *ProbeManager) Probe(ctx context.Context, name string, force bool) CapabilityResult {
	m.mu.Lock()
	p, ok := m.probes[name]
	if !ok {
		m.mu.Unlock()
		return CapabilityResult{Name: name, ErrorCode: CodeProbeUnknown, Message: "no such probe registered"}
	}
	if m.inflight[name] {
		if r, ok := m.results[name]; ok {
			m.mu.Unlock()
			return r
		}
		m.mu.Unlock()
		return CapabilityResult{Name: name, ErrorCode: CodeProbeBusy, Message: "probe already running"}
	}
	m.inflight[name] = true
	m.mu.Unlock()

	res := p.Probe(ctx, ProbeRequest{Force: force})
	res.Name = name
	res.TestedAt = time.Now()
	if res.Method == "" {
		res.Method = "probe"
	}

	m.mu.Lock()
	m.inflight[name] = false
	m.results[name] = res
	if m.store != nil {
		if err := m.store.Update(name, res); err != nil {
			log.Printf("profile: save failed for %s: %v", name, err)
		}
	}
	m.mu.Unlock()
	m.notify()
	log.Printf("profile: probe %s -> result=%v supported=%v available=%v code=%q latency=%dms",
		name, res.Result, res.Supported, res.Available, res.ErrorCode, res.LatencyMS)
	return res
}

// ProbeAll runs the named probes sequentially. Empty list probes all
// registered capabilities. Returns a copy of all latest results.
func (m *ProbeManager) ProbeAll(ctx context.Context, names []string, force bool) map[string]CapabilityResult {
	if len(names) == 0 {
		names = m.Names()
	}
	for _, n := range names {
		m.Probe(ctx, n, force)
	}
	return m.All()
}

func (m *ProbeManager) Get(name string) (CapabilityResult, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.results[name]
	return r, ok
}

func (m *ProbeManager) All() map[string]CapabilityResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]CapabilityResult, len(m.results))
	for k, v := range m.results {
		out[k] = v
	}
	return out
}

// Subscribe registers a channel that receives a notification whenever a
// probe completes.
func (m *ProbeManager) Subscribe(ch chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs[ch] = struct{}{}
}

func (m *ProbeManager) Unsubscribe(ch chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.subs, ch)
}

func (m *ProbeManager) notify() {
	m.mu.Lock()
	subs := make([]chan struct{}, 0, len(m.subs))
	for ch := range m.subs {
		subs = append(subs, ch)
	}
	m.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
