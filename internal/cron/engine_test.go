package cron

import (
	"testing"
	"time"
)

// TestSameMinuteSuppression verifies the sameMinute guard logic used to
// suppress spurious skipped_concurrent_limit records when the 30s ticker
// fires twice within the same cron minute.
//
// The production logic is:
//
//	sameMinute := !j.runStart.IsZero() &&
//	    j.runStart.In(j.loc).Truncate(time.Minute).Equal(nowLocal.Truncate(time.Minute))
func TestSameMinuteSuppression(t *testing.T) {
	loc := time.UTC

	// base: job started at 09:00:05
	runStart := time.Date(2026, 3, 27, 9, 0, 5, 0, loc)

	tests := []struct {
		name       string
		runStart   time.Time
		nowLocal   time.Time
		wantSuppress bool // true = sameMinute (suppress noise)
	}{
		{
			name:         "30s tick within same minute → suppress",
			runStart:     runStart,
			nowLocal:     time.Date(2026, 3, 27, 9, 0, 35, 0, loc), // 30s later, same minute
			wantSuppress: true,
		},
		{
			name:         "next minute fire → do not suppress (real conflict)",
			runStart:     runStart,
			nowLocal:     time.Date(2026, 3, 27, 9, 1, 5, 0, loc), // 1m later
			wantSuppress: false,
		},
		{
			name:         "zero runStart → do not suppress (job not running)",
			runStart:     time.Time{},
			nowLocal:     time.Date(2026, 3, 27, 9, 0, 35, 0, loc),
			wantSuppress: false,
		},
		{
			name:         "yesterday's run still active → do not suppress",
			runStart:     time.Date(2026, 3, 26, 9, 0, 5, 0, loc),
			nowLocal:     time.Date(2026, 3, 27, 9, 0, 5, 0, loc),
			wantSuppress: false,
		},
		{
			name:         "exact same second → suppress",
			runStart:     runStart,
			nowLocal:     runStart,
			wantSuppress: true,
		},
		{
			name:         "59s into same minute → suppress",
			runStart:     time.Date(2026, 3, 27, 9, 0, 0, 0, loc),
			nowLocal:     time.Date(2026, 3, 27, 9, 0, 59, 999999999, loc),
			wantSuppress: true,
		},
		{
			name:         "boundary: rollover to next minute → do not suppress",
			runStart:     time.Date(2026, 3, 27, 9, 0, 59, 0, loc),
			nowLocal:     time.Date(2026, 3, 27, 9, 1, 0, 0, loc),
			wantSuppress: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j := &cronJob{loc: loc}
			j.runStart = tt.runStart

			sameMinute := !j.runStart.IsZero() &&
				j.runStart.In(j.loc).Truncate(time.Minute).Equal(tt.nowLocal.Truncate(time.Minute))

			if sameMinute != tt.wantSuppress {
				t.Errorf("sameMinute = %v, want %v (runStart=%v, now=%v)",
					sameMinute, tt.wantSuppress, tt.runStart, tt.nowLocal)
			}
		})
	}
}
