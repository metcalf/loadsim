package loadsim

import (
	"sync"
	"time"
)

type WorkClock struct {
	wg      sync.WaitGroup
	ticker  chan time.Time
	started bool
}

func (c *WorkClock) Tick() time.Time {
	if c.started {
		c.wg.Done()
	} else {
		c.started = true
	}

	now := <-c.ticker
	c.wg.Add(1)

	return now
}

func (c *WorkClock) Done() {
	c.started = false
	c.wg.Done()
}

type SimClock struct {
	clocks []WorkClock
	wg     sync.WaitGroup
}

func (c *SimClock) Run() {
	var now time.Time
	for {
		c.wg.Wait()
		now = now.Add(time.Millisecond)

		for _, clock := range c.clocks {
			if clock.started {
				clock.ticker <- now
			}
		}

	}
}

func (c *SimClock) Clock() WorkClock {
	clock := WorkClock{
		wg:     c.wg,
		ticker: make(chan time.Time),
	}
	c.clocks = append(c.clocks)

	return clock
}
