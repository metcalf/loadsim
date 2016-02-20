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
	Run(chan<- Task, <-chan struct{}) <-chan Result
}

type DelayLimitAgent struct {
	Agent Agent
	Clock Clock
	Delay time.Duration
	Limit time.Duration
}

type RepeatAgent struct {
	BaseRequest *http.Request
	Body        []byte
	Clock       Clock
	ID          string
}

func (a *RepeatAgent) Run(queue chan<- Task, stop <-chan struct{}) <-chan Result {
	results := make(chan Result)

	go func() {
		ticker := a.Clock.Tick()
		now := a.Clock.Now()
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

			select {
			case <-stop:
				close(results)
				a.Clock.Done()
				return
			default:
			}
		}
	}()

	return results
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

func (a *IntervalAgent) Run(queue chan<- Task, stop <-chan struct{}) <-chan Result {
	results := make(chan Result)
	var wg sync.WaitGroup

	go func() {
		for {
			start := a.Clock.Now()

			task := Task{copyRequest(a.BaseRequest, a.Body), make(chan Result, 1)}
			queue <- task

			go func(rc <-chan Result) {
				wg.Add(1)

				res := <-rc
				res.Start = start
				res.AgentID = a.ID
				results <- res

				wg.Done()
			}(task.Result)

			end := start.Add(a.Interval)
			now := start

			ticker := a.Clock.Tick()
			for now.Before(end) {
				select {
				case <-stop:
					a.Clock.Done()
					wg.Wait()
					close(results)
					return
				case now = <-ticker:
				}
			}
			a.Clock.Done()
		}
	}()

	return results
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
