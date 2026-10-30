package engine

import (
	"context"
	"sync"
)

// ConnLimit caps how many connections all downloads from one server may use
// together, and splits them evenly between those downloads (like IDM's
// "connections per server" setting).
//
// Without it, three downloads from one server open 8 connections each. On a
// line that is already full, the extra connections only add congestion,
// servers throttle them, and some downloads starve at 0 B/s while others take
// everything. With it, the 8 connections are shared: 1 download uses 8,
// 2 downloads use 4 each, 3 downloads use 3+3+2.
type ConnLimit struct {
	mu      sync.Mutex
	max     int
	used    int
	waiting map[*Downloader]int  // connections waiting for a slot, per download
	per     map[*Downloader]int  // connections in use per download
	members map[*Downloader]bool // downloads currently running
	wake    chan struct{}        // closed (and replaced) on every change
}

// NewConnLimit allows n connections at once in total.
func NewConnLimit(n int) *ConnLimit {
	if n < 1 {
		n = 1
	}
	return &ConnLimit{
		max:     n,
		per:     map[*Downloader]int{},
		waiting: map[*Downloader]int{},
		members: map[*Downloader]bool{},
		wake:    make(chan struct{}),
	}
}

func (c *ConnLimit) broadcastLocked() {
	close(c.wake)
	c.wake = make(chan struct{})
}

// share is the fair number of connections for each running download.
func (c *ConnLimit) shareLocked() int {
	n := len(c.members)
	if n == 0 {
		return c.max
	}
	return (c.max + n - 1) / n // rounded up; the total cap keeps the sum at max
}

func (c *ConnLimit) join(d *Downloader) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.members[d] = true
	c.broadcastLocked()
	c.mu.Unlock()
}

func (c *ConnLimit) leave(d *Downloader) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.members, d)
	c.broadcastLocked()
	c.mu.Unlock()
}

// acquire waits until d may open another connection.
func (c *ConnLimit) acquire(ctx context.Context, d *Downloader) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	c.waiting[d]++
	defer func() {
		if c.waiting[d]--; c.waiting[d] <= 0 {
			delete(c.waiting, d)
		}
		c.mu.Unlock()
	}()
	for {
		if c.used < c.max && c.per[d] < c.shareLocked() {
			c.used++
			c.per[d]++
			return nil
		}
		w := c.wake
		c.mu.Unlock()
		select {
		case <-w:
			c.mu.Lock()
		case <-ctx.Done():
			c.mu.Lock()
			return ctx.Err()
		}
	}
}

// yield reports whether d holds more than its fair share while another
// download is waiting for a connection. If so, the slot is given up right away
// (the caller must not call release) and the caller should stop its current
// piece so the waiting download can start.
func (c *ConnLimit) yield(d *Downloader) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.per[d] <= c.shareLocked() {
		return false
	}
	others := false
	for w, n := range c.waiting {
		if w != d && n > 0 && c.per[w] < c.shareLocked() {
			others = true
			break
		}
	}
	if !others {
		return false
	}
	c.used--
	if c.per[d]--; c.per[d] <= 0 {
		delete(c.per, d)
	}
	c.broadcastLocked()
	return true
}

// release gives a connection back.
func (c *ConnLimit) release(d *Downloader) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.used--
	if c.per[d]--; c.per[d] <= 0 {
		delete(c.per, d)
	}
	c.broadcastLocked()
	c.mu.Unlock()
}
