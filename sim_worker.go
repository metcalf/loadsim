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

type ResourceMap interface {
	RequestNeeds(*http.Request) ([]*ResourceNeed, error)
}

type SimWorker struct {
	ResourceMap ResourceMap
	Resources   []Resource
	Clock       Clock
}

func (w *SimWorker) Run(queue <-chan Task) {
	ticker := w.Clock.Tick()

	now := w.Clock.Now()
	for {
		select {
		case task := <-queue:
			// Zero value indicates a closed channel
			if task.Request == nil {
				w.Clock.Done()
				return
			}
			now = w.do(ticker, task, now)
		case now = <-ticker:
		}
	}
}

func (w *SimWorker) do(ticker <-chan time.Time, task Task, start time.Time) time.Time {
	needs, err := w.ResourceMap.RequestNeeds(task.Request)
	if err != nil {
		task.Result <- Result{
			Err:       err,
			WorkStart: start,
			End:       start,
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
			task.Result <- Result{
				// TODO: Eventually incorporate 503 and 429
				StatusCode: 200,
				WorkStart:  start,
				End:        now,
			}
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
