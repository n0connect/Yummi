package skillcurator

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Worker runs curator LLM calls through a bounded, shutdown-aware queue.
// Task completion only enqueues; it never starts an untracked background
// goroutine or leaves an unlimited context.Background call behind.
type Worker struct {
	curator *Curator
	queue   chan Trajectory
	timeout time.Duration
	once    sync.Once
	done    chan struct{}
}

func NewWorker(curator *Curator, capacity int, timeout time.Duration) *Worker {
	if capacity <= 0 {
		capacity = 32
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return &Worker{curator: curator, queue: make(chan Trajectory, capacity), timeout: timeout, done: make(chan struct{})}
}

func (w *Worker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	w.once.Do(func() {
		go func() {
			defer close(w.done)
			for {
				select {
				case <-ctx.Done():
					return
				case traj := <-w.queue:
					jobCtx, cancel := context.WithTimeout(ctx, w.timeout)
					w.curator.ProposeFromTask(jobCtx, traj)
					cancel()
				}
			}
		}()
	})
}

// Submit is deliberately non-blocking so a full optional learning queue can
// never delay or fail an otherwise-completed security task.
func (w *Worker) Submit(traj Trajectory) bool {
	if w == nil || w.curator == nil {
		return false
	}
	select {
	case w.queue <- SanitizeTrajectory(traj):
		return true
	default:
		slog.Warn("skillcurator: queue full, dropping trajectory", "task_id", traj.TaskID)
		return false
	}
}

func (w *Worker) Wait(ctx context.Context) error {
	if w == nil {
		return nil
	}
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
