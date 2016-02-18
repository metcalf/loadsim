package loadsim

import (
	"log"
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
}

type AgentResult struct {
	Agent
	Result
}

func Simulate(agents []Agent, worker Worker, clock Clock, duration time.Duration) <-chan AgentResult {
	workerDone := make(chan struct{})
	agentStop := make(chan struct{})
	queue := make(chan Task)

	var wg sync.WaitGroup

	results := make(chan AgentResult)

	go func() {
		worker.Run(queue)
		close(workerDone)
	}()

	for _, agent := range agents {
		go func(a Agent) {
			wg.Add(1)
			for res := range a.Run(queue, agentStop) {
				results <- AgentResult{a, res}
				if res.Err != nil {
					log.Printf("Ack! error %v", res.Err)
					close(agentStop)
				}
			}
			wg.Done()
		}(agent)
	}

	go func() {
		ticker := clock.Tick()

		start := <-ticker
		end := start.Add(duration)

		for {
			select {
			case <-agentStop:
				break
			case now := <-ticker:
				if now.Before(end) {
					continue
				}
				log.Printf("time up")
				close(agentStop)
			}
			break
		}
		clock.Done()

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
