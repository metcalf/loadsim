package loadsim

import (
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
	Tick() <-chan time.Time // Return a channel that ticks with the clock.
	Done()                  // Done waiting on the clock for now
}

type workClock struct {
	now     time.Time
	ticker  chan time.Time
	started bool

	startMutex sync.Mutex
	timeMutex  sync.Mutex
}

func (c *workClock) Now() time.Time {
	c.timeMutex.Lock()
	defer c.timeMutex.Unlock()

	return c.now
}

// Tick indicates that the consumer is active and returns
// a channel of time updates. Advancing the clock will
// block on reading the next time update.
func (c *workClock) Tick() <-chan time.Time {
	c.startMutex.Lock()
	defer c.startMutex.Unlock()
	c.started = true

	c.timeMutex.Lock()
	defer c.timeMutex.Unlock()
	c.ticker = make(chan time.Time)

	return c.ticker
}

// Done indicates that the consumer is no longer active
// and the clock should not block on ticking.
func (c *workClock) Done() {
	c.startMutex.Lock()
	defer c.startMutex.Unlock()
	c.started = false

	// Unblock reading from the ticker channel if necessary
	select {
	case <-c.ticker:
	default:
	}

	c.timeMutex.Lock()
	defer c.timeMutex.Unlock()
	close(c.ticker)
}

// Advance the clock to time t. Blocks until the new time
// is read if the consumer is active. Returns a boolean indicating
// whether the consumer is active.
func (c *workClock) Advance(t time.Time) bool {
	c.startMutex.Lock()
	c.timeMutex.Lock()
	defer c.timeMutex.Unlock()

	wasStarted := c.started
	c.startMutex.Unlock()

	c.now = t
	if wasStarted {
		c.ticker <- t
	}

	return wasStarted
}

type SimClock struct {
	clocks []*workClock
}

func (c *SimClock) Run(stop <-chan struct{}) {
	var now time.Time
	go func() {
		for {
			found := false
			for _, clock := range c.clocks {
				//log.Printf("advancing clock %d to %s", i, now)
				found = clock.Advance(now) || found
				//log.Printf("advanced")
			}

			// Only advance if someone is listening
			if found {
				now = now.Add(time.Millisecond)
			}

			select {
			case <-stop:
				return
			default:
			}
		}
	}()
}

func (c *SimClock) Clock() Clock {
	var clock workClock
	c.clocks = append(c.clocks, &clock)

	return &clock
}

type WallClock struct {
	ticker *time.Ticker
}

func (c *WallClock) Now() time.Time {
	return time.Now()
}

func (c *WallClock) Tick() <-chan time.Time {
	if c.ticker == nil {
		c.ticker = time.NewTicker(time.Millisecond)
	}
	return c.ticker.C
}

func (c *WallClock) Done() {
	if c.ticker != nil {
		c.ticker.Stop()
	}
}
