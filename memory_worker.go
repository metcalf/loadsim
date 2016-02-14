package loadsim

import (
	"net/http"
	"sync"
	"time"
)

// Need a mapping from request => resources required

// CPUs and wall clock only ever give back 1ms

type Resource interface {
	Name() string
	Ask(int) int
}

type ResourceNeed struct {
	Name  string
	Value int
}

type ResourceMap interface {
	RequestNeeds(*http.Request) ([]ResourceNeed, error)
}

type SimWorker struct {
	ResourceMap ResourceMap
	Resources   []Resource
	Clock       interface {
		Tick() time.Time // Wait for the next tick of the clock.
		Done()           // Done waiting on the clock for now
	}
}

func (w *SimWorker) Run(queue <-chan Task) {
	for task := range queue {
		needs, err := w.ResourceMap.RequestNeeds(task.Request)
		if err != nil {
			task.Result <- Result{Err: err}
			continue
		}

		for {
			now := w.Clock.Tick()

			need := needs[0]

			// TODO: optimize this with a map
			for _, res := range w.Resources {
				if res.Name() != need.Name {
					continue
				}
				need.Value -= res.Ask(need.Value)
			}
			if need.Value <= 0 {
				needs = needs[1:]
			}

			if len(needs) == 0 {
				task.Result <- Result{
					// TODO: Eventually incorporate 503 and 429
					StatusCode: 200,
					End:        now,
				}
				break
			}
		}
		w.Clock.Done()
	}
}

type WorkerPool struct {
	Backlog int
	Timeout time.Duration
	Workers []Worker
}

func (w *WorkerPool) Run(queue <-chan Task) {
	backlog := make(chan Task, w.Backlog)
	workerQueue := make(chan Task)

	var wg sync.WaitGroup

	for _, worker := range w.Workers {
		go func(worker Worker) {
			wg.Add(1)
			worker.Run(workerQueue)
			wg.Done()
		}(worker)
	}

	go func() {
		for task := range queue {
			select {
			case backlog <- task:
			default:
				// Drop the task on the floor if the backlog is full
			}
		}
		close(backlog)
	}()

	for next := range backlog {
		workerQueue <- next
	}
	close(workerQueue)
	wg.Wait()
}

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
