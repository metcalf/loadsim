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

type RepeatAgent struct {
	BaseRequest *http.Request
	Body        []byte
	Clock       Clock
}

func (a *RepeatAgent) Run(queue chan<- Task, stop <-chan struct{}) <-chan Result {
	results := make(chan Result)

	go func() {
		for {
			task := Task{copyRequest(a.BaseRequest, a.Body), make(chan Result, 1)}
			queue <- task
			start := a.Clock.Now()

			res := <-task.Result
			res.Start = start
			results <- res

			select {
			case <-stop:
				close(results)
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
				results <- res

				wg.Done()
			}(task.Result)

			end := start.Add(a.Interval)
			now := start
			ticker := a.Clock.Tick()
			for now.Before(end) {
				select {
				case <-stop:
					wg.Wait()
					close(results)
					a.Clock.Done()
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