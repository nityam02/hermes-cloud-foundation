package uiserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

// JobStep is one named stage of a job, shown to the user as it progresses.
type JobStep struct {
	Name   string `json:"name"`
	Status string `json:"status"` // running, done, failed, warn
	Detail string `json:"detail,omitempty"`
}

// Job is a long operation: a sign-in, a setup change, or discovery.
type Job struct {
	ID    string    `json:"id"`
	Kind  string    `json:"kind"`
	State string    `json:"state"` // running, done, failed, cancelled
	Steps []JobStep `json:"steps"`
	Error string    `json:"error,omitempty"`
	// Link is a sign-in page the tool asked the person to open, for when the browser did not open it.
	Link string `json:"link,omitempty"`
	// Code is a device code the person confirms on the sign-in page.
	Code string `json:"code,omitempty"`
	// Log is the last lines the tool printed.
	Log     []string        `json:"log,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Started time.Time       `json:"started"`
	Ended   time.Time       `json:"ended,omitzero"`
}

const logLines = 12

// progress is what a running job reports through.
type progress struct {
	mu  *sync.Mutex
	job *Job
}

func (p progress) Step(name, status, detail string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.job.Steps {
		if p.job.Steps[i].Name == name {
			p.job.Steps[i].Status, p.job.Steps[i].Detail = status, detail
			return
		}
	}
	p.job.Steps = append(p.job.Steps, JobStep{Name: name, Status: status, Detail: detail})
}

func (p progress) Prompt(link, code string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if link != "" && p.job.Link == "" {
		p.job.Link = link
	}
	if code != "" {
		p.job.Code = code
	}
}

func (p progress) Line(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	p.job.Log = append(p.job.Log, s)
	if len(p.job.Log) > logLines {
		p.job.Log = p.job.Log[len(p.job.Log)-logLines:]
	}
}

// Report adapts progress to connect.Reporter.
func (p progress) Report(step, status, detail string) { p.Step(step, status, detail) }

// jobFunc runs a job. A non-nil result is returned to the page as JSON.
type jobFunc func(ctx context.Context, p progress) (any, error)

// jobs runs at most one job per lane at a time: AWS work, Google work and discovery can overlap, but
// two AWS sign-ins cannot.
type jobs struct {
	mu      sync.Mutex
	jobs    map[string]*Job
	cancels map[string]context.CancelFunc
	busy    map[string]string // lane -> running job id
}

func newJobs() *jobs {
	return &jobs{jobs: map[string]*Job{}, cancels: map[string]context.CancelFunc{}, busy: map[string]string{}}
}

func (j *jobs) get(id string) (Job, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	v, ok := j.jobs[id]
	if !ok {
		return Job{}, false
	}
	cp := *v
	// Never nil: the page reads steps as a list, even before the first step is reported.
	cp.Steps = append([]JobStep{}, v.Steps...)
	cp.Log = append([]string(nil), v.Log...)
	return cp, true
}

// running returns the job running in a lane, if any.
func (j *jobs) running(lane string) (string, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	id, ok := j.busy[lane]
	return id, ok
}

func (j *jobs) cancel(id string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	c, ok := j.cancels[id]
	if ok {
		c()
	}
	return ok
}

// start runs fn in the background. It returns false when another job is still running in the lane.
func (j *jobs) start(lane, kind string, timeout time.Duration, fn jobFunc) (string, bool) {
	j.mu.Lock()
	if _, busy := j.busy[lane]; busy {
		j.mu.Unlock()
		return "", false
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	job := &Job{ID: hex.EncodeToString(b), Kind: kind, State: "running", Steps: []JobStep{}, Started: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	j.jobs[job.ID] = job
	j.cancels[job.ID] = cancel
	j.busy[lane] = job.ID
	j.trim()
	j.mu.Unlock()

	go func() {
		defer cancel()
		result, err := fn(ctx, progress{mu: &j.mu, job: job})
		cancelled := errors.Is(ctx.Err(), context.Canceled)
		var raw json.RawMessage
		if err == nil && result != nil {
			raw, _ = json.Marshal(result)
		}
		j.mu.Lock()
		defer j.mu.Unlock()
		job.Ended = time.Now().UTC()
		switch {
		case cancelled:
			job.State, job.Error = "cancelled", "Cancelled."
		case err != nil:
			job.State, job.Error = "failed", err.Error()
		default:
			job.State, job.Result = "done", raw
		}
		for i := range job.Steps {
			if job.State != "done" && job.Steps[i].Status == "running" {
				job.Steps[i].Status = "failed"
				if cancelled {
					job.Steps[i].Detail = "Cancelled."
				}
			}
		}
		delete(j.cancels, job.ID)
		delete(j.busy, lane)
	}()
	return job.ID, true
}

// trim keeps the 50 most recent finished jobs. Callers hold the lock.
func (j *jobs) trim() {
	if len(j.jobs) <= 50 {
		return
	}
	var done []*Job
	for _, v := range j.jobs {
		if v.State != "running" {
			done = append(done, v)
		}
	}
	sort.Slice(done, func(a, b int) bool { return done[a].Started.Before(done[b].Started) })
	for i := 0; i < len(done) && len(j.jobs) > 50; i++ {
		delete(j.jobs, done[i].ID)
	}
}
