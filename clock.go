package loadsim

import (
	"sync"
	"sync/atomic"
	"time"
)

type Clock interface {
	Now() time.Time
	Tick() (<-chan time.Time, chan<- struct{})
}

type SimClock struct {
	now       time.Time
	mutex     sync.RWMutex
	cond      sync.Cond
	wg        sync.WaitGroup
	listeners int64

	// Just for testing
	lids int64
	ID   string
}

func NewSimClock() *SimClock {
	var c SimClock
	c.cond.L = c.mutex.RLocker()
	c.now = time.Unix(1, 0)
	return &c
}

func (c *SimClock) Run(stop <-chan struct{}) {
	for {
		//log.Printf("%s: lock", c.ID)
		c.mutex.Lock()
		cnt := int(atomic.LoadInt64(&c.listeners))
		if cnt > 0 {
			c.now = c.now.Add(time.Millisecond)
			c.wg.Add(cnt)
		}
		//log.Printf("%s: broadcast", c.ID)
		c.cond.Broadcast()
		//log.Printf("%s: unlock (now:%s listeners: %d)", c.ID, c.now, cnt)
		c.mutex.Unlock()
		//log.Printf("%s: wait", c.ID)
		c.wg.Wait()

		select {
		case <-stop:
			return
		default:
		}
	}
}

func (c *SimClock) Now() time.Time {
	c.cond.L.Lock()
	defer c.cond.L.Unlock()

	return c.now
}

func (c *SimClock) Tick() (<-chan time.Time, chan<- struct{}) {
	ticker := make(chan time.Time)
	stop := make(chan struct{})

	c.cond.L.Lock()
	//lid := atomic.AddInt64(&c.lids, 1)
	atomic.AddInt64(&c.listeners, 1)
	//log.Printf("%s %d: starting", c.ID, lid)

	go func() {
		for {
			//log.Printf("%s %d: waiting", c.ID, lid)
			c.cond.Wait()

			select {
			case <-stop:
				//log.Printf("%s %d: stopping", c.ID, lid)
				atomic.AddInt64(&c.listeners, -1)
				c.wg.Done()
				c.cond.L.Unlock()
				return
			case ticker <- c.now:
			}
			//log.Printf("%s %d: ticked", c.ID, lid)
			c.wg.Done()
		}
	}()

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
