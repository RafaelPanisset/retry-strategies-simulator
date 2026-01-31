package main

import (
	"flag"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"
)

// Server simulates a service with limited capacity.
type Server struct {
	mu       sync.Mutex
	capacity int
	downFor  time.Duration
	start    time.Time
	requests  map[int]int // second -> request count
	accepted map[int]int // second -> accepted count
}

func NewServer(capacity int, downFor time.Duration) *Server {
	return &Server{
		capacity: capacity,
		downFor:  downFor,
		start:    time.Now(),
		requests:  make(map[int]int),
		accepted: make(map[int]int),
	}
}

// Do attempts a request. Returns true if the server accepted it.
func (s *Server) Do() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	elapsed := time.Since(s.start)
	sec := int(elapsed.Seconds())
	s.requests[sec]++

	if elapsed < s.downFor {
		return false
	}
	if s.accepted[sec] >= s.capacity {
		return false
	}
	s.accepted[sec]++
	return true
}

// Strategy computes the next sleep duration given the attempt number and previous delay.
type Strategy func(attempt int, prevDelay time.Duration) time.Duration

const (
	baseSleep = 100 * time.Millisecond
	capSleep  = 10 * time.Second
)

func constantRetry(_ int, _ time.Duration) time.Duration {
	return 1 * time.Millisecond
}

func exponentialBackoff(attempt int, _ time.Duration) time.Duration {
	d := time.Duration(float64(baseSleep) * math.Pow(2, float64(attempt)))
	if d > capSleep {
		d = capSleep
	}
	return d
}

func fullJitter(attempt int, _ time.Duration) time.Duration {
	d := time.Duration(float64(baseSleep) * math.Pow(2, float64(attempt)))
	if d > capSleep {
		d = capSleep
	}
	return time.Duration(rand.Int63n(int64(d)))
}

func decorrelatedJitter(_ int, prev time.Duration) time.Duration {
	if prev < baseSleep {
		prev = baseSleep
	}
	d := time.Duration(rand.Int63n(int64(prev)*3-int64(baseSleep))) + baseSleep
	if d > capSleep {
		d = capSleep
	}
	return d
}

// Metrics collected across all clients.
type Metrics struct {
	mu            sync.Mutex
	totalRequests int
	wastedReqs    int
	latencies     []time.Duration
}

func (m *Metrics) Record(latency time.Duration, wasted int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.totalRequests += wasted + 1
	m.wastedReqs += wasted
	m.latencies = append(m.latencies, latency)
}

func clientLoop(srv *Server, strategy Strategy, metrics *Metrics) {
	start := time.Now()
	attempt := 0
	prevDelay := baseSleep

	for {
		if srv.Do() {
			metrics.Record(time.Since(start), attempt)
			return
		}
		delay := strategy(attempt, prevDelay)
		prevDelay = delay
		attempt++
		time.Sleep(delay)
	}
}

func runSimulation(numClients int, srv *Server, strategy Strategy) *Metrics {
	var wg sync.WaitGroup
	metrics := &Metrics{}

	wg.Add(numClients)
	for i := 0; i < numClients; i++ {
		go func() {
			defer wg.Done()
			clientLoop(srv, strategy, metrics)
		}()
	}
	wg.Wait()
	return metrics
}

func printHistogram(srv *Server) {
	srv.mu.Lock()
	defer srv.mu.Unlock()

	if len(srv.requests) == 0 {
		fmt.Println("  (no data)")
		return
	}

	maxSec := 0
	maxCount := 0
	for s, c := range srv.requests {
		if s > maxSec {
			maxSec = s
		}
		if c > maxCount {
			maxCount = c
		}
	}

	barWidth := 60
	capMark := 0
	if maxCount > 0 {
		capMark = srv.capacity * barWidth / maxCount
		if capMark > barWidth {
			capMark = barWidth
		}
	}

	fmt.Printf("\n  Requests per second (capacity=%d req/s marked with |)\n\n", srv.capacity)

	for s := 0; s <= maxSec; s++ {
		count := srv.requests[s]
		w := 0
		if maxCount > 0 {
			w = count * barWidth / maxCount
		}

		bar := strings.Repeat("█", w)
		if w < barWidth {
			bar += strings.Repeat(" ", barWidth-w)
		}

		// Insert capacity marker
		if capMark < barWidth {
			runes := []rune(bar)
			runes[capMark] = '|'
			bar = string(runes)
		}

		label := "     "
		if s < int(srv.downFor.Seconds()) {
			label = " DOWN"
		}

		fmt.Printf("  %3ds%s %s %d\n", s, label, bar, count)
	}
	fmt.Println()
}

func printSummary(srv *Server, metrics *Metrics) {
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	srv.mu.Lock()
	defer srv.mu.Unlock()

	// Time to stable: first second after downtime where no requests are rejected
	recoveryTime := -1
	downSec := int(srv.downFor.Seconds())
	for s := downSec; s < downSec+60; s++ {
		rejected := srv.requests[s] - srv.accepted[s]
		if srv.requests[s] > 0 && rejected == 0 {
			recoveryTime = s - downSec
			break
		}
	}

	// Peak overshoot
	peakOvershoot := 0
	for s := downSec; s < downSec+60; s++ {
		over := srv.requests[s] - srv.capacity
		if over > peakOvershoot {
			peakOvershoot = over
		}
	}

	// p99 latency
	sort.Slice(metrics.latencies, func(i, j int) bool {
		return metrics.latencies[i] < metrics.latencies[j]
	})
	var p99 time.Duration
	if len(metrics.latencies) > 0 {
		idx := int(float64(len(metrics.latencies)) * 0.99)
		if idx >= len(metrics.latencies) {
			idx = len(metrics.latencies) - 1
		}
		p99 = metrics.latencies[idx]
	}

	fmt.Println("  Summary")
	fmt.Println("  -------")
	if recoveryTime >= 0 {
		fmt.Printf("  Time to stable : %ds\n", recoveryTime)
	} else {
		fmt.Printf("  Time to stable : >60s\n")
	}
	fmt.Printf("  Peak overshoot (reqs/s)    : %d over capacity\n", peakOvershoot)
	fmt.Printf("  Total requests             : %d\n", metrics.totalRequests)
	fmt.Printf("  Wasted (rejected) requests : %d\n", metrics.wastedReqs)
	fmt.Printf("  Clients served             : %d\n", len(metrics.latencies))
	fmt.Printf("  p99 client latency         : %s\n", p99.Round(time.Millisecond))
	fmt.Println()
}

func main() {
	strategyName := flag.String("strategy", "constant", "retry strategy: constant|backoff|jitter|decorrelated")
	flag.Parse()

	strategies := map[string]Strategy{
		"constant":     constantRetry,
		"backoff":      exponentialBackoff,
		"jitter":       fullJitter,
		"decorrelated": decorrelatedJitter,
	}

	strat, ok := strategies[*strategyName]
	if !ok {
		fmt.Printf("Unknown strategy %q. Use: constant, backoff, jitter, decorrelated\n", *strategyName)
		return
	}

	const (
		numClients     = 1000
		serverCapacity = 200
		downDuration   = 10 * time.Second
	)

	fmt.Printf("  Strategy: %s | Clients: %d | Server capacity: %d req/s | Outage: %s\n",
		*strategyName, numClients, serverCapacity, downDuration)

	srv := NewServer(serverCapacity, downDuration)
	metrics := runSimulation(numClients, srv, strat)

	printHistogram(srv)
	printSummary(srv, metrics)
}
