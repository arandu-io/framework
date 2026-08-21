// The one assertion that cannot be made from outside this package.
//
// isLocked decides, by the text of an error, whether a Singleton task was
// skipped because another replica is doing it or failed for a reason worth
// reporting. It is unexported, so this file is in the package rather than
// beside it.
//
// The failure it guards against is silent in both directions. A refusal
// isLocked does not recognize is reported as a failed task, every minute, on
// every replica that lost the race. A failure it recognizes by accident is a
// task that never runs and never says so.

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/hesape/cache"
)

// TestIsLockedRecognizesARealRefusal wires the locker the framework hands out
// to the function that reads its errors.
//
// Both halves are asserted from one real lock: the refusal a held lock produces
// has to be recognized, and the error the work itself returned has to not be --
// a check that answered yes to everything would pass the first half alone.
func TestIsLockedRecognizesARealRefusal(t *testing.T) {
	locker := foundation.NewLocker(cache.NewLocks(cache.NewArrayStore()))

	holding := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		_ = locker.Run(context.Background(), "reports.monthly", time.Minute,
			func(context.Context) error {
				close(holding)
				<-release
				return nil
			})
	}()
	<-holding

	refusal := locker.Run(context.Background(), "reports.monthly", time.Minute,
		func(context.Context) error {
			t.Error("the work ran while the lock was held")
			return nil
		})

	if !isLocked(refusal) {
		t.Fatalf("isLocked does not recognize %v: a held lock would be reported as a failed task on every replica that lost the race", refusal)
	}

	close(release)
	<-done

	// The other direction: work that genuinely failed must not be mistaken for
	// a lock somebody else holds, or the failure is swallowed and the task
	// looks like it ran somewhere.
	failure := locker.Run(context.Background(), "reports.monthly", time.Minute,
		func(context.Context) error { return errors.New("the report has no rows") })

	if isLocked(failure) {
		t.Errorf("isLocked treats %v as a held lock, so a real failure would never be reported", failure)
	}
}
