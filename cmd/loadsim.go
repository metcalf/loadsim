package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/metcalf/loadsim"
	"github.com/montanaflynn/stats"
	"github.com/olekukonko/tablewriter"
)

type WorkerConfig struct {
	Duration                      time.Duration
	WallClockRate, WallClockBurst time.Duration
	Hosts, Workers, CPUs          int
	Backlog                       int
	Timeout                       time.Duration
	ResourceMapper                loadsim.ResourceMapper
	RequestOverhead               []loadsim.ResourceNeed
}

func main() {
	workerPrediction()
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
			Delay:       100 * time.Millisecond,
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

func writeResults(results []loadsim.Result, path string) error {
	os.Stderr.WriteString("\n" + path + "\n")
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
	simClk := clk.(*loadsim.SimClock)

	var limiter loadsim.Limiter
	if cfg.WallClockRate > 0 {
		var err error
		limiter, err = loadsim.NewWallClockLimiter(cfg.WallClockBurst, cfg.WallClockRate, clk)
		if err != nil {
			panic(err)
		}
	}

	for host := 0; host < cfg.Hosts; host++ {
		cpu := &loadsim.CPUResource{Count: cfg.CPUs}
		allResources = append(allResources, cpu)

		for proc := 0; proc < cfg.Workers; proc++ {
			time := &loadsim.TimeResource{}
			allResources = append(allResources, time)
			workerResources := []loadsim.Resource{cpu, time}

			var worker loadsim.Worker
			worker = &loadsim.SimWorker{
				ResourceMapper: cfg.ResourceMapper,
				Resources:      workerResources,
				Clock:          clk,
				Limiter:        limiter,
			}

			if len(cfg.RequestOverhead) > 0 {
				worker = &loadsim.WorkerOverhead{
					Worker:    worker,
					Clock:     clk,
					Resources: workerResources,
					Needs:     cfg.RequestOverhead,
				}
			}

			workers = append(workers, worker)
		}
	}
	worker := loadsim.WorkerPool{
		Backlog: cfg.Backlog,
		Timeout: cfg.Timeout,
		Workers: workers,
		Clock:   clk,
	}

	stop := make(chan struct{})

	simClk.Hook = func() {
		for _, res := range allResources {
			res.Reset()
		}
	}

	resultCh := loadsim.Simulate(agents, &worker, clk, cfg.Duration)
	go simClk.Run(stop)

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
	table := tablewriter.NewWriter(out)

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

	header := []string{"Agent", "total"}
	for _, code := range codes {
		header = append(header, strconv.Itoa(code))
	}
	header = append(header, "time(p50/p90/p99)", "work time(p50/p90/p99)")
	table.SetHeader(header)

	for _, key := range keys {
		var line []string
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

		line = append(line, key, strconv.Itoa(len(data)))
		for _, code := range codes {
			line = append(line, strconv.Itoa(codeCounts[code]))
		}
		line = append(line, timeStr, workTimeStr)

		table.Append(line)
	}

	table.SetBorder(false)
	table.Render()
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
