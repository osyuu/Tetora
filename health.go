package main

// health.go is a thin facade wrapping internal/health.
// Business logic lives in internal/health/; this file bridges globals/Config.

import (
	"fmt"
	"tetora/internal/health"
	"time"
)

// deepHealthCheck performs a comprehensive health check and returns a structured report.
func deepHealthCheck(cfg *Config, state *dispatchState, cron *CronEngine, startTime time.Time) map[string]any {
	input := health.CheckInput{
		Version:      tetoraVersion,
		StartTime:    startTime,
		BaseDir:      cfg.baseDir,
		DiskBlockMB:  cfg.DiskBlockMB,
		DiskWarnMB:   cfg.DiskWarnMB,
		DiskBudgetGB: cfg.DiskBudgetGB,
	}

	// DB check callback.
	if cfg.HistoryDB != "" {
		input.DBCheck = func() (int, error) {
			rows, err := queryDB(cfg.HistoryDB, "SELECT count(*) as cnt FROM job_runs;")
			if err != nil {
				return 0, err
			}
			count := 0
			if len(rows) > 0 {
				if v, ok := rows[0]["cnt"]; ok {
					fmt.Sscanf(fmt.Sprint(v), "%d", &count)
				}
			}
			return count, nil
		}
		input.DBPath = cfg.HistoryDB
	}

	// Providers.
	providers := map[string]health.ProviderInfo{}
	if cfg.registry != nil {
		for name := range cfg.Providers {
			pi := health.ProviderInfo{
				Type:   cfg.Providers[name].Type,
				Status: "ok",
			}
			if cfg.circuits != nil {
				cb := cfg.circuits.Get(name)
				st := cb.State()
				pi.Circuit = st.String()
				if st == CircuitOpen {
					pi.Status = "open"
				} else if st == CircuitHalfOpen {
					pi.Status = "recovering"
				}
			}
			providers[name] = pi
		}
		// Always include default "claude" provider.
		if _, exists := providers["claude"]; !exists {
			pi := health.ProviderInfo{Type: "claude-cli", Status: "ok"}
			if cfg.circuits != nil {
				cb := cfg.circuits.Get("claude")
				st := cb.State()
				pi.Circuit = st.String()
				if st == CircuitOpen {
					pi.Status = "open"
				}
			}
			providers["claude"] = pi
		}
	}
	input.Providers = providers

	// Dispatch state.
	input.DispatchJSON = state.statusJSON()

	// Cron.
	if cron != nil {
		jobs := cron.ListJobs()
		running := 0
		enabled := 0
		for _, j := range jobs {
			if j.Running {
				running++
			}
			if j.Enabled {
				enabled++
			}
		}
		input.Cron = &health.CronSummary{Total: len(jobs), Enabled: enabled, Running: running}
	}

	// Circuit breakers summary.
	if cfg.circuits != nil {
		input.CircuitStatus = cfg.circuits.Status()
	}

	// Offline queue.
	if cfg.OfflineQueue.Enabled && cfg.HistoryDB != "" {
		input.Queue = &health.QueueInfo{
			Pending: countPendingQueue(cfg.HistoryDB),
			Max:     cfg.OfflineQueue.maxItemsOrDefault(),
		}
	}

	return health.DeepCheck(input)
}

// degradeStatus returns the worse of the current and proposed status.
// Order: healthy < degraded < unhealthy.
func degradeStatus(current, proposed string) string {
	return health.DegradeStatus(current, proposed)
}

// diskInfo returns free disk space info for the given path.
func diskInfo(path string) map[string]any {
	return health.DiskInfo(path)
}

// diskFreeBytes returns free disk space in bytes for the given path.
func diskFreeBytes(path string) uint64 {
	return health.DiskFreeBytes(path)
}
