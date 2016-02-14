package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/metcalf/loadsim"
	"github.com/montanaflynn/stats"
)

func main() {
	badreq, err := http.NewRequest("GET", "https://qa-api.stripe.com", nil)
	if err != nil {
		log.Fatal(err)
	}

	getreq, err := http.NewRequest("GET", "https://api.stripe.com/v1/charges/ch_17eCuA2eZvKYlo2CKz71JW4V", nil)
	if err != nil {
		log.Fatal(err)
	}
	getreq.SetBasicAuth(os.Getenv("STRIPE_KEY"), "")

	agents := []loadsim.Agent{
		&loadsim.RepeatAgent{
			BaseRequest: getreq,
			Clock:       &loadsim.WallClock{},
		},
		&loadsim.RepeatAgent{
			BaseRequest: badreq,
			Clock:       &loadsim.WallClock{},
		},
		&loadsim.IntervalAgent{
			BaseRequest: badreq,
			Interval:    time.Second * 2,
			Clock:       &loadsim.WallClock{},
		},
		&loadsim.IntervalAgent{
			BaseRequest: badreq,
			Interval:    time.Millisecond * 50,
			Clock:       &loadsim.WallClock{},
		},
	}

	results := loadsim.Simulate(agents, loadsim.HTTPWorker{
		Clock: &loadsim.WallClock{},
	})

	allCodes := make(map[int]struct{})
	for _, data := range results {
		for _, datum := range data {
			allCodes[datum.StatusCode] = struct{}{}
		}
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
		data := results[agent]

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
