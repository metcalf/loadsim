package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
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
			{"time", 1000},
			{"CPU", 2500},
		}, nil
	}

	return []*loadsim.ResourceNeed{
		{"time", 1000},
		{"CPU", 500},
	}, nil
}

func buildAgents(clk loadsim.Clock) []loadsim.Agent {
	var agents []loadsim.Agent

	var keys []string
	if envKeys := os.Getenv("STRIPE_KEYS"); envKeys != "" {
		keys = strings.Split(envKeys, ",")
	} else {
		keys = []string{"list", "ch1", "ch2", "ch3", "ch4", "ch5", "ch6"}
	}

	listreq, err := http.NewRequest("GET", "https://qa-api.stripe.com/v1/charges/?limit=100", nil)
	if err != nil {
		log.Fatal(err)
	}
	listreq.SetBasicAuth(keys[0], "")

	for i := 0; i < 60; i++ {
		agent := &loadsim.RepeatAgent{
			BaseRequest: listreq,
			Clock:       clk,
			ID:          keys[0], //fmt.Sprintf("%s %s", listreq.Method, listreq.URL.String()),
			Delay:       100 * time.Millisecond,
		}
		delay := 20*time.Second + time.Millisecond*time.Duration(i*300)

		agents = append(agents, &loadsim.DelayLimitAgent{
			Agent: agent,
			Delay: delay,
			Limit: 90 * time.Second,
			Clock: clk,
		})
	}

	for i := 0; i < 6; i++ {
		createreq, err := http.NewRequest("POST", "https://qa-api.stripe.com/v1/charges", nil)
		if err != nil {
			log.Fatal(err)
		}
		createreq.SetBasicAuth(keys[i+1], "")

		agents = append(agents, &loadsim.IntervalAgent{
			BaseRequest: createreq,
			Clock:       clk,
			ID:          keys[i+1], //fmt.Sprintf("%s %s", createreq.Method, createreq.URL.String()),
			Interval:    2000 * time.Millisecond,
		})
	}

	return agents
}

func main() {
	simRun()
}

func simRun() {
	clk := loadsim.NewSimClock()

	agents := buildAgents(clk)

	var workers []loadsim.Worker
	var allResources []loadsim.Resource

	workerCnt := 16
	limiter, err := loadsim.NewWallClockLimiter(
		time.Duration(5*workerCnt)*time.Second,
		time.Duration(workerCnt)*time.Second,
		clk,
	)
	if err != nil {
		panic(err)
	}

	for host := 0; host < 2; host++ {
		cpu := &loadsim.CPUResource{Count: 2}
		allResources = append(allResources, cpu)

		for proc := 0; proc < workerCnt; proc++ {
			time := &loadsim.TimeResource{}
			allResources = append(allResources, time)

			workers = append(workers, &loadsim.SimWorker{
				ResourceMap: &ResourceMapper{},
				Resources:   []loadsim.Resource{cpu, time},
				Clock:       clk,
				Limiter:     limiter,
			})
		}
	}
	worker := loadsim.WorkerPool{
		Backlog: 200,
		Timeout: 40 * time.Second,
		Workers: workers,
		Clock:   clk,
	}

	stop := make(chan struct{})

	ticker, tickStop := clk.Tick()
	go func() {
		for {
			select {
			case <-stop:
				close(tickStop)
				return
			case <-ticker:
			}

			for _, res := range allResources {
				res.Reset()
			}
		}
	}()

	resultCh := loadsim.Simulate(agents, &worker, clk, 180*time.Second)
	go clk.Run(stop)

	results := collect(resultCh)
	close(stop)

	summarize(results, os.Stderr)
	output(results, os.Stdout)
}

func httpRun() {
	var clk loadsim.WallClock
	agents := buildAgents(&clk)

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

	var keys []string
	for key := range keyed {
		keys = append(keys, key)
	}

	sort.Sort(sort.StringSlice(keys))

	codes := make([]int, 0, len(codeSet))
	for code := range codeSet {
		codes = append(codes, code)
	}

	io.WriteString(out, "Agent\ttotal")
	for _, code := range codes {
		fmt.Fprintf(out, "\t%d", code)
	}
	io.WriteString(out, "\ttime(p50/p90/p99)\twork time(p50/p90/p99)\n")

	for _, key := range keys {
		data := keyed[key]

		codeCounts := make(map[int]int, len(codes))
		times := make([]float64, len(data))
		var workTimes []float64

		for i, datum := range data {
			times[i] = datum.End.Sub(datum.Start).Seconds() * 1000
			if datum.StatusCode == 200 {
				workTimes = append(workTimes, datum.End.Sub(datum.WorkStart).Seconds()*1000)
			}
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

		fmt.Fprintf(out, "%s\t%d", key, len(data))
		for _, code := range codes {
			fmt.Fprintf(out, "\t%d", codeCounts[code])
		}
		fmt.Fprintf(out, "\t%s\t%s", timeStr, workTimeStr)
		io.WriteString(out, "\n")
	}
}

func statString(data []float64) (string, error) {
	strs := make([]string, 3)

	for i, pct := range []float64{50, 90, 99} {
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
