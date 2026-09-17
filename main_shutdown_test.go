package main

import (
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

// TestShutdownSummaryNamesTheDeadlineThatPassed pins that the final shutdown line claims a clean
// shutdown only when no deadline passed, and names the overall deadline only when it did.
func TestShutdownSummaryNamesTheDeadlineThatPassed(t *testing.T) {
	overall := shutdownTimeout.String()
	writers := (shutdownTimeout - storageCloseReserve).String()
	for _, tc := range []struct {
		name                         string
		writersOverran, closeOverran bool
		want, notWant                []string
	}{
		{"nothing overran", false, false, []string{"cleanly"}, []string{"deadline"}},
		{"only the writers overran", true, false,
			[]string{writers + " deadline for stopping writers passed", "storage still closed within the " + overall}, []string{"cleanly", "while closing"}},
		{"closing overran", false, true, []string{overall + " shutdown deadline passed while closing storage"}, []string{"cleanly"}},
		{"both overran", true, true, []string{overall + " shutdown deadline passed while closing storage"}, []string{"cleanly", "still closed within"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := shutdownSummary(tc.writersOverran, tc.closeOverran)
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
