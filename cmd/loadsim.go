package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/metcalf/loadsim"
	"github.com/montanaflynn/stats"
)

type ResourceMapper struct{}

func (r *ResourceMapper) RequestNeeds(req *http.Request) ([]*loadsim.ResourceNeed, error) {
	return []*loadsim.ResourceNeed{
		{"time", 9},
		{"CPU", 1},
	}, nil
}

func buildAgents(clocker func() loadsim.Clock) []loadsim.Agent {
	/*badreq, err := http.NewRequest("GET", "https://qa-api.stripe.com", nil)
	if err != nil {
		log.Fatal(err)
	}*/

	getreq, err := http.NewRequest("GET", "https://qa-api.stripe.com/v1/charges/ch_17eCuA2eZvKYlo2CKz71JW4V", nil)
	if err != nil {
		log.Fatal(err)
	}
	getreq.SetBasicAuth(os.Getenv("STRIPE_KEY"), "")

	agents := []loadsim.Agent{
		&loadsim.RepeatAgent{
			BaseRequest: getreq,
			Clock:       clocker(),
		},
		/*&loadsim.RepeatAgent{
			BaseRequest: badreq,
			Clock:       clocker(),
		},
		&loadsim.IntervalAgent{
			BaseRequest: badreq,
			Interval:    time.Second * 2,
			Clock:       clocker(),
		},
		&loadsim.IntervalAgent{
			BaseRequest: badreq,
			Interval:    time.Millisecond * 50,
			Clock:       clocker(),
		},*/
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

	resources := []loadsim.Resource{
		&loadsim.TimeResource{},
		&loadsim.CPUResource{Count: 1},
	}
	worker := loadsim.SimWorker{
		ResourceMap: &ResourceMapper{},
		Resources:   resources,
		Clock:       sim.Clock(),
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

			for _, res := range resources {
				res.Reset()
			}
			<-ticker2
		}
	}()

	results := loadsim.Simulate(agents, &worker, sim.Clock())
	sim.Run(stop)

	outputResults(results, agents)
	close(stop)
}

func httpRun() {
	agents := buildAgents(newWallClock)

	results := loadsim.Simulate(agents, &loadsim.HTTPWorker{
		Clock: &loadsim.WallClock{},
	}, &loadsim.WallClock{})

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
