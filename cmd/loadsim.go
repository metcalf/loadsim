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

type WorkerConfig struct {
	Duration time.Duration
}

func chargeCreate(key string) (*http.Request, []byte) {
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

	req, err := http.NewRequest("POST", "https://qa-api.stripe.com/v1/charges", nil)
	if err != nil {
		log.Fatal(err)
	}
	req.SetBasicAuth(key, "")

	return req, body
}

func buildAgent(clk loadsim.Clock, id string, req *http.Request, body []byte, interval time.Duration) loadsim.Agent {
	if interval == 0 {
		return &loadsim.RepeatAgent{
			BaseRequest: req,
			Body:        body,
			Clock:       clk,
			ID:          id,
		}
	}

	return &loadsim.IntervalAgent{
		BaseRequest: req,
		Body:        body,
		Clock:       clk,
		ID:          id,
		Interval:    interval,
	}
}

func main() {
	var keys []string
	if envKeys := os.Getenv("STRIPE_KEYS"); envKeys != "" {
		keys = strings.Split(envKeys, ",")
	} else {
		keys = []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M", "N", "O", "P"}
	}

	start := time.Now()
	cfg := WorkerConfig{
		Duration: 60 * time.Second,
	}
	prefix := "data"

	simClock := loadsim.NewSimClock()

	runners := []struct {
		Name  string
		Run   func(WorkerConfig, []loadsim.Agent, loadsim.Clock) []loadsim.Result
		Clock loadsim.Clock
	}{
		{"sim", simRun, simClock},
		//{"http", httpRun, &loadsim.WallClock{}},
	}

	listreq, err := http.NewRequest("GET", "https://qa-api.stripe.com/v1/charges?limit=100", nil)
	if err != nil {
		log.Fatal(err)
	}
	listreq.SetBasicAuth(keys[0], "")

	for _, listAgents := range []int{1} {
		for _, createInterval := range []time.Duration{0} {
			var rep string
			if createInterval == 0 {
				rep = "repeat"
			} else {
				rep = createInterval.String()
			}

			for _, runner := range runners {
				var agents []loadsim.Agent

				for i := 0; i < listAgents; i++ {
					id := fmt.Sprintf("%s %s", listreq.Method, listreq.URL.Path)
					delay := 20*time.Second + time.Millisecond*time.Duration(i*300)

					agents = append(agents, &loadsim.DelayLimitAgent{
						Agent: buildAgent(runner.Clock, id, listreq, nil, 0),
						Delay: delay,
						Limit: 60 * time.Second,
						Clock: runner.Clock,
					})
				}

				for i := 0; i < 6; i++ {
					req, body := chargeCreate(keys[i+1])
					id := fmt.Sprintf("%s %s", req.Method, req.URL.Path)
					agents = append(agents, buildAgent(runner.Clock, id, req, body, createInterval))
				}

				results := runner.Run(cfg, agents, runner.Clock)

				path := path.Join(prefix, fmt.Sprintf("%d_%s_%dlist@%s.tsv", start.Unix(), runner.Name, listAgents, rep))
				if err := writeResults(results, path); err != nil {
					log.Fatal(err)
				}
			}
		}
	}
}

func writeResults(results []loadsim.Result, path string) error {
	os.Stderr.WriteString(path + "\n")
	summarize(results, os.Stderr)
	os.Stderr.WriteString("\n")

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}

	output(results, file)

	if err := file.Close(); err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}

	return nil
}

func simRun(cfg WorkerConfig, agents []loadsim.Agent, clk loadsim.Clock) []loadsim.Result {
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
	go clk.(*loadsim.SimClock).Run(stop)

	results := collect(resultCh)
	close(stop)

	return results
}

func httpRun(cfg WorkerConfig, agents []loadsim.Agent, clk loadsim.Clock) []loadsim.Result {
	resultCh := loadsim.Simulate(agents, &loadsim.HTTPWorker{
		Clock: clk,
	}, clk, cfg.Duration)
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
