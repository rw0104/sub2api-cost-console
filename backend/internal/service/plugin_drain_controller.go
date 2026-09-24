package service

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// pluginDrainTimeout is the single deadline used by lifecycle operations that
// retire one or more plugin runtimes. A controller shares this deadline across
// all runtimes instead of waiting for timeout once per runtime.
const pluginDrainTimeout = 10 * time.Second

type DrainState string

const (
	DrainStatePending   DrainState = "pending"
	DrainStateRequested DrainState = "requested"
	DrainStateDraining  DrainState = "draining"
	DrainStateForced    DrainState = "forced"
	DrainStateFinished  DrainState = "finished"
)

// DrainInstanceProgress is the host-owned lifecycle state for one runtime.
// It intentionally contains identifiers and counters only; plugin error text
// and request payloads never enter this snapshot.
type DrainInstanceProgress struct {
	PluginID      int64      `json:"plugin_id"`
	InstanceID    string     `json:"instance_id"`
	State         DrainState `json:"state"`
	InFlight      int64      `json:"in_flight"`
	RequestedAt   time.Time  `json:"requested_at,omitempty"`
	FinishedAt    time.Time  `json:"finished_at,omitempty"`
	LastErrorCode string     `json:"last_error_code,omitempty"`
}

// DrainProgress is a point-in-time, host-owned snapshot of a drain operation.
// The deadline applies to the whole operation, including all instances.
type DrainProgress struct {
	OperationID string                  `json:"operation_id"`
	State       DrainState              `json:"state"`
	StartedAt   time.Time               `json:"started_at,omitempty"`
	Deadline    time.Time               `json:"deadline,omitempty"`
	FinishedAt  time.Time               `json:"finished_at,omitempty"`
	Total       int                     `json:"total"`
	Requested   int                     `json:"requested"`
	Draining    int                     `json:"draining"`
	Finished    int                     `json:"finished"`
	Forced      int                     `json:"forced"`
	Instances   []DrainInstanceProgress `json:"instances"`
}

type drainInstance struct {
	runtime  *pluginRuntime
	progress DrainInstanceProgress
}

// DrainController drains a set of runtimes concurrently under one bounded
// deadline. Run is synchronous so lifecycle callers can preserve their
// existing ordering, while Snapshot remains safe to call from status handlers
// while Run is in progress.
type DrainController struct {
	mu          sync.RWMutex
	operationID string
	timeout     time.Duration
	startedAt   time.Time
	deadline    time.Time
	state       DrainState
	finishedAt  time.Time
	instances   []*drainInstance
	runOnce     sync.Once
	done        chan struct{}
}

func NewDrainController(runtimes []*pluginRuntime, timeout time.Duration) *DrainController {
	if timeout <= 0 {
		timeout = pluginDrainTimeout
	}
	controller := &DrainController{
		operationID: fmt.Sprintf("drain-%d", time.Now().UnixNano()),
		timeout:     timeout,
		state:       DrainStatePending,
		done:        make(chan struct{}),
	}
	seen := make(map[*pluginRuntime]struct{}, len(runtimes))
	for _, runtime := range runtimes {
		if runtime == nil {
			continue
		}
		if _, ok := seen[runtime]; ok {
			continue
		}
		seen[runtime] = struct{}{}
		pluginID := int64(0)
		if runtime.installation != nil {
			pluginID = runtime.installation.ID
		}
		controller.instances = append(controller.instances, &drainInstance{
			runtime: runtime,
			progress: DrainInstanceProgress{
				PluginID:   pluginID,
				InstanceID: runtime.instanceID,
				State:      DrainStatePending,
			},
		})
	}
	return controller
}

// drainPluginRuntimes is the manager-facing helper used by lifecycle paths.
// It deliberately returns the final snapshot so callers that own an operation
// can persist or expose the progress without reaching into runtime internals.
func drainPluginRuntimes(ctx context.Context, runtimes []*pluginRuntime, timeout time.Duration) DrainProgress {
	return NewDrainController(runtimes, timeout).Run(ctx)
}

// Run starts all drains together and waits until every runtime has finished or
// the shared deadline expires. It is safe to call Run more than once; only the
// first invocation performs lifecycle work.
func (c *DrainController) Run(ctx context.Context) DrainProgress {
	if c == nil {
		return DrainProgress{State: DrainStateFinished}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.runOnce.Do(func() {
		c.mu.Lock()
		c.startedAt = time.Now().UTC()
		c.deadline = c.startedAt.Add(c.timeout)
		c.state = DrainStateDraining
		instances := append([]*drainInstance(nil), c.instances...)
		c.mu.Unlock()

		runCtx, cancel := context.WithTimeout(ctx, c.timeout)
		defer cancel()
		var wg sync.WaitGroup
		wg.Add(len(instances))
		for _, instance := range instances {
			go func(instance *drainInstance) {
				defer wg.Done()
				c.drainOne(runCtx, instance)
			}(instance)
		}
		wg.Wait()

		c.mu.Lock()
		c.finishedAt = time.Now().UTC()
		c.state = DrainStateFinished
		for _, instance := range c.instances {
			if instance.progress.State == DrainStateForced {
				c.state = DrainStateForced
				break
			}
		}
		c.mu.Unlock()
		close(c.done)
	})
	return c.Snapshot()
}

// Wait returns the final snapshot, or the current snapshot if the controller
// has not been started. It is useful to expose progress without duplicating
// lifecycle synchronization in callers.
func (c *DrainController) Wait(ctx context.Context) DrainProgress {
	if c == nil {
		return DrainProgress{State: DrainStateFinished}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.RLock()
	pending := c.state == DrainStatePending
	c.mu.RUnlock()
	if pending {
		return c.Snapshot()
	}
	select {
	case <-c.done:
	case <-ctx.Done():
	}
	return c.Snapshot()
}

func (c *DrainController) drainOne(ctx context.Context, instance *drainInstance) {
	c.mu.Lock()
	instance.progress.State = DrainStateRequested
	instance.progress.RequestedAt = time.Now().UTC()
	instance.progress.InFlight = instance.runtime.inFlight.Load()
	instance.progress.State = DrainStateDraining
	c.mu.Unlock()

	forced := drainRuntimeUntil(ctx, instance.runtime)

	c.mu.Lock()
	instance.progress.InFlight = instance.runtime.inFlight.Load()
	instance.progress.FinishedAt = time.Now().UTC()
	if forced {
		instance.progress.State = DrainStateForced
		instance.progress.LastErrorCode = "DRAIN_DEADLINE_EXCEEDED"
	} else {
		instance.progress.State = DrainStateFinished
	}
	c.mu.Unlock()
}

// Snapshot copies all mutable fields so callers can safely serialize it while
// drains are running. Instance order is stable and follows the input list.
func (c *DrainController) Snapshot() DrainProgress {
	if c == nil {
		return DrainProgress{State: DrainStateFinished}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	progress := DrainProgress{
		OperationID: c.operationID,
		State:       c.state,
		StartedAt:   c.startedAt,
		Deadline:    c.deadline,
		FinishedAt:  c.finishedAt,
		Total:       len(c.instances),
		Instances:   make([]DrainInstanceProgress, 0, len(c.instances)),
	}
	for _, instance := range c.instances {
		item := instance.progress
		item.InFlight = instance.runtime.inFlight.Load()
		progress.Instances = append(progress.Instances, item)
		switch item.State {
		case DrainStateRequested:
			progress.Requested++
		case DrainStateDraining:
			progress.Requested++
			progress.Draining++
		case DrainStateForced:
			progress.Requested++
			progress.Forced++
			progress.Finished++
		case DrainStateFinished:
			progress.Requested++
			progress.Finished++
		}
	}
	return progress
}

// drainRuntimeUntil is the context-aware counterpart of pluginRuntime.drain.
// The legacy method remains unchanged for older internal callers; new batch
// lifecycle paths use this helper to share one operation deadline.
func drainRuntimeUntil(ctx context.Context, runtime *pluginRuntime) bool {
	if runtime == nil {
		return false
	}
	runtime.markRuntimeDrainRequested()
	runtime.draining.Store(true)
	if runtime.inFlight.Load() == 0 && runtime.done != nil {
		runtime.doneOnce.Do(func() { close(runtime.done) })
	}
	forced := false
	if runtime.done != nil {
		select {
		case <-runtime.done:
		default:
			select {
			case <-runtime.done:
			case <-ctx.Done():
				forced = true
			}
		}
	}
	runtime.markRuntimeDrainFinished()
	runtime.kill()
	return forced
}
