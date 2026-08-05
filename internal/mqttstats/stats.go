package mqttstats

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Tracker measures the MQTT transport rate: every second the bytes
// pushed through the counting connection(s) since the last sample are
// converted to a bytes-per-second rate and stored in a fixed-size ring
// window. Connections are counted individually and summed, so
// reconnects and parallel sockets are all included.
type Tracker struct {
	inBytes  uint64
	outBytes uint64
	totalIn  uint64
	totalOut uint64

	mu     sync.Mutex
	in     []int64
	out    []int64
	window int
	last   time.Time
}

// Sample is one snapshot of the rate window, sent over SSE.
type Sample struct {
	IntervalS int     `json:"interval_s"`
	In        []int64 `json:"in"`  // B/s per second, oldest -> newest
	Out       []int64 `json:"out"` // B/s per second, oldest -> newest
	TotalIn   int64   `json:"total_in_bytes"`
	TotalOut  int64   `json:"total_out_bytes"`
}

func New(windowSeconds int) *Tracker {
	if windowSeconds <= 0 {
		windowSeconds = 60
	}
	return &Tracker{
		in:     make([]int64, 0, windowSeconds),
		out:    make([]int64, 0, windowSeconds),
		window: windowSeconds,
		last:   time.Now(),
	}
}

// Wrap wraps a net.Conn so every byte read/written through it is
// counted. The counting connection owns the underlying conn.
func (t *Tracker) Wrap(conn net.Conn) net.Conn {
	return &countingConn{Conn: conn, t: t}
}

// tick samples one second of traffic.
func (t *Tracker) tick() {
	now := time.Now()
	elapsed := now.Sub(t.last)
	if elapsed <= 0 {
		elapsed = time.Second
	}
	in := int64(atomic.SwapUint64(&t.inBytes, 0))
	out := int64(atomic.SwapUint64(&t.outBytes, 0))
	w := t.window

	t.mu.Lock()
	t.in = append(t.in, int64(float64(in)/elapsed.Seconds()))
	t.out = append(t.out, int64(float64(out)/elapsed.Seconds()))
	if len(t.in) > w {
		t.in = t.in[len(t.in)-w:]
		t.out = t.out[len(t.out)-w:]
	}
	t.last = now
	t.mu.Unlock()
}

// Start samples every second until stop is closed.
func (t *Tracker) Start(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.tick()
		case <-stop:
			return
		}
	}
}

// Sample returns a copy of the current rate window.
func (t *Tracker) Sample() Sample {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := Sample{
		IntervalS: 1,
		In:        make([]int64, len(t.in)),
		Out:       make([]int64, len(t.out)),
		TotalIn:   int64(atomic.LoadUint64(&t.totalIn)),
		TotalOut:  int64(atomic.LoadUint64(&t.totalOut)),
	}
	copy(s.In, t.in)
	copy(s.Out, t.out)
	return s
}

type countingConn struct {
	net.Conn
	t *Tracker
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		atomic.AddUint64(&c.t.inBytes, uint64(n))
		atomic.AddUint64(&c.t.totalIn, uint64(n))
	}
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		atomic.AddUint64(&c.t.outBytes, uint64(n))
		atomic.AddUint64(&c.t.totalOut, uint64(n))
	}
	return n, err
}
