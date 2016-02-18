package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/metcalf/loadsim"
	"github.com/montanaflynn/stats"
)

type ResourceMapper struct{}

func (r *ResourceMapper) RequestNeeds(req *http.Request) ([]*loadsim.ResourceNeed, error) {
	return []*loadsim.ResourceNeed{
		{"time", 5000},
		{"CPU", 5000},
	}, nil
}

func buildAgents(clocker func() loadsim.Clock) []loadsim.Agent {
	listreq, err := http.NewRequest("GET", "https://qa-api.stripe.com/v1/charges/?limit=100", nil)
	if err != nil {
		log.Fatal(err)
	}
	listreq.SetBasicAuth(os.Getenv("STRIPE_KEY"), "")

	agents := make([]loadsim.Agent, 60)
	for i := range agents {
		agents[i] = &loadsim.RepeatAgent{
			BaseRequest: listreq,
			Clock:       clocker(),
		}
	}

	return agents
}

func main() {
	simRun()
}

func simRun() {
	var sim loadsim.SimClock
	resClock1 := sim.Clock()

	agents := buildAgents(sim.Clock)

	var workers []loadsim.Worker
	var allResources []loadsim.Resource

	for host := 0; host < 2; host++ {
		cpu := &loadsim.CPUResource{Count: 4}
		allResources = append(allResources, cpu)

		for proc := 0; proc < 14; proc++ {
			time := &loadsim.TimeResource{}
			allResources = append(allResources, time)

			workers = append(workers, &loadsim.SimWorker{
				ResourceMap: &ResourceMapper{},
				Resources:   []loadsim.Resource{cpu, time},
				Clock:       sim.Clock(),
			})
		}
	}
	worker := loadsim.WorkerPool{
		Backlog: 200,
		Timeout: 60 * time.Second,
		Workers: workers,
		Clock:   sim.Clock(),
	}

	stop := make(chan struct{})

	resClock2 := sim.Clock()
	// WTF this has got to be some kind of concurrency joke
	ticker1 := resClock1.Tick()
	ticker2 := resClock2.Tick()
	go func() {
		for {
			select {
			case <-stop:
				resClock1.Done()
				resClock2.Done()
				return
			case <-ticker1:
			}

			for _, res := range allResources {
				res.Reset()
			}
			<-ticker2
		}
	}()

	results := loadsim.Simulate(agents, &worker, sim.Clock(), 120*time.Second)
	sim.Run(stop)

	outputResults(results, agents)
	close(stop)
}

func httpRun() {
	agents := buildAgents(newWallClock)

	results := loadsim.Simulate(agents, &loadsim.HTTPWorker{
		Clock: &loadsim.WallClock{},
	}, &loadsim.WallClock{}, 100*time.Second)

	outputResults(results, agents)
}

func outputResults(results <-chan loadsim.AgentResult, agents []loadsim.Agent) {
	allResults := make(map[loadsim.Agent][]loadsim.Result, len(agents))
	allCodes := make(map[int]struct{})
	for ares := range results {
		res := ares.Result
		allResults[ares.Agent] = append(allResults[ares.Agent], res)
		allCodes[res.StatusCode] = struct{}{}
	}

	codes := make([]int, 0, len(allCodes))
	for code := range allCodes {
		codes = append(codes, code)
	}

	os.Stdout.WriteString("Agent\tp50\tp90\tcount")
	for _, code := range codes {
		fmt.Fprintf(os.Stdout, "\t%d", code)
	}
	os.Stdout.WriteString("\n")

	for _, agent := range agents {
		data := allResults[agent]

		codeCounts := make(map[int]int, len(codes))
		durations := make([]float64, len(data))
		for i, datum := range data {
			durations[i] = datum.End.Sub(datum.Start).Seconds() * 1000
			codeCounts[datum.StatusCode]++
		}
		med, err := stats.Median(durations)
		if err != nil {
			log.Fatal(err)
		}

		p90, err := stats.Percentile(durations, 90)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Fprintf(os.Stdout, "%s\t%f\t%f\t%d", agent, med, p90, len(data))
		for _, code := range codes {
			fmt.Fprintf(os.Stdout, "\t%d", codeCounts[code])
		}
		os.Stdout.WriteString("\n")
	}
}

func newWallClock() loadsim.Clock {
	return &loadsim.WallClock{}
}
