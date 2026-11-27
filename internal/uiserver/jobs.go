package uiserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// JobStep is one named stage of a job, shown to the user as it progresses.
type JobStep struct {
	Name   string `json:"name"`
	Status string `json:"status"` // running, done, failed, warn
	Detail string `json:"detail,omitempty"`
}

// Job is a long operation. Only one runs at a time, so the page has one thing to show.
type Job struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	State   string    `json:"state"` // running, done, failed
	Steps   []JobStep `json:"steps"`
	Error   string    `json:"error,omitempty"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitempty"`
}

type jobs struct {
	mu   sync.Mutex
	jobs map[string]*Job
	busy bool
}

func newJobs() *jobs { return &jobs{jobs: map[string]*Job{}} }

func (j *jobs) get(id string) (Job, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	v, ok := j.jobs[id]
	if !ok {
		return Job{}, false
	}
	cp := *v
	cp.Steps = append([]JobStep(nil), v.Steps...)
	return cp, true
}

// start runs fn in the background. It returns false when another job is still running.
func (j *jobs) start(kind string, timeout time.Duration, fn func(ctx context.Context, report func(step, status, detail string)) error) (string, bool) {
	j.mu.Lock()
	if j.busy {
		j.mu.Unlock()
		return "", false
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	job := &Job{ID: hex.EncodeToString(b), Kind: kind, State: "running", Started: time.Now().UTC()}
	j.jobs[job.ID] = job
	j.busy = true
	j.mu.Unlock()

	report := func(step, status, detail string) {
		j.mu.Lock()
		defer j.mu.Unlock()
		for i := range job.Steps {
			if job.Steps[i].Name == step {
				job.Steps[i].Status, job.Steps[i].Detail = status, detail
				return
			}
		}
		job.Steps = append(job.Steps, JobStep{Name: step, Status: status, Detail: detail})
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		err := fn(ctx, report)
		j.mu.Lock()
		defer j.mu.Unlock()
		job.Ended = time.Now().UTC()
		if err != nil {
			job.State, job.Error = "failed", err.Error()
		} else {
			job.State = "done"
		}
		j.busy = false
	}()
	return job.ID, true
}
