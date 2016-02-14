package loadsim

import "time"

type WorkClock struct {
	ticker  chan time.Time
	started bool
}

func (c *WorkClock) Tick() <-chan time.Time {
	c.started = true
	return c.ticker
}

func (c *WorkClock) Done() {
	c.started = false
	// Unblock reading from the ticker channel if necessary
	select {
	case <-c.ticker:
	default:
	}
}

type SimClock struct {
	clocks []WorkClock
}

func (c *SimClock) Run() {
	var now time.Time
	for {
		found := false
		for _, clock := range c.clocks {
			if clock.started {
				clock.ticker <- now
				found = true
			}
		}

		// Only tick if someone was listening!
		if found {
			now = now.Add(time.Millisecond)
		}
	}
}

func (c *SimClock) Clock() WorkClock {
	clock := WorkClock{
		ticker: make(chan time.Time),
	}
	c.clocks = append(c.clocks)

	return clock
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

func (c *WallClock) Stop() {
	if c.ticker != nil {
		c.ticker.Stop()
	}
}
