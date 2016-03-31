package main

import (
	"fmt"
	"log"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/metcalf/loadsim"
)

func workerCounts() {
	start := time.Now()
	cpus := 4
	hosts := 4
	duration := 180 * time.Second
	fuckeryDelay := duration / 6
	fuckeryDuration := duration - fuckeryDelay*2
	cfg := WorkerConfig{
		Duration: duration,
		CPUs:     cpus,
		Hosts:    hosts,
		Backlog:  hosts * 100,
		Timeout:  40 * time.Second,
	}
	prefix := "data"

	clk := loadsim.NewSimClock()

	normalMapper := func(req *http.Request) []loadsim.ResourceNeed {
		if req.Method == "GET" {
			total := 3000
			wall := total / 5
			return []loadsim.ResourceNeed{
				{"time", wall},
				{"CPU", total - wall},
			}
		}

		total := 1350
		cpu := total / 3
		return []loadsim.ResourceNeed{
			{"time", total - cpu},
			{"CPU", cpu},
		}
	}

	listreq, err := http.NewRequest("GET", "https://qa-api.stripe.com/v1/charges?limit=100", nil)
	if err != nil {
		log.Fatal(err)
	}
	listreq.SetBasicAuth("list", "")

	var listAgents []loadsim.Agent
	for i := 0; i < (hosts * cpus * 2); i++ {
		listAgents = append(listAgents, buildAgent(clk, "list", listreq, nil, 0))
	}

	kerfuckery := []struct {
		Name           string
		Agents         []loadsim.Agent
		ResourceMapper loadsim.ResourceMapper
	}{
		//{"none", nil, nil},

		// 10% of charges see a 20 second delay in the response
		/*{
			"charge-net-delay",
			nil,
			func(req *http.Request) []loadsim.ResourceNeed {
				needs := normalMapper(req)
				if req.Method == "POST" && req.URL.Path == "/v1/charges" && rand.Float32() < 0.1 {
					needs = append(needs, &loadsim.ResourceNeed{"time", 20000})
				}
				return needs
			},
		},*/

		// 100% of charges see a 1.25 second delay in the response
		/*{
			"charge-scoring-delay",
			nil,
			func(req *http.Request) []loadsim.ResourceNeed {
				needs := normalMapper(req)
				if req.Method == "POST" && req.URL.Path == "/v1/charges" {
					needs = append(needs, &loadsim.ResourceNeed{"time", 1250})
				}
				return needs
			},
		},*/

		// A bunch of expensive list queries wreck havock
		{"list", listAgents, nil},
	}

	for _, workerCnt := range []int{cpus + cpus/2, 2 * cpus, 4 * cpus, 8 * cpus} {
		cfg.Workers = workerCnt
		cfg.WallClockRate = time.Duration(workerCnt*hosts) / 2 * time.Second
		cfg.WallClockBurst = cfg.WallClockRate * 5

		for _, fuckery := range kerfuckery {
			var agents []loadsim.Agent

			for i := 0; i < hosts*cpus*3/2; i++ {
				req, body := chargeCreate(strconv.Itoa(i))
				agents = append(agents, buildAgent(clk, "charge", req, body, 3*time.Second))
			}

			for i, newAgent := range fuckery.Agents {
				agents = append(agents, &loadsim.DelayLimitAgent{
					Agent: newAgent,
					Delay: fuckeryDelay + duration/1000*time.Duration(i),
					Limit: duration - fuckeryDelay*2,
					Clock: clk,
				})
			}
			if fuckery.ResourceMapper != nil {
				beginFuckery := clk.Now().Add(fuckeryDelay)
				endFuckery := beginFuckery.Add(fuckeryDuration)
				cfg.ResourceMapper = func(req *http.Request) []loadsim.ResourceNeed {
					now := clk.Now()
					if now.After(beginFuckery) && now.Before(endFuckery) {
						return fuckery.ResourceMapper(req)
					}

					return normalMapper(req)
				}
			} else {
				cfg.ResourceMapper = normalMapper
			}

			results := simRun(cfg, agents, clk)

			path := path.Join(prefix, fmt.Sprintf("%d_%dworkers_%s.tsv", start.Unix(), workerCnt*hosts, fuckery.Name))
			if err := writeResults(results, path); err != nil {
				log.Fatal(err)
			}
		}
	}
}
