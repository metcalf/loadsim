package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
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
			{"time", 500},
			{"CPU", 2000},
		}, nil
	}

	return []*loadsim.ResourceNeed{
		{"time", 1200},
		{"CPU", 400},
	}, nil
}

type RunConfig struct {
	ListAgents, CreateAgents int
	Duration, CreateInterval time.Duration
}

func buildAgents(clk loadsim.Clock, cfg RunConfig) []loadsim.Agent {
	var agents []loadsim.Agent

	var keys []string
	if envKeys := os.Getenv("STRIPE_KEYS"); envKeys != "" {
		keys = strings.Split(envKeys, ",")
	} else {
		keys = []string{"list", "ch1", "ch2", "ch3", "ch4", "ch5", "ch6"}
	}

	listreq, err := http.NewRequest("GET", "https://qa-api.stripe.com/v1/charges?limit=100", nil)
	if err != nil {
		log.Fatal(err)
	}
	listreq.SetBasicAuth(keys[0], "")

	for i := 0; i < cfg.ListAgents; i++ {
		id := fmt.Sprintf("%s %s", listreq.Method, listreq.URL.Path)
		// id := keys[0]
		agent := &loadsim.RepeatAgent{
			BaseRequest: listreq,
			Clock:       clk,
			ID:          id,
			Delay:       100 * time.Millisecond,
		}
		delay := 20*time.Second + time.Millisecond*time.Duration(i*300)

		agents = append(agents, &loadsim.DelayLimitAgent{
			Agent: agent,
			Delay: delay,
			Limit: 60 * time.Second,
			Clock: clk,
		})
	}

	for i := 0; i < cfg.CreateAgents; i++ {
		key := keys[i+1]

		var card string
		if strings.HasPrefix(key, "sk_test") {
			card = "4242424242424242"
		} else {
			card = "4024007134427763" // randomly generated
		}

		body := []byte(url.Values{
			"capture":           {"false"},
			"amount":            {"400"},
			"currency":          {"usd"},
			"source[number]":    {card},
			"source[exp_year]":  {"2030"},
			"source[exp_month]": {"01"},
			"source[cvc]":       {"123"},
			"source[object]":    {"card"},
		}.Encode())

		createreq, err := http.NewRequest("POST", "https://qa-api.stripe.com/v1/charges", nil)
		if err != nil {
			log.Fatal(err)
		}
		createreq.SetBasicAuth(key, "")

		id := fmt.Sprintf("%s %s", createreq.Method, createreq.URL.Path)
		// id := key

		var agent loadsim.Agent
		if cfg.CreateInterval == 0 {
			agent = &loadsim.RepeatAgent{
				BaseRequest: createreq,
				Body:        body,
				Clock:       clk,
				ID:          id,
			}
		} else {
			agent = &loadsim.IntervalAgent{
				BaseRequest: createreq,
				Body:        body,
				Clock:       clk,
				ID:          id,
				Interval:    cfg.CreateInterval,
			}
		}
		agents = append(agents, agent)
	}

	return agents
}

func main() {
	start := time.Now()
	cfg := RunConfig{
		Duration:     90 * time.Second,
		CreateAgents: 6,
	}
	prefix := "loaddata"

	runners := []struct {
		Name string
		Run  func(RunConfig) []loadsim.Result
	}{
		{"sim", simRun},
		{"http", httpRun},
	}

	for _, listAgents := range []int{1, 10, 60} {
		cfg.ListAgents = listAgents

		for _, createInterval := range []time.Duration{0, 3 * time.Second} {
			cfg.CreateInterval = createInterval
			var rep string
			if createInterval == 0 {
				rep = "repeat"
			} else {
				rep = createInterval.String()
			}

			for _, runner := range runners {
				results := runner.Run(cfg)

				label := fmt.Sprintf("%d_%s_%dlist@%s", start.Unix(), runner.Name, listAgents, rep)
				os.Stderr.WriteString(label + "\n")
				summarize(results, os.Stderr)
				os.Stderr.WriteString("\n")

				file, err := os.Create(path.Join(prefix, label+".tsv"))
				if err != nil {
					log.Fatalf("%s: %v", label, err)
				}

				output(results, file)

				if err := file.Close(); err != nil {
					log.Fatalf("%s: %v", label, err)
				}
			}
		}
	}
}

func simRun(cfg RunConfig) []loadsim.Result {
	clk := loadsim.NewSimClock()

	agents := buildAgents(clk, cfg)

	var workers []loadsim.Worker
	var allResources []loadsim.Resource

	workerCnt := 15
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

	resultCh := loadsim.Simulate(agents, &worker, clk, cfg.Duration)
	go clk.Run(stop)

	results := collect(resultCh)
	close(stop)

	return results
}

func httpRun(cfg RunConfig) []loadsim.Result {
	var clk loadsim.WallClock
	agents := buildAgents(&clk, cfg)

	resultCh := loadsim.Simulate(agents, &loadsim.HTTPWorker{
		Clock: &clk,
	}, &clk, cfg.Duration)
	return collect(resultCh)
}

func collect(resultCh <-chan loadsim.Result) []loadsim.Result {
	var results []loadsim.Result
	for r := range resultCh {
		if r.Err != nil {
			log.Printf("%s: %s", r.AgentID, r.Err)
		} else {
			results = append(results, r)
		}
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
	sort.Sort(sort.IntSlice(codes))

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

		var workTimeStr string
		if len(workTimes) > 0 {
			workTimeStr, err = statString(workTimes)
			if err != nil {
				panic(err)
			}
		} else {
			workTimeStr = "-/-/-"
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
