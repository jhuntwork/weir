package main

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

func testJobs(t *testing.T, max int) *JobManager {
	t.Helper()
	m, err := NewJobManager(&Config{Commands: map[string]CommandConfig{"sh": {}}, Defaults: DefaultConfig{Timeout: 10, MaxOutputBytes: 1024}, Jobs: JobConfig{Dir: t.TempDir(), MaxConcurrent: max, LogBytes: 64, Retention: time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func waitJob(t *testing.T, m *JobManager, id string) *Job {
	t.Helper()
	j, err := m.Wait(context.Background(), id, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if j.State == JobQueued || j.State == JobRunning || j.State == JobCancellationRequested {
		t.Fatalf("job did not finish: %+v", j)
	}
	return j
}

func TestJobCompletesAndPersistsBoundedLogs(t *testing.T) {
	m := testJobs(t, 1)
	j, err := m.Start(`sh -c "printf hello"`, "", nil, 0, "test")
	if err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, m, j.ID)
	if j.State != JobSucceeded || j.Stdout != "hello" || j.EffectScope != "local_process_tree" {
		t.Fatalf("unexpected job: %+v", j)
	}
}

func TestJobWaitReturnsAlreadyTerminalJob(t *testing.T) {
	m := testJobs(t, 1)
	j, err := m.Start(`sh -c "exit 0"`, "", nil, 0, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, m, j.ID)
	start := time.Now()
	got, err := m.Wait(context.Background(), j.ID, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != JobSucceeded || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("terminal wait was not immediate: %+v", got)
	}
}

func TestJobWaitReturnsSuccessfulCompletion(t *testing.T) {
	m := testJobs(t, 1)
	j, err := m.Start(`sh -c "sleep 0.05; printf done"`, "", nil, 0, "success")
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Wait(context.Background(), j.ID, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != JobSucceeded || got.Stdout != "done" {
		t.Fatalf("unexpected completed job: %+v", got)
	}
}

func TestJobWaitReturnsFailedCompletion(t *testing.T) {
	m := testJobs(t, 1)
	j, err := m.Start(`sh -c "exit 7"`, "", nil, 0, "failure")
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Wait(context.Background(), j.ID, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != JobFailed || got.ExitCode == nil || *got.ExitCode != 7 {
		t.Fatalf("unexpected failed job: %+v", got)
	}
}

func TestJobWaitTimeoutDoesNotCancelJob(t *testing.T) {
	m := testJobs(t, 1)
	j, err := m.Start(`sh -c "sleep 0.2"`, "", nil, 0, "timeout")
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Wait(context.Background(), j.ID, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != JobQueued && got.State != JobRunning {
		t.Fatalf("wait timeout changed job state: %+v", got)
	}
	got = waitJob(t, m, j.ID)
	if got.State != JobSucceeded {
		t.Fatalf("job did not complete after wait timeout: %+v", got)
	}
}

func TestJobWaitCallerCancellationDoesNotCancelJob(t *testing.T) {
	m := testJobs(t, 1)
	j, err := m.Start(`sh -c "sleep 0.2"`, "", nil, 0, "caller-cancel")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Wait(ctx, j.ID, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v, want context canceled", err)
	}
	got, err := m.Get(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != JobQueued && got.State != JobRunning {
		t.Fatalf("caller cancellation changed job state: %+v", got)
	}
	if _, err := m.Cancel(j.ID); err != nil {
		t.Fatal(err)
	}
}

func TestJobWaitRejectsUnknownIDAndNonPositiveTimeout(t *testing.T) {
	m := testJobs(t, 1)
	if _, err := m.Wait(context.Background(), "missing", time.Second); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unknown ID error = %v", err)
	}
	if _, err := m.Wait(context.Background(), "missing", 0); err == nil {
		t.Fatal("expected non-positive timeout error")
	}
}

func TestJobWaitReturnsRestartLoadedTerminalJob(t *testing.T) {
	m := testJobs(t, 1)
	j, err := m.Start(`sh -c "printf persisted"`, "", nil, 0, "restart")
	if err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, m, j.ID)
	reloaded, err := NewJobManager(m.cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reloaded.Wait(context.Background(), j.ID, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != JobSucceeded || got.Stdout != "persisted" {
		t.Fatalf("unexpected reloaded job: %+v", got)
	}
}

func TestJobWaitSupportsConcurrentWaiters(t *testing.T) {
	m := testJobs(t, 1)
	j, err := m.Start(`sh -c "sleep 0.05"`, "", nil, 0, "concurrent")
	if err != nil {
		t.Fatal(err)
	}
	const waiters = 8
	var wg sync.WaitGroup
	errs := make(chan error, waiters)
	for range waiters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := m.Wait(context.Background(), j.ID, time.Second)
			if err != nil {
				errs <- err
				return
			}
			if got.State != JobSucceeded {
				errs <- errors.New("waiter returned before success")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestJobCancellationReapsProcessTree(t *testing.T) {
	m := testJobs(t, 1)
	j, err := m.Start(`sh -c "sleep 10"`, "", nil, 0, "test")
	if err != nil {
		t.Fatal(err)
	}
	j, err = m.Cancel(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != JobCancelled {
		t.Fatalf("state = %s, want cancelled", j.State)
	}
}

func TestJobsQueueAtConcurrencyLimit(t *testing.T) {
	m := testJobs(t, 1)
	first, err := m.Start(`sh -c "sleep 1"`, "", nil, 0, "test")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Start(`sh -c "printf second"`, "", nil, 0, "test")
	if err != nil {
		t.Fatal(err)
	}
	j, err := m.Get(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != JobQueued {
		t.Fatalf("second state = %s, want queued", j.State)
	}
	if _, err := m.Cancel(first.ID); err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, m, second.ID)
	if j.State != JobSucceeded {
		t.Fatalf("second state = %s", j.State)
	}
}
