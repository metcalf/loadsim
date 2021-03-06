package loadsim

import (
	"net/http"
	"sync"
	"time"
)

type Task struct {
	Request *http.Request
	Result  chan Result
}

type Result struct {
	StatusCode            int
	Start, WorkStart, End time.Time
	Err                   error
	AgentID               string
}

func Simulate(agents []Agent, worker Worker, clock Clock, duration time.Duration) <-chan Result {
	workerDone := make(chan struct{})
	agentStop := make(chan struct{})
	queue := make(chan Task)
	results := make(chan Result)

	var wg sync.WaitGroup

	go func() {
		worker.Run(queue)
		close(workerDone)
	}()

	for _, agent := range agents {
		wg.Add(1)
		go func(a Agent) {
			a.Run(queue, results, agentStop)
			wg.Done()
		}(agent)
	}

	go func() {
		ticker, tickStop := clock.Tick()

		start := clock.Now()
		end := start.Add(duration)

		for {
			select {
			case <-agentStop:
				break
			case now := <-ticker:
				if now.Before(end) {
					continue
				}
				close(agentStop)
			}
			break
		}
		close(tickStop)

		// Wait for all agents to stop
		wg.Wait()
		// Close the queue to stop the worker
		close(queue)
		// Wait for the worker to stop
		<-workerDone
		// Stop listening for results
		close(results)
	}()

	return results
}
