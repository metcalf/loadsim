package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/metcalf/loadsim"
)

func workerPrediction() {
	keys := strings.Split(os.Getenv("STRIPE_KEYS"), ",")

	start := time.Now()
	hosts := 2
	cpusPerHost := 16
	totalCPUs := hosts * cpusPerHost
	dur := 180 * time.Second
	cfg := WorkerConfig{
		Duration: dur,
		CPUs:     cpusPerHost,
		Hosts:    hosts,
		Backlog:  200,
		Timeout:  24 * time.Second,
		ResourceMapper: func(req *http.Request) []loadsim.ResourceNeed {
			if req.Method == "GET" {
				total := 5000
				time := total / 5
				return []loadsim.ResourceNeed{
					{"time", time},
					{"CPU", total - time},
				}
			}

			total := 1000
			cpu := total / 2
			return []loadsim.ResourceNeed{
				{"time", total - cpu},
				{"CPU", cpu},
			}
		},
		RequestOverhead: []loadsim.ResourceNeed{
			{"time", 20},
			{"CPU", 40},
		},
	}
	prefix := "data"

	clk := &loadsim.WallClock{}

	listreq, err := http.NewRequest("GET", "https://qa-api.stripe.com/v1/charges?limit=100", nil)
	if err != nil {
		log.Fatal(err)
	}
	listreq.SetBasicAuth(keys[0], "")

	var listAgents []loadsim.Agent
	for i := 0; i < 4*totalCPUs; i++ {
		delay := dur/6 + time.Millisecond*time.Duration(i*300)

		listAgents = append(listAgents, &loadsim.DelayLimitAgent{
			Agent: buildAgent(clk, "list", listreq, nil, 0),
			Delay: delay,
			Limit: dur / 3 * 2,
			Clock: clk,
		})
	}

	delayAgents := make([]loadsim.Agent, len(listAgents))
	copy(delayAgents, listAgents)

	repeatAgents := make([]loadsim.Agent, len(listAgents))
	copy(repeatAgents, listAgents)

	for i := 0; i < totalCPUs/3; i++ {
		req, body := chargeCreate(keys[(i%(len(keys)-1))+1])
		delayAgents = append(delayAgents, buildAgent(clk, "charge", req, body, 2*time.Second))
		repeatAgents = append(repeatAgents, buildAgent(clk, "charge", req, body, 0))
	}

	runs := map[string][]loadsim.Agent{
		"delay":  delayAgents,
		"repeat": repeatAgents,
	}

	var readyAt time.Time

	for _, workerRatio := range []int{3, 2, 4, 1} {
		workerCnt := totalCPUs * workerRatio

		cfg.Workers = workerCnt
		cfg.WallClockRate = time.Duration(workerCnt*hosts/2) * time.Second
		cfg.WallClockBurst = 5 * cfg.WallClockRate

		updateQAConfig(cfg.Workers, cfg.WallClockRate, cfg.WallClockBurst)

		log.Printf("running for realsies")
		for name, agents := range runs {

			if readyAt.After(time.Now()) {
				sleepFor := readyAt.Sub(time.Now())
				log.Printf("Sleeping %s while rate limiters reset", sleepFor)
				time.Sleep(sleepFor)
			}

			results := httpRun(cfg, agents, clk)
			readyAt = time.Now().Add(5 * time.Minute)

			path := path.Join(prefix, fmt.Sprintf("%d_%dworkers_%s.tsv", start.Unix(), workerCnt, name))
			if err := writeResults(results, path); err != nil {
				log.Fatal(err)
			}
		}
	}
}
