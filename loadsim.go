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
	StatusCode int
	Start, End time.Time
	Err        error
}

func Simulate(agents []Agent, worker HTTPWorker) map[Agent][]Result {
	workerDone := make(chan struct{})
	agentStop := make(chan struct{})
	queue := make(chan Task)

	var wg sync.WaitGroup
	type agentResult struct {
		Agent  Agent
		Result Result
	}
	results := make(chan agentResult)
	allResults := make(map[Agent][]Result, len(agents))

	go func() {
		worker.Run(queue)
		close(workerDone)
	}()

	for _, agent := range agents {
		go func(a Agent) {
			wg.Add(1)
			for res := range a.Run(queue, agentStop) {
				results <- agentResult{a, res}
			}
			wg.Done()
		}(agent)
	}

	go func() {
		timeout := time.After(time.Second * 5)
		select {
		case <-agentStop:
			break
		case <-timeout:
			close(agentStop)
		}

		// Wait for all agents to stop
		wg.Wait()
		// Close the queue to stop the worker
		close(queue)
		// Wait for the worker to stop
		<-workerDone
		// Stop listening for results
		close(results)
	}()

	for ares := range results {
		res := ares.Result
		allResults[ares.Agent] = append(allResults[ares.Agent], res)
		if res.Err != nil {
			log.Printf("Ack! error %v", res.Err)
			close(agentStop)
		}
	}

	return allResults
}
