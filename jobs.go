package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type JobState string

const (
	JobQueued                JobState = "queued"
	JobRunning               JobState = "running"
	JobCancellationRequested JobState = "cancellation_requested"
	JobSucceeded             JobState = "succeeded"
	JobFailed                JobState = "failed"
	JobCancelled             JobState = "cancelled"
	JobInterrupted           JobState = "interrupted"
	JobExpired               JobState = "expired"
)

type Job struct {
	ID             string     `json:"id"`
	State          JobState   `json:"state"`
	Command        string     `json:"command"`
	Argv           []string   `json:"argv"`
	Cwd            string     `json:"cwd,omitempty"`
	Env            []string   `json:"env,omitempty"`
	TimeoutSeconds int        `json:"timeout_seconds,omitempty"`
	Attribution    string     `json:"attribution,omitempty"`
	EffectScope    string     `json:"effect_scope"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	DurationMs     int64      `json:"duration_ms,omitempty"`
	PID            int        `json:"pid,omitempty"`
	PGID           int        `json:"pgid,omitempty"`
	ExitCode       *int       `json:"exit_code,omitempty"`
	TerminalError  string     `json:"terminal_error,omitempty"`
	Stdout         string     `json:"stdout,omitempty"`
	Stderr         string     `json:"stderr,omitempty"`
	StdoutCut      bool       `json:"stdout_truncated,omitempty"`
	StderrCut      bool       `json:"stderr_truncated,omitempty"`
}

type jobRuntime struct {
	job    *Job
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan struct{}
	stdout *jobLog
	stderr *jobLog
	env    map[string]string
}

type jobLog struct {
	mu        sync.Mutex
	buf       []byte
	limit     int
	truncated bool
}

func (l *jobLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if remaining := l.limit - len(l.buf); remaining > 0 {
		if len(p) > remaining {
			l.buf = append(l.buf, p[:remaining]...)
			l.truncated = true
		} else {
			l.buf = append(l.buf, p...)
		}
	} else {
		l.truncated = true
	}
	return len(p), nil
}

func (l *jobLog) snapshot() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.buf), l.truncated
}

type JobManager struct {
	cfg     *Config
	root    string
	mu      sync.Mutex
	jobs    map[string]*jobRuntime
	pending []string
	running int
	closed  bool
}

func NewJobManager(cfg *Config) (*JobManager, error) {
	if cfg.Jobs.MaxConcurrent <= 0 {
		cfg.Jobs.MaxConcurrent = 2
	}
	if cfg.Jobs.LogBytes <= 0 {
		cfg.Jobs.LogBytes = cfg.Defaults.MaxOutputBytes
	}
	if cfg.Jobs.LogBytes <= 0 {
		cfg.Jobs.LogBytes = defaultMaxOutputBytes
	}
	if cfg.Jobs.Retention <= 0 {
		cfg.Jobs.Retention = 7 * 24 * time.Hour
	}
	m := &JobManager{cfg: cfg, root: cfg.Jobs.Dir, jobs: make(map[string]*jobRuntime)}
	if m.root == "" {
		return m, nil
	}
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return nil, fmt.Errorf("create job directory: %w", err)
	}
	entries, err := os.ReadDir(m.root)
	if err != nil {
		return nil, fmt.Errorf("read job directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.root, entry.Name()))
		if err != nil {
			return nil, err
		}
		var job Job
		if err := json.Unmarshal(data, &job); err != nil {
			return nil, fmt.Errorf("parse job %s: %w", entry.Name(), err)
		}
		if job.State == JobQueued || job.State == JobRunning || job.State == JobCancellationRequested {
			now := time.Now().UTC()
			job.State = JobInterrupted
			job.FinishedAt = &now
			job.TerminalError = "weir restarted before the job reached a terminal state"
			m.persistLocked(&job)
		}
		m.jobs[job.ID] = &jobRuntime{job: &job, done: closedChannel()}
	}
	return m, nil
}

func closedChannel() chan struct{} { ch := make(chan struct{}); close(ch); return ch }

func (m *JobManager) Start(command string, cwd string, env map[string]string, timeout int, attribution string) (*Job, error) {
	parts, err := tokenize(command)
	if err != nil || len(parts) == 0 {
		return nil, fmt.Errorf("parse command: %w", err)
	}
	basename := filepath.Base(parts[0])
	if !m.cfg.IsAllowed(basename) || !m.cfg.IsSubcommandAllowed(basename, parts[1:]) {
		return nil, errors.New("job command is not permitted by Weir's restricted command surface")
	}
	path, err := m.cfg.Resolve(basename)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = m.cfg.TimeoutFor(basename)
	}
	id, err := newJobID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	job := &Job{ID: id, State: JobQueued, Command: basename, Argv: append([]string{path}, parts[1:]...), Cwd: cwd, Env: envNames(env), TimeoutSeconds: timeout, Attribution: attribution, EffectScope: "local_process_tree", CreatedAt: now}
	r := &jobRuntime{job: job, done: make(chan struct{}), stdout: &jobLog{limit: m.cfg.Jobs.LogBytes}, stderr: &jobLog{limit: m.cfg.Jobs.LogBytes}, env: copyEnv(env)}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("job manager is shutting down")
	}
	m.jobs[id] = r
	m.pending = append(m.pending, id)
	if err := m.persistLocked(job); err != nil {
		return nil, err
	}
	m.pumpLocked()
	return copyJob(job), nil
}

func (m *JobManager) pumpLocked() {
	for m.running < m.cfg.Jobs.MaxConcurrent && len(m.pending) > 0 {
		id := m.pending[0]
		m.pending = m.pending[1:]
		r := m.jobs[id]
		if r == nil || r.job.State != JobQueued {
			continue
		}
		m.running++
		go m.run(r, r.job.TimeoutSeconds)
	}
}

func (m *JobManager) run(r *jobRuntime, timeout int) {
	m.mu.Lock()
	if r.job.State != JobQueued {
		m.running--
		m.pumpLocked()
		m.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	r.job.State = JobRunning
	r.job.StartedAt = &now
	ctx := context.Background()
	if timeout > 0 {
		ctx, r.cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	} else {
		ctx, r.cancel = context.WithCancel(ctx)
	}
	cmd := exec.CommandContext(ctx, r.job.Argv[0], r.job.Argv[1:]...)
	cmd.Dir = r.job.Cwd
	configureProcessTree(cmd)
	cmd.Env = os.Environ()
	for k, v := range r.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout, cmd.Stderr = r.stdout, r.stderr
	r.cmd = cmd
	if err := cmd.Start(); err != nil {
		m.finishLocked(r, JobFailed, nil, "start: "+err.Error())
		m.mu.Unlock()
		return
	}
	r.job.PID, r.job.PGID = cmd.Process.Pid, cmd.Process.Pid
	_ = m.persistLocked(r.job)
	m.mu.Unlock()

	err := cmd.Wait()
	m.mu.Lock()
	state := JobSucceeded
	var exit *int
	message := ""
	if r.job.State == JobCancellationRequested {
		state = JobCancelled
	} else if ctx.Err() == context.DeadlineExceeded {
		state = JobFailed
		message = "job timed out"
	} else if err != nil {
		state = JobFailed
		message = err.Error()
		if ee, ok := err.(*exec.ExitError); ok {
			code := ee.ExitCode()
			exit = &code
		}
	}
	m.finishLocked(r, state, exit, message)
	m.mu.Unlock()
}

func (m *JobManager) finishLocked(r *jobRuntime, state JobState, exit *int, message string) {
	now := time.Now().UTC()
	r.job.State, r.job.FinishedAt, r.job.ExitCode, r.job.TerminalError = state, &now, exit, message
	if r.job.StartedAt != nil {
		r.job.DurationMs = now.Sub(*r.job.StartedAt).Milliseconds()
	}
	r.job.Stdout, r.job.StdoutCut = r.stdout.snapshot()
	r.job.Stderr, r.job.StderrCut = r.stderr.snapshot()
	_ = m.persistLocked(r.job)
	m.running--
	close(r.done)
	m.pumpLocked()
}

func (m *JobManager) Get(id string) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.jobs[id]
	if r == nil {
		return nil, os.ErrNotExist
	}
	j := copyJob(r.job)
	if r.stdout != nil {
		j.Stdout, j.StdoutCut = r.stdout.snapshot()
		j.Stderr, j.StderrCut = r.stderr.snapshot()
	}
	return j, nil
}

// Wait returns when a job becomes terminal, the caller cancels, or the wait
// deadline elapses. A wait deadline never changes or cancels the job itself.
func (m *JobManager) Wait(ctx context.Context, id string, timeout time.Duration) (*Job, error) {
	if timeout <= 0 {
		return nil, errors.New("wait timeout must be positive")
	}
	m.mu.Lock()
	r := m.jobs[id]
	if r == nil {
		m.mu.Unlock()
		return nil, os.ErrNotExist
	}
	done := r.done
	m.mu.Unlock()

	select {
	case <-done:
		return m.Get(id)
	default:
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return m.Get(id)
	case <-timer.C:
		return m.Get(id)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *JobManager) List() []*Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Job, 0, len(m.jobs))
	for _, r := range m.jobs {
		out = append(out, copyJob(r.job))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (m *JobManager) Cancel(id string) (*Job, error) {
	m.mu.Lock()
	r := m.jobs[id]
	if r == nil {
		m.mu.Unlock()
		return nil, os.ErrNotExist
	}
	if r.job.State == JobQueued {
		r.job.State = JobCancelled
		now := time.Now().UTC()
		r.job.FinishedAt = &now
		_ = m.persistLocked(r.job)
		close(r.done)
		m.mu.Unlock()
		return copyJob(r.job), nil
	}
	if r.job.State != JobRunning && r.job.State != JobCancellationRequested {
		j := copyJob(r.job)
		m.mu.Unlock()
		return j, nil
	}
	r.job.State = JobCancellationRequested
	_ = m.persistLocked(r.job)
	if r.cmd != nil {
		_ = terminateProcessTree(r.cmd)
	}
	done := r.done
	m.mu.Unlock()
	<-done
	return m.Get(id)
}

func (m *JobManager) Cleanup() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-m.cfg.Jobs.Retention)
	n := 0
	for _, r := range m.jobs {
		if r.job.FinishedAt != nil && r.job.FinishedAt.Before(cutoff) && r.job.State != JobExpired {
			r.job.State = JobExpired
			r.job.Stdout, r.job.Stderr = "", ""
			r.job.StdoutCut, r.job.StderrCut = false, false
			if err := m.persistLocked(r.job); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}
func (m *JobManager) Shutdown() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	ids := make([]string, 0)
	for id, r := range m.jobs {
		if r.job.State == JobRunning || r.job.State == JobCancellationRequested {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		_, _ = m.Cancel(id)
	}
}

func (m *JobManager) persistLocked(job *Job) error {
	if m.root == "" {
		return nil
	}
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(m.root, job.ID+".tmp")
	target := filepath.Join(m.root, job.ID+".json")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}
func newJobID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func copyEnv(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func envNames(in map[string]string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func copyJob(in *Job) *Job {
	out := *in
	out.Argv = append([]string(nil), in.Argv...)
	out.Env = append([]string(nil), in.Env...)
	return &out
}
