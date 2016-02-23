package loadsim

import (
	"math/rand"
	"sync/atomic"
	"time"
)

type Clock interface {
	Now() time.Time
	Tick() (<-chan time.Time, chan<- struct{})
}

type simTicker struct {
	ticker chan<- time.Time
	stop   <-chan struct{}
}

type SimClock struct {
	Hook func()

	now int64

	listeners []simTicker
	queue     chan simTicker
}

func NewSimClock() *SimClock {
	var c SimClock

	c.now = 1
	c.queue = make(chan simTicker)

	return &c
}

func (c *SimClock) Run(stop <-chan struct{}) {
	for {
		//log.Printf("%s: lock", c.ID)
		// TODO: Try atomic here with millis
		var now time.Time
		if len(c.listeners) > 0 {
			millis := atomic.AddInt64(&c.now, 1)
			now = time.Unix(millis/1000, (millis%1000)*1e6)

			if c.Hook != nil {
				c.Hook()
			}
		}

		// Shuffle listeners so that we don't schedule the same way every time
		for i := range c.listeners {
			j := rand.Intn(i + 1)
			c.listeners[i], c.listeners[j] = c.listeners[j], c.listeners[i]
		}

		// Call each listener in order
		origStart := len(c.listeners) - 1
		for i := range c.listeners {
			idx := origStart - i
			listener := c.listeners[idx]

			// Loop until we write to the listener or stop, pulling from
			// the new listener queue as we go.
			var done bool
			for !done {
				select {
				case listener.ticker <- now:
					done = true
				case <-listener.stop:
					// Delete this index, since we're iterating backwards this
					// won't change the index of any remaining elements
					c.listeners = append(c.listeners[:idx], c.listeners[idx+1:]...)
					done = true
				case new := <-c.queue:
					c.listeners = append(c.listeners, new)
				}
			}
		}

		select {
		case <-stop:
			return
		case new := <-c.queue:
			// Handle the case where c.listeners is empty so we never
			// run the main loop
			c.listeners = append(c.listeners, new)
		default:
		}
	}
}

func (c *SimClock) Now() time.Time {
	millis := atomic.LoadInt64(&c.now)

	return time.Unix(millis/1000, (millis%1000)*1e6)
}

func (c *SimClock) Tick() (<-chan time.Time, chan<- struct{}) {
	ticker := make(chan time.Time)
	stop := make(chan struct{})

	c.queue <- simTicker{ticker, stop}

	return ticker, stop
}

type WallClock struct{}

func (c *WallClock) Now() time.Time {
	return time.Now()
}

func (c *WallClock) Tick() (<-chan time.Time, chan<- struct{}) {
	ticker := time.NewTicker(time.Millisecond)
	stop := make(chan struct{})

	go func() {
		<-stop
		ticker.Stop()
	}()

	return ticker.C, stop
}
