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

func listOverload() {
	var keys []string
	if envKeys := os.Getenv("STRIPE_KEYS"); envKeys != "" {
		keys = strings.Split(envKeys, ",")
	} else {
		keys = []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M", "N", "O", "P"}
	}

	start := time.Now()
	workerCnt := 15
	cfg := WorkerConfig{
		Duration:       60 * time.Second,
		Workers:        workerCnt,
		CPUs:           2,
		Hosts:          2,
		Backlog:        200,
		Timeout:        40 * time.Second,
		WallClockBurst: time.Duration(5*workerCnt) * time.Second,
		WallClockRate:  time.Duration(workerCnt) * time.Second,
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

	for _, listAgents := range []int{1, 10, 60} {
		for _, createInterval := range []time.Duration{0, 3 * time.Second} {
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
