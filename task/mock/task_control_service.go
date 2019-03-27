package mock

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/influxdata/influxdb"
	"github.com/influxdata/influxdb/snowflake"
	"github.com/influxdata/influxdb/task/backend"
	cron "gopkg.in/robfig/cron.v2"
)

var idgen = snowflake.NewDefaultIDGenerator()

// TaskControlService is a mock implementation of TaskControlService (used by NewScheduler).
type TaskControlService struct {
	mu sync.Mutex
	// Map of stringified task ID to last ID used for run.
	runs map[string]map[string]*influxdb.Run

	// Map of stringified, concatenated task and platform ID, to runs that have been created.
	created map[string]backend.QueuedRun

	// Map of stringified task ID to task meta.
	tasks      map[string]*influxdb.Task
	manualRuns []*influxdb.Run
	// Map of task ID to total number of runs created for that task.
	totalRunsCreated map[influxdb.ID]int
	finishedRuns     map[string]*influxdb.Run
}

var _ backend.TaskControlService = (*TaskControlService)(nil)

func NewTaskControlService() *TaskControlService {
	return &TaskControlService{
		runs:             make(map[string]map[string]*influxdb.Run),
		finishedRuns:     make(map[string]*influxdb.Run),
		tasks:            make(map[string]*influxdb.Task),
		created:          make(map[string]backend.QueuedRun),
		totalRunsCreated: make(map[influxdb.ID]int),
	}
}

// SetTask sets the task.
// SetTask must be called before CreateNextRun, for a given task ID.
func (d *TaskControlService) SetTask(task *influxdb.Task) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.tasks[task.ID.String()] = task
}

func (d *TaskControlService) SetManualRuns(runs []*influxdb.Run) {
	d.manualRuns = runs
}

// CreateNextRun creates the next run for the given task.
// Refer to the documentation for SetTaskPeriod to understand how the times are determined.
func (d *TaskControlService) CreateNextRun(ctx context.Context, taskID influxdb.ID, now int64) (backend.RunCreation, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !taskID.Valid() {
		return backend.RunCreation{}, errors.New("invalid task id")
	}
	tid := taskID.String()

	task, ok := d.tasks[tid]
	if !ok {
		panic(fmt.Sprintf("meta not set for task with ID %s", tid))
	}

	if len(d.manualRuns) != 0 {
		run := d.manualRuns[0]
		d.manualRuns = d.manualRuns[1:]
		runs, ok := d.runs[tid]
		if !ok {
			runs = make(map[string]*influxdb.Run)
		}
		runs[run.ID.String()] = run
		d.runs[task.ID.String()] = runs
		now, err := time.Parse(time.RFC3339, run.ScheduledFor)
		next, _ := d.NextDueRun(ctx, taskID)
		if err == nil {
			rc := backend.RunCreation{
				Created: backend.QueuedRun{
					TaskID: task.ID,
					RunID:  run.ID,
					Now:    now.Unix(),
				},
				NextDue:  next,
				HasQueue: len(d.manualRuns) != 0,
			}
			d.created[tid+rc.Created.RunID.String()] = rc.Created
			d.totalRunsCreated[taskID]++
			return rc, nil
		}
	}

	rc, err := d.createNextRun(task, now)
	if err != nil {
		return backend.RunCreation{}, err
	}
	rc.Created.TaskID = taskID
	d.created[tid+rc.Created.RunID.String()] = rc.Created
	d.totalRunsCreated[taskID]++
	return rc, nil
}

func (t *TaskControlService) createNextRun(task *influxdb.Task, now int64) (backend.RunCreation, error) {
	sch, err := cron.Parse(task.EffectiveCron())
	if err != nil {
		return backend.RunCreation{}, err
	}
	latest := int64(0)
	lt, err := time.Parse(time.RFC3339, task.LatestCompleted)
	if err == nil {
		latest = lt.Unix()
	}
	for _, r := range t.runs[task.ID.String()] {
		rt, err := time.Parse(time.RFC3339, r.ScheduledFor)
		if err == nil {
			if rt.Unix() > latest {
				latest = rt.Unix()
			}
		}
	}

	nextScheduled := sch.Next(time.Unix(latest, 0))
	nextScheduledUnix := nextScheduled.Unix()
	offset := int64(0)
	if task.Offset != "" {
		toff, err := time.ParseDuration(task.Offset)
		if err == nil {
			offset = toff.Nanoseconds()
		}
	}
	if dueAt := nextScheduledUnix + int64(offset); dueAt > now {
		return backend.RunCreation{}, backend.RunNotYetDueError{DueAt: dueAt}
	}

	runID := idgen.ID()
	runs, ok := t.runs[task.ID.String()]
	if !ok {
		runs = make(map[string]*influxdb.Run)
	}
	runs[runID.String()] = &influxdb.Run{
		ID:           runID,
		ScheduledFor: nextScheduled.Format(time.RFC3339),
	}
	t.runs[task.ID.String()] = runs

	return backend.RunCreation{
		Created: backend.QueuedRun{
			RunID: runID,
			Now:   nextScheduledUnix,
		},
		NextDue:  sch.Next(nextScheduled).Unix() + offset,
		HasQueue: false,
	}, nil
}

func (d *TaskControlService) FinishRun(_ context.Context, taskID, runID influxdb.ID) (*influxdb.Run, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	tid := taskID.String()
	rid := runID.String()
	r := d.runs[tid][rid]
	delete(d.runs[tid], rid)
	t := d.tasks[tid]
	schedFor, _ := time.Parse(time.RFC3339, r.ScheduledFor)
	latest, _ := time.Parse(time.RFC3339, t.LatestCompleted)
	if schedFor.After(latest) {
		t.LatestCompleted = r.ScheduledFor
	}
	d.finishedRuns[rid] = r
	delete(d.created, tid+rid)
	return r, nil
}

func (t *TaskControlService) CurrentlyRunning(ctx context.Context, taskID influxdb.ID) ([]*influxdb.Run, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rtn := []*influxdb.Run{}
	for _, run := range t.runs[taskID.String()] {
		rtn = append(rtn, run)
	}
	return rtn, nil
}

func (t *TaskControlService) ManualRuns(ctx context.Context, taskID influxdb.ID) ([]*influxdb.Run, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.manualRuns != nil {
		return t.manualRuns, nil
	}
	return []*influxdb.Run{}, nil
}

// NextDueRun returns the Unix timestamp of when the next call to CreateNextRun will be ready.
// The returned timestamp reflects the task's offset, so it does not necessarily exactly match the schedule time.
func (d *TaskControlService) NextDueRun(ctx context.Context, taskID influxdb.ID) (int64, error) {
	task := d.tasks[taskID.String()]
	sch, err := cron.Parse(task.EffectiveCron())
	if err != nil {
		return 0, err
	}
	latest := int64(0)
	lt, err := time.Parse(time.RFC3339, task.LatestCompleted)
	if err == nil {
		latest = lt.Unix()
	}

	for _, r := range d.runs[task.ID.String()] {
		rt, err := time.Parse(time.RFC3339, r.ScheduledFor)
		if err == nil {
			if rt.Unix() > latest {
				latest = rt.Unix()
			}
		}
	}

	nextScheduled := sch.Next(time.Unix(latest, 0))
	nextScheduledUnix := nextScheduled.Unix()
	offset := int64(0)
	if task.Offset != "" {
		toff, err := time.ParseDuration(task.Offset)
		if err == nil {
			offset = toff.Nanoseconds()
		}
	}

	return nextScheduledUnix + int64(offset), nil
}

// UpdateRunState sets the run state at the respective time.
func (d *TaskControlService) UpdateRunState(ctx context.Context, taskID, runID influxdb.ID, when time.Time, state backend.RunStatus) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	run := d.runs[taskID.String()][runID.String()]
	run.Status = state.String()
	switch state {
	case backend.RunStarted:
		run.StartedAt = when.Format(time.RFC3339Nano)
	case backend.RunSuccess, backend.RunFail, backend.RunCanceled:
		run.FinishedAt = when.Format(time.RFC3339Nano)
	}
	return nil
}

// AddRunLog adds a log line to the run.
func (d *TaskControlService) AddRunLog(ctx context.Context, taskID, runID influxdb.ID, when time.Time, log string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	run := d.runs[taskID.String()][runID.String()]
	if run == nil {
		panic("cannot add a log to a non existant run")
	}
	run.Log = append(run.Log, influxdb.Log{Time: when.Format(time.RFC3339Nano), Message: log})
	return nil
}

func (d *TaskControlService) CreatedFor(taskID influxdb.ID) []backend.QueuedRun {
	d.mu.Lock()
	defer d.mu.Unlock()

	var qrs []backend.QueuedRun
	for _, qr := range d.created {
		if qr.TaskID == taskID {
			qrs = append(qrs, qr)
		}
	}

	return qrs
}

// TotalRunsCreatedForTask returns the number of runs created for taskID.
func (d *TaskControlService) TotalRunsCreatedForTask(taskID influxdb.ID) int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.totalRunsCreated[taskID]
}

// PollForNumberCreated blocks for a small amount of time waiting for exactly the given count of created and unfinished runs for the given task ID.
// If the expected number isn't found in time, it returns an error.
//
// Because the scheduler and executor do a lot of state changes asynchronously, this is useful in test.
func (d *TaskControlService) PollForNumberCreated(taskID influxdb.ID, count int) ([]backend.QueuedRun, error) {
	const numAttempts = 50
	actualCount := 0
	var created []backend.QueuedRun
	for i := 0; i < numAttempts; i++ {
		time.Sleep(2 * time.Millisecond) // we sleep even on first so it becomes more likely that we catch when too many are produced.
		created = d.CreatedFor(taskID)
		actualCount = len(created)
		if actualCount == count {
			return created, nil
		}
	}
	return created, fmt.Errorf("did not see count of %d created run(s) for task with ID %s in time, instead saw %d", count, taskID.String(), actualCount) // we return created anyways, to make it easier to debug
}

func (d *TaskControlService) FinishedRun(runID influxdb.ID) *influxdb.Run {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.finishedRuns[runID.String()]
}

func (d *TaskControlService) FinishedRuns() []*influxdb.Run {
	rtn := []*influxdb.Run{}
	for _, run := range d.finishedRuns {
		rtn = append(rtn, run)
	}
	return rtn
}