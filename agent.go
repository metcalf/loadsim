package loadsim

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"
	"time"
)

type Agent interface {
	Run(chan<- Task, chan<- Result, <-chan struct{})
}

type DelayLimitAgent struct {
	Agent Agent
	Clock Clock
	Delay time.Duration
	Limit time.Duration
}

func (a *DelayLimitAgent) Run(queue chan<- Task, results chan<- Result, stop <-chan struct{}) {
	ticker, tickStop := a.Clock.Tick()
	now := a.Clock.Now()

	start := now.Add(a.Delay)
	for now.Before(start) {
		select {
		case <-stop:
			close(tickStop)
			return
		case now = <-ticker:
		}
	}

	end := now.Add(a.Limit)
	subStop := make(chan struct{})

	go func() {
		for now.Before(end) {
			select {
			case <-stop:
				close(subStop)
				close(tickStop)
				return
			case now = <-ticker:
			}
		}
		close(subStop)
		close(tickStop)
	}()

	a.Agent.Run(queue, results, subStop)
}

type RepeatAgent struct {
	BaseRequest *http.Request
	Body        []byte
	Clock       Clock
	Delay       time.Duration // Delay between requests
	ID          string
}

func (a *RepeatAgent) Run(queue chan<- Task, results chan<- Result, stop <-chan struct{}) {
	ticker, tickStop := a.Clock.Tick()
	now := a.Clock.Now()

	// Force at least a millisecond between requests
	if a.Delay == 0 {
		a.Delay = time.Millisecond
	}

	for {
		start := now
		task := Task{copyRequest(a.BaseRequest, a.Body), make(chan Result, 1)}

		queued := false
		for !queued {
			select {
			case queue <- task:
				queued = true
			case now = <-ticker:
			}
		}

		done := false
		for !done {
			select {
			case res := <-task.Result:
				res.Start = start
				res.AgentID = a.ID
				// We assume this is never blocking on the clock
				results <- res
				done = true
			case now = <-ticker:
			}
		}

		end := now.Add(a.Delay)
		for now.Before(end) {
			select {
			case <-stop:
				close(tickStop)
				return
			case now = <-ticker:
			}
		}
	}
}

func (a *RepeatAgent) String() string {
	return fmt.Sprintf("RepeatAgent(%s %s)", a.BaseRequest.Method, a.BaseRequest.URL.String())
}

type IntervalAgent struct {
	BaseRequest *http.Request
	Body        []byte
	Clock       Clock
	Interval    time.Duration
	ID          string
}

func (a *IntervalAgent) Run(queue chan<- Task, results chan<- Result, stop <-chan struct{}) {
	var wg sync.WaitGroup

	for {
		start := a.Clock.Now()

		task := Task{copyRequest(a.BaseRequest, a.Body), make(chan Result, 1)}
		queue <- task

		wg.Add(1)
		go func(rc <-chan Result) {
			res := <-rc
			res.Start = start
			res.AgentID = a.ID
			results <- res

			wg.Done()
		}(task.Result)

		end := start.Add(a.Interval)
		now := start

		ticker, tickStop := a.Clock.Tick()
		for now.Before(end) {
			select {
			case <-stop:
				close(tickStop)
				wg.Wait()
				return
			case now = <-ticker:
			}
		}
		close(tickStop)
	}
}

func (a *IntervalAgent) String() string {
	return fmt.Sprintf("IntervalAgent(%s %s every %s)", a.BaseRequest.Method, a.BaseRequest.URL.String(), a.Interval)
}

func copyRequest(orig *http.Request, body []byte) *http.Request {
	copy := *orig

	copy.Header = make(http.Header)
	for k, vv := range orig.Header {
		for _, v := range vv {
			copy.Header.Add(k, v)
		}
	}

	if body != nil {
		copy.Body = ioutil.NopCloser(bytes.NewReader(body))
	}

	return &copy
}
