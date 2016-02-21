package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/metcalf/loadsim"
	"github.com/montanaflynn/stats"
)

type ResourceMapper struct{}

func (r *ResourceMapper) RequestNeeds(req *http.Request) ([]*loadsim.ResourceNeed, error) {
	if req.Method == "GET" {
		return []*loadsim.ResourceNeed{
			{"time", 500},
			{"CPU", 2500},
		}, nil
	}

	return []*loadsim.ResourceNeed{
		{"time", 1000},
		{"CPU", 500},
	}, nil
}

func buildAgents(clocker func() loadsim.Clock) []loadsim.Agent {
	var agents []loadsim.Agent

	listreq, err := http.NewRequest("GET", "https://qa-api.stripe.com/v1/charges/?limit=100", nil)
	if err != nil {
		log.Fatal(err)
	}
	listreq.SetBasicAuth(os.Getenv("STRIPE_KEY"), "")

	for i := 0; i < 50; i++ {
		agent := &loadsim.RepeatAgent{
			BaseRequest: listreq,
			Clock:       clocker(),
			ID:          fmt.Sprintf("%s %s", listreq.Method, listreq.URL.String()),
		}
		delay := 20*time.Second + time.Millisecond*time.Duration(i*300)

		agents = append(agents, &loadsim.DelayLimitAgent{
			Agent: agent,
			Delay: delay,
			Limit: 60 * time.Second,
			Clock: clocker(),
		})
	}

	createreq, err := http.NewRequest("POST", "https://qa-api.stripe.com/v1/charges", nil)
	if err != nil {
		log.Fatal(err)
	}
	createreq.SetBasicAuth(os.Getenv("STRIPE_KEY"), "")

	for i := 0; i < 5; i++ {
		agents = append(agents, &loadsim.RepeatAgent{
			BaseRequest: createreq,
			Clock:       clocker(),
			ID:          fmt.Sprintf("%s %s", createreq.Method, createreq.URL.String()),
		})
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

	resultCh := loadsim.Simulate(agents, &worker, sim.Clock(), 120*time.Second)
	sim.Run(stop)

	results := collect(resultCh)
	close(stop)

	summarize(results, os.Stderr)
	output(results, os.Stdout)
}

func httpRun() {
	agents := buildAgents(newWallClock)

	resultCh := loadsim.Simulate(agents, &loadsim.HTTPWorker{
		Clock: &loadsim.WallClock{},
	}, &loadsim.WallClock{}, 100*time.Second)
	results := collect(resultCh)

	summarize(results, os.Stderr)
	output(results, os.Stdout)
}

func collect(resultCh <-chan loadsim.Result) []loadsim.Result {
	var results []loadsim.Result
	for r := range resultCh {
		results = append(results, r)
	}

	return results
}

func output(results []loadsim.Result, out io.Writer) {
	writer := csv.NewWriter(out)
	writer.Comma = '\t'
	writer.Write([]string{
		"agent", "status_code",
		"request_start", "work_start", "end",
		"total_time", "work_time",
	})

	begin := results[0].Start
	for _, res := range results {
		if res.Start.Before(begin) {
			begin = res.Start
		}
	}

	dtos := func(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 6, 64) }

	for _, res := range results {
		writer.Write([]string{
			res.AgentID,
			strconv.Itoa(res.StatusCode),
			dtos(res.Start.Sub(begin)), dtos(res.WorkStart.Sub(begin)), dtos(res.End.Sub(begin)),
			dtos(res.End.Sub(res.Start)), dtos(res.End.Sub(res.WorkStart)),
		})
	}

	writer.Flush()
}

func summarize(results []loadsim.Result, out io.Writer) {
	keyed := make(map[string][]loadsim.Result)
	codeSet := make(map[int]struct{})
	for _, res := range results {
		keyed[res.AgentID] = append(keyed[res.AgentID], res)
		codeSet[res.StatusCode] = struct{}{}
	}

	codes := make([]int, 0, len(codeSet))
	for code := range codeSet {
		codes = append(codes, code)
	}

	io.WriteString(out, "Agent\ttime(p50/p90/p95)\twork time(p50/p90/p95)\tcount")
	for _, code := range codes {
		fmt.Fprintf(out, "\t%d", code)
	}
	io.WriteString(out, "\n")

	for key, data := range keyed {
		codeCounts := make(map[int]int, len(codes))
		times := make([]float64, len(data))
		workTimes := make([]float64, len(data))

		for i, datum := range data {
			times[i] = datum.End.Sub(datum.Start).Seconds() * 1000
			workTimes[i] = datum.End.Sub(datum.WorkStart).Seconds() * 1000
			codeCounts[datum.StatusCode]++
		}

		timeStr, err := statString(times)
		if err != nil {
			panic(err)
		}

		workTimeStr, err := statString(workTimes)
		if err != nil {
			panic(err)
		}

		fmt.Fprintf(out, "%s\t%s\t%s\t%d", key, timeStr, workTimeStr, len(data))
		for _, code := range codes {
			fmt.Fprintf(out, "\t%d", codeCounts[code])
		}
		io.WriteString(out, "\n")
	}
}

func statString(data []float64) (string, error) {
	strs := make([]string, 3)

	for i, pct := range []float64{50, 90, 95} {
		stat, err := stats.Percentile(data, pct)
		if err != nil {
			return "", err
		}
		strs[i] = strconv.FormatFloat(stat, 'f', 2, 64)
	}

	return strings.Join(strs, "/"), nil
}

func newWallClock() loadsim.Clock {
	return &loadsim.WallClock{}
}
