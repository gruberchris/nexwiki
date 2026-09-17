package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestShutdownContextsFitTheStopGrace pins the shutdown budget: the whole sequence ends within
// shutdownTimeout, under a container runtime's 10s stop grace, and the writers' deadline comes
// storageCloseReserve before storage's, so writers that use all of their time still leave storage
// time to wait for a write in progress.
func TestShutdownContextsFitTheStopGrace(t *testing.T) {
	if shutdownTimeout >= 10*time.Second {
		t.Errorf("shutdownTimeout %s must stay under the 10s stop grace", shutdownTimeout)
	}
	if storageCloseReserve <= 0 || storageCloseReserve >= shutdownTimeout {
		t.Errorf("storageCloseReserve %s must be a positive part of shutdownTimeout %s", storageCloseReserve, shutdownTimeout)
	}

	start := time.Now()
	stopCtx, closeCtx, cancel := shutdownContexts()
	stopDeadline, okStop := stopCtx.Deadline()
	closeDeadline, okClose := closeCtx.Deadline()
	if !okStop || !okClose {
		t.Fatal("both shutdown contexts must carry a deadline")
	}
	if closeDeadline.After(time.Now().Add(shutdownTimeout)) || closeDeadline.Before(start.Add(shutdownTimeout)) {
		t.Errorf("storage's deadline is %s after start, want shutdownTimeout (%s)", closeDeadline.Sub(start), shutdownTimeout)
	}
	if got := closeDeadline.Sub(stopDeadline); got != storageCloseReserve {
		t.Errorf("the writers' deadline comes %s before storage's, want storageCloseReserve (%s)", got, storageCloseReserve)
	}

	cancel()
	if stopCtx.Err() == nil || closeCtx.Err() == nil {
		t.Error("cancel must release both contexts")
	}
}

// TestOpenSignalCloseCountsFromTheSignal pins the deadline for closing storage after a signal that
// arrived while it opened, and the line logged about it: shutdownTimeout from the signal, not from the
// open finishing, while that leaves storage at least storageCloseReserve, and storageCloseReserve from
// the open finishing once it does not, never a deadline already passed. The line claims the deadline
// passed only when it did, decided from the exact times rather than the rounded figure it shows.
func TestOpenSignalCloseCountsFromTheSignal(t *testing.T) {
	// An open that finished as the budget ran out must still get the minimum inside the 10s stop grace.
	if shutdownTimeout+storageCloseReserve >= 10*time.Second {
		t.Errorf("shutdownTimeout %s plus the minimum close time %s must stay under the 10s stop grace", shutdownTimeout, storageCloseReserve)
	}

	const (
		graceful = "Storage finished opening: shutting down gracefully..."
		short    = "less than 1s before the 5s shutdown deadline: allowing up to 1s to close it"
		past     = "past the 5s shutdown deadline: allowing up to 1s to close it"
	)
	signalled := time.Date(2026, time.September, 17, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name           string
		openTook       time.Duration // from the signal to storage finishing opening
		want           time.Duration // from the signal to the deadline
		deadlinePassed bool
		logLine        string // a part of the line logged
		notInLog       string
	}{
		{"open finished at once", 0, shutdownTimeout, false, graceful, "deadline"},
		{"open finished well within the budget", 3 * time.Second, shutdownTimeout, false, graceful, "deadline"},
		{"open left exactly the minimum", shutdownTimeout - storageCloseReserve, shutdownTimeout, false, graceful, "deadline"},
		{"open left less than the minimum", 4500 * time.Millisecond, 5500 * time.Millisecond, false, "opening 4.5s after the signal, " + short, "past"},
		{"open finished 3ms before the deadline", 4997 * time.Millisecond, 5997 * time.Millisecond, false, "opening 4.997s after the signal, " + short, "past"},
		{"open finished right at the deadline", shutdownTimeout, shutdownTimeout + storageCloseReserve, false, "opening 5s after the signal, " + short, "past"},
		{"open finished 3ms after the deadline", 5003 * time.Millisecond, 6003 * time.Millisecond, true, "opening 5.003s after the signal, " + past, "less than"},
		{"open finished a fraction of a millisecond after the deadline", shutdownTimeout + 400*time.Microsecond, 6*time.Second + 400*time.Microsecond, true, "opening 5.001s after the signal, " + past, "less than"},
		{"open outlasted the budget", 6 * time.Second, 7 * time.Second, true, "opening 6s after the signal, " + past, "less than"},
		{"open outlasted the budget by far", 30 * time.Second, 31 * time.Second, true, "opening 30s after the signal, " + past, "less than"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deadline, deadlinePassed, logLine := openSignalClose(signalled, signalled.Add(tc.openTook))
			if got := deadline.Sub(signalled); got != tc.want {
				t.Errorf("deadline is %s after the signal, want %s", got, tc.want)
			}
			if deadlinePassed != tc.deadlinePassed {
				t.Errorf("deadlinePassed = %t, want %t", deadlinePassed, tc.deadlinePassed)
			}
			if !strings.Contains(logLine, tc.logLine) {
				t.Errorf("logged %q, want it to say %q", logLine, tc.logLine)
			}
			if strings.Contains(logLine, tc.notInLog) {
				t.Errorf("logged %q, which wrongly says %q", logLine, tc.notInLog)
			}
		})
	}
}

// TestShutdownSummaryNamesTheDeadlineThatPassed pins that the final shutdown line claims a clean
// shutdown only when nothing ran out, names the overall deadline as passing while closing storage only
// when it did, and names the close allowance instead when the deadline had passed before closing began.
func TestShutdownSummaryNamesTheDeadlineThatPassed(t *testing.T) {
	overall := shutdownTimeout.String()
	writers := (shutdownTimeout - storageCloseReserve).String()
	var (
		clean         = []string{"cleanly"}
		writersPassed = []string{writers + " deadline for stopping writers passed", "storage still closed within the " + overall}
		closePassed   = []string{overall + " shutdown deadline passed while closing storage"}
		allowanceOut  = []string{storageCloseReserve.String() + " allowed for closing storage ran out", overall + " shutdown deadline had already passed while storage was opening"}
	)
	for _, tc := range []struct {
		name                                                    string
		writersOverran, closeOverran, deadlinePassedBeforeClose bool
		want, notWant                                           []string
	}{
		{"nothing overran", false, false, false, clean, []string{"deadline", "allowed"}},
		{"only the writers overran", true, false, false, writersPassed, []string{"cleanly", "while closing", "allowed"}},
		{"closing overran the deadline", false, true, false, closePassed, []string{"cleanly", "allowed", "already passed"}},
		{"writers and closing overran the deadline", true, true, false, closePassed, []string{"cleanly", "still closed within", "allowed"}},
		{"closed within the allowance", false, false, true, clean, []string{"deadline", "allowed"}},
		{"closing overran the allowance", false, true, true, allowanceOut, []string{"cleanly", "passed while closing"}},
		// Not a combination main produces, since the writers never start when storage opens late.
		{"only the writers overran, deadline passed before close", true, false, true, writersPassed, []string{"cleanly", "allowed"}},
		{"writers overran and closing overran the allowance", true, true, true, allowanceOut, []string{"cleanly", "passed while closing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := shutdownSummary(tc.writersOverran, tc.closeOverran, tc.deadlinePassedBeforeClose)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("summary %q does not say %q", got, w)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(got, w) {
					t.Errorf("summary %q wrongly says %q", got, w)
				}
			}
		})
	}
}

// TestCloseAfterOpenSignalSummarizesWhatRanOut pins the summary main logs last after closing storage
// that opened during a signal, with a close that returns at once or only when its deadline runs out.
// When the open ended past the shutdown deadline, a close that runs out has used up its allowance, and
// the deadline must not be reported as passing while storage closed.
func TestCloseAfterOpenSignalSummarizesWhatRanOut(t *testing.T) {
	quick := func(context.Context) error { return nil }
	slow := func(ctx context.Context) error { <-ctx.Done(); return nil }
	for _, tc := range []struct {
		name     string
		openTook time.Duration
		close    func(context.Context) error
		want     string
	}{
		{"open and close in time", time.Second, quick, "NexWiki shut down cleanly."},
		{"open finished past the deadline, close in time", 6 * time.Second, quick, "NexWiki shut down cleanly."},
		{"open finished before the deadline, close ran past it", 4500 * time.Millisecond, slow, shutdownTimeout.String() + " shutdown deadline passed while closing storage"},
		{"open finished past the deadline, close ran out of its allowance", 6 * time.Second, slow, storageCloseReserve.String() + " allowed for closing storage ran out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel() // the slow closes each wait out a real deadline, at most storageCloseReserve away
			openedAt := time.Now()
			got := closeAfterOpenSignal(tc.close, openedAt.Add(-tc.openTook), openedAt)
			if !strings.Contains(got, tc.want) {
				t.Errorf("summary %q does not say %q", got, tc.want)
			}
		})
	}
}
