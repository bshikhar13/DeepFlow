package harness

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"deepflow/internal/provider"
)

// ErrBudgetExhausted means an agent used its model-call budget for the session.
var ErrBudgetExhausted = errors.New("inference budget exhausted")

// Scheduler caps concurrent model calls, serving high priority work first.
type Scheduler struct {
	mu   sync.Mutex
	free int
	high []chan struct{}
	low  []chan struct{}
}

// NewScheduler allows n concurrent holders.
func NewScheduler(n int) *Scheduler { return &Scheduler{free: n} }

// Acquire blocks for a slot. The returned function releases it.
func (s *Scheduler) Acquire(ctx context.Context, high bool) (func(), error) {
	s.mu.Lock()
	if s.free > 0 {
		s.free--
		s.mu.Unlock()
		return s.releaser(), nil
	}
	ch := make(chan struct{})
	if high {
		s.high = append(s.high, ch)
	} else {
		s.low = append(s.low, ch)
	}
	s.mu.Unlock()
	select {
	case <-ch:
		return s.releaser(), nil
	case <-ctx.Done():
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.remove(ch) {
			return nil, ctx.Err()
		}
		// Granted at the same moment we canceled: hand the slot on.
		s.handOff()
		return nil, ctx.Err()
	}
}

func (s *Scheduler) remove(ch chan struct{}) bool {
	for _, q := range []*[]chan struct{}{&s.high, &s.low} {
		for i, c := range *q {
			if c == ch {
				*q = append((*q)[:i], (*q)[i+1:]...)
				return true
			}
		}
	}
	return false
}

func (s *Scheduler) releaser() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.handOff()
		})
	}
}

func (s *Scheduler) handOff() {
	switch {
	case len(s.high) > 0:
		close(s.high[0])
		s.high = s.high[1:]
	case len(s.low) > 0:
		close(s.low[0])
		s.low = s.low[1:]
	default:
		s.free++
	}
}

func (s *Scheduler) waiting() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.high) + len(s.low)
}

// Metrics counts inference use across a session.
type Metrics struct {
	Calls        atomic.Int64
	InputTokens  atomic.Int64
	OutputTokens atomic.Int64
	Failures     atomic.Int64
}

// Governed wraps a provider with scheduling, an optional budget and metrics.
type Governed struct {
	P       provider.Provider
	Sched   *Scheduler
	High    bool
	Budget  *atomic.Int64 // remaining calls; nil means unlimited
	Metrics *Metrics
}

// Name implements provider.Provider.
func (g *Governed) Name() string { return g.P.Name() }

func (g *Governed) begin(ctx context.Context) (func(), error) {
	if g.Budget != nil && g.Budget.Add(-1) < 0 {
		g.Budget.Add(1)
		return nil, ErrBudgetExhausted
	}
	release, err := g.Sched.Acquire(ctx, g.High)
	if err != nil {
		if g.Budget != nil {
			g.Budget.Add(1)
		}
		return nil, err
	}
	return release, nil
}

func (g *Governed) record(u provider.Usage, err error) {
	if g.Metrics == nil {
		return
	}
	g.Metrics.Calls.Add(1)
	g.Metrics.InputTokens.Add(u.InputTokens)
	g.Metrics.OutputTokens.Add(u.OutputTokens)
	if err != nil {
		g.Metrics.Failures.Add(1)
	}
}

// Stream implements provider.Provider.
func (g *Governed) Stream(ctx context.Context, req provider.TextRequest, onDelta func(string)) (provider.Usage, error) {
	release, err := g.begin(ctx)
	if err != nil {
		return provider.Usage{}, err
	}
	defer release()
	u, err := g.P.Stream(ctx, req, onDelta)
	g.record(u, err)
	return u, err
}

// Structured implements provider.Provider.
func (g *Governed) Structured(ctx context.Context, req provider.StructuredRequest, out any) (provider.Usage, error) {
	release, err := g.begin(ctx)
	if err != nil {
		return provider.Usage{}, err
	}
	defer release()
	u, err := g.P.Structured(ctx, req, out)
	g.record(u, err)
	return u, err
}
