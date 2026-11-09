// Package groupops runs the same command for the machines of a group at the same time
// (docs/plan/gruppen.md). It implements driving.GroupRunner.
package groupops

import (
	"context"
	"sync"
	"time"

	"github.com/pt9912/hetzner-viper/internal/port/driving"
)

// Runner is the driving.GroupRunner.
type Runner struct{}

// Run runs the tasks, at most limit at the same time (limit < 1: driving.DefaultGroupParallel), and returns the
// results in the order of the tasks. A task that has not started when ctx ends does not start; its
// result says so.
func (Runner) Run(ctx context.Context, tasks []driving.GroupTask, limit int) []driving.GroupResult {
	if limit < 1 {
		limit = driving.DefaultGroupParallel
	}
	results := make([]driving.GroupResult, len(tasks))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i, t := range tasks {
		results[i].Machine = t.Machine
		// the places are taken in the order of the tasks, so the first machines start first
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			results[i].Canceled = true
			continue
		}
		if ctx.Err() != nil {
			<-sem
			results[i].Canceled = true
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			start := time.Now()
			code, err := t.Run(ctx)
			results[i].Code, results[i].Err, results[i].Elapsed = code, err, time.Since(start)
			if ctx.Err() != nil && (err != nil || code != 0) {
				results[i].Canceled = true
			}
		}()
	}
	wg.Wait()
	return results
}
