package feature

import (
	"context"
	"testing"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/hesape/auth"
)

// TestTheApplicationAnswersTheSchedulerItComposed: after the boot, a screen
// reaches the scheduler Configure built -- the application's tasks are listed
// and one runs now -- and before it there is none to reach.
func TestTheApplicationAnswersTheSchedulerItComposed(t *testing.T) {
	ran := 0
	app := mustConfigure(t, func(b *foundation.ApplicationBuilder) {
		b.WithSchedule(foundation.Task{ID: "notes.digest", Spec: "0 3 * * *", Run: func(context.Context, auth.Grant) error {
			ran++
			return nil
		}})
	})
	if app.Scheduler() != nil {
		t.Fatal("a scheduler was answered before the boot built it")
	}
	if err := app.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { _ = app.Shutdown() })

	s := app.Scheduler()
	if s == nil {
		t.Fatal("no scheduler after the boot")
	}
	listed := false
	for _, task := range s.List() {
		listed = listed || task.ID == "notes.digest"
	}
	if !listed {
		t.Errorf("the scheduler lists %+v, want notes.digest", s.List())
	}
	if err := s.RunNow(context.Background(), "notes.digest", ""); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if ran != 1 {
		t.Errorf("the task ran %d times, want once", ran)
	}

	if foundation.New(testConfig("dev")).Scheduler() != nil {
		t.Error("an Application made by New answered a scheduler it never composed")
	}
}
