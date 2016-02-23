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
	Reset()
}

type TimeResource struct {
	have  bool
	mutex sync.Mutex
}

func (c *TimeResource) Name() string { return "time" }
func (c *TimeResource) Ask(i int) int {
	if i == 0 {
		return 0
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.have {
		c.have = false
		return 1
	}
	return 0
}
func (c *TimeResource) Reset() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.have = true
}

type CPUResource struct {
	Count int // Number of CPUs represented by this resource

	remaining int
	mutex     sync.Mutex
}

func (c *CPUResource) Name() string { return "CPU" }
func (c *CPUResource) Ask(i int) int {
	if i == 0 {
		return 0
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.remaining > 0 {
		c.remaining--
		return 1
	}
	return 0
}
func (c *CPUResource) Reset() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.remaining = c.Count
}

type ResourceNeed struct {
	Name  string
	Value int
}

type ResourceMapper func(*http.Request) []*ResourceNeed

type SimWorker struct {
	ResourceMapper ResourceMapper
	Resources      []Resource
	Clock          Clock
	Limiter        Limiter
}

func (w *SimWorker) Run(queue <-chan Task) {
	ticker, tickStop := w.Clock.Tick()

	now := w.Clock.Now()
	for {
		select {
		case task := <-queue:
			// Zero value indicates a closed channel
			if task.Request == nil {
				close(tickStop)
				return
			}
			now = w.do(ticker, task, now)
		case now = <-ticker:
		}
	}
}

func (w *SimWorker) do(ticker <-chan time.Time, task Task, start time.Time) time.Time {
	needs := w.ResourceMapper(task.Request)

	if !(w.Limiter == nil || w.Limiter.Allow(task)) {
		// Tick once to simulate a little bit of time passing to avoid
		// the clients getting into a tight loop.
		now := <-ticker
		task.Result <- Result{
			WorkStart:  start,
			End:        now,
			StatusCode: 429,
		}
		return start
	}

	var now time.Time
	for now = range ticker {
		// TODO: Handle the case where the need starts at zero
		need := needs[0]

		// TODO: optimize this with a map
		for _, res := range w.Resources {
			if res.Name() != need.Name {
				continue
			}
			//log.Printf("Asking %d of %#v at %s", need.Value, res, now)
			got := res.Ask(need.Value)

			if got > 0 {
				need.Value -= got
				break
			}
		}
		if need.Value <= 0 {
			needs = needs[1:]
		}

		if len(needs) == 0 {
			res := Result{
				StatusCode: http.StatusOK,
				WorkStart:  start,
				End:        now,
			}
			if w.Limiter != nil {
				w.Limiter.Report(task, res)
			}
			task.Result <- res
			break
		}
	}

	return now
}

type WorkerPool struct {
	Backlog int
	// TODO: Actually use timeouts
	Timeout time.Duration
	Workers []Worker

	Clock Clock
}

type poolTask struct {
	Task  Task
	Start time.Time
}

func (w *WorkerPool) Run(queue <-chan Task) {
	backlog := make(chan poolTask, w.Backlog)
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
			now := w.Clock.Now()
			select {
			case backlog <- poolTask{task, now}:
			default:
				task.Result <- Result{
					StatusCode: http.StatusServiceUnavailable,
					WorkStart:  now,
					End:        now,
				}
			}
		}
		close(backlog)
	}()

	for next := range backlog {
		ticker, tickStop := w.Clock.Tick()
		now := w.Clock.Now()
		deadline := next.Start.Add(w.Timeout)
		done := false

		for !done {
			select {
			case workerQueue <- next.Task:
				done = true
			case now = <-ticker:
				if now.After(deadline) {
					next.Task.Result <- Result{
						StatusCode: http.StatusGatewayTimeout,
						WorkStart:  now,
						End:        now,
					}
					done = true
				}
			}
		}
		close(tickStop)
	}
	close(workerQueue)
	wg.Wait()
}
