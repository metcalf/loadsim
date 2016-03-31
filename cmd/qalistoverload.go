package main

import (
	"fmt"
	"log"
	"net/http"
	"path"
	"time"

	"github.com/metcalf/loadsim"
)

func qaListOverload() {
	start := time.Now()
	workerCnt := 15
	hosts := 2
	cpus := 2
	cfg := WorkerConfig{
		Duration: 210 * time.Second,
		Workers:  workerCnt,
		CPUs:     cpus,
		Hosts:    hosts,
		Backlog:  100 * hosts,
		Timeout:  40 * time.Second,
		ResourceMapper: func(req *http.Request) []loadsim.ResourceNeed {
			if req.Method == "GET" {
				return []loadsim.ResourceNeed{
					{"time", 500},
					{"CPU", 2000},
				}
			}

			return []loadsim.ResourceNeed{
				{"time", 1200},
				{"CPU", 400},
			}
		},
		RequestOverhead: []loadsim.ResourceNeed{
			{"time", 20},
			{"CPU", 20},
		},
	}
	prefix := "data"

	clk := loadsim.NewSimClock()

	listreq, err := http.NewRequest("GET", "https://qa-api.stripe.com/v1/charges?limit=100", nil)
	if err != nil {
		log.Fatal(err)
	}
	listreq.SetBasicAuth("list", "")

	var agents []loadsim.Agent

	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("%s %s", listreq.Method, listreq.URL.Path)
		delay := 30*time.Second + time.Millisecond*time.Duration(i*200)

		agents = append(agents, &loadsim.DelayLimitAgent{
			Agent: buildAgent(clk, id, listreq, nil, 0),
			Delay: delay,
			Limit: 120 * time.Second,
			Clock: clk,
		})
	}

	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("charge%d", i)
		req, body := chargeCreate(id)
		agents = append(agents, buildAgent(clk, id, req, body, 0))
	}

	cases := []struct {
		Name          string
		WallClockRate time.Duration
	}{
		{"no-limiter", 0},
		{"limiter", time.Duration(workerCnt*hosts/2) * time.Second},
	}

	for _, simcase := range cases {
		cfg.WallClockRate = simcase.WallClockRate
		cfg.WallClockBurst = 5 * cfg.WallClockRate

		results := simRun(cfg, agents, clk)

		path := path.Join(prefix, fmt.Sprintf("%d_%s_qalist.tsv", start.Unix(), simcase.Name))
		if err := writeResults(results, path); err != nil {
			log.Fatal(err)
		}
	}
}
