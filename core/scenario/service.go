/*
*
*	Ddosify - Load testing tool for any web system.
*   Copyright (C) 2021  Ddosify (https://ddosify.com)
*
*   This program is free software: you can redistribute it and/or modify
*   it under the terms of the GNU Affero General Public License as published
*   by the Free Software Foundation, either version 3 of the License, or
*   (at your option) any later version.
*
*   This program is distributed in the hope that it will be useful,
*   but WITHOUT ANY WARRANTY; without even the implied warranty of
*   MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
*   GNU Affero General Public License for more details.
*
*   You should have received a copy of the GNU Affero General Public License
*   along with this program.  If not, see <https://www.gnu.org/licenses/>.
*
 */

package scenario

import (
	"context"
	"math/rand"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.ddosify.com/ddosify/core/scenario/requester"
	"go.ddosify.com/ddosify/core/types"
)

// ScenarioService encapsulates proxy/scenario/requester information and runs the scenario.
type ScenarioService struct {
	// Client map structure [proxy_addr][]scenarioItemRequester
	// Each proxy represents a client.
	// Each scenarioItem has a requester
	clients map[*url.URL][]scenarioItemRequester

	scenario types.Scenario
	ctx      context.Context

	clientMutex sync.Mutex
	debug       bool
}

// NewScenarioService is the constructor of the ScenarioService.
func NewScenarioService() *ScenarioService {
	return &ScenarioService{}
}

// Init initializes the ScenarioService.clients with the given types.Scenario and proxies.
// Passes the given ctx to the underlying requestor so we are able to control the life of each request.
func (s *ScenarioService) Init(ctx context.Context, scenario types.Scenario, proxies []*url.URL, debug bool) (err error) {
	s.scenario = scenario
	s.ctx = ctx
	s.debug = debug
	s.clients = make(map[*url.URL][]scenarioItemRequester, len(proxies))
	for _, p := range proxies {
		err = s.createRequesters(p)
		if err != nil {
			return
		}
	}
	return
}

// Do executes the scenario for the given proxy.
// Returns "types.Response" filled by the requester of the given Proxy, injects the given startTime to the response
// Returns error only if types.Response.Err.Type is types.ErrorProxy or types.ErrorIntented
func (s *ScenarioService) Do(proxy *url.URL, startTime time.Time) (
	response *types.ScenarioResult, err *types.RequestError) {
	response = &types.ScenarioResult{StepResults: []*types.ScenarioStepResult{}}
	response.StartTime = startTime
	response.ProxyAddr = proxy

	requesters, e := s.getOrCreateRequesters(proxy)
	if e != nil {
		return nil, &types.RequestError{Type: types.ErrorUnkown, Reason: e.Error()}
	}

	for _, sr := range requesters {
		res := sr.requester.Send()
		if res.Err.Type == types.ErrorProxy || res.Err.Type == types.ErrorIntented {
			err = &res.Err
			if res.Err.Type == types.ErrorIntented {
				// Stop the loop. ErrorProxy can be fixed in time. But ErrorIntented is a signal to stop all.
				return
			}
		}
		response.StepResults = append(response.StepResults, res)

		// Sleep before running the next step
		if sr.sleeper != nil && len(s.scenario.Steps) > 1 {
			sr.sleeper.sleep()
		}
	}
	return
}

func (s *ScenarioService) Done() {
	for _, v := range s.clients {
		for _, r := range v {
			r.requester.Done()
		}
	}
}

func (s *ScenarioService) getOrCreateRequesters(proxy *url.URL) (requesters []scenarioItemRequester, err error) {
	s.clientMutex.Lock()
	defer s.clientMutex.Unlock()

	requesters, ok := s.clients[proxy]
	if !ok {
		err = s.createRequesters(proxy)
		if err != nil {
			return
		}
	}
	return s.clients[proxy], err
}

func (s *ScenarioService) createRequesters(proxy *url.URL) (err error) {
	s.clients[proxy] = []scenarioItemRequester{}
	for _, si := range s.scenario.Steps {
		var r requester.Requester
		r, err = requester.NewRequester(si)
		if err != nil {
			return
		}
		s.clients[proxy] = append(
			s.clients[proxy],
			scenarioItemRequester{
				scenarioItemID: si.ID,
				sleeper:        newSleeper(si.Sleep),
				requester:      r,
			},
		)

		err = r.Init(s.ctx, si, proxy, s.debug)
		if err != nil {
			return
		}
	}
	return err
}

type scenarioItemRequester struct {
	scenarioItemID uint16
	sleeper        Sleeper
	requester      requester.Requester
}

// Sleeper is the interface for implementing different sleep strategies.
type Sleeper interface {
	sleep()
}

// RangeSleep is the implementation of the range sleep feature
type RangeSleep struct {
	min int
	max int
}

func (rs *RangeSleep) sleep() {
	rand.Seed(time.Now().UnixNano())
	dur := rand.Intn(rs.max-rs.min+1) + rs.min
	time.Sleep(time.Duration(dur) * time.Millisecond)
}

// DurationSleep is the implementation of the exact duration sleep feature
type DurationSleep struct {
	duration int
}

func (ds *DurationSleep) sleep() {
	time.Sleep(time.Duration(ds.duration) * time.Millisecond)
}

// newSleeper is the factor method for the Sleeper implementations.
func newSleeper(sleepStr string) Sleeper {
	if sleepStr == "" {
		return nil
	}

	var sl Sleeper

	// Sleep field already validated in types.scenario.validate(). No need to check parsing errors here.
	s := strings.Split(sleepStr, "-")
	if len(s) == 2 {
		min, _ := strconv.Atoi(s[0])
		max, _ := strconv.Atoi(s[1])
		if min > max {
			min, max = max, min
		}

		sl = &RangeSleep{
			min: min,
			max: max,
		}
	} else {
		dur, _ := strconv.Atoi(s[0])

		sl = &DurationSleep{
			duration: dur,
		}
	}

	return sl
}
