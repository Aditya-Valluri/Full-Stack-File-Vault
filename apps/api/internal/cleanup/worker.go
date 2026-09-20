package cleanup

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Run schedules bounded cycles. Cancellation stops scheduling and cancels current
// database work; callers close pools/storage only after Run returns.
func (c *Collector) Run(ctx context.Context, interval time.Duration, logger *slog.Logger) error {
	if interval < time.Second || interval > time.Hour || logger == nil {
		return errors.New("cleanup interval must be 1s..1h and logger is required")
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			report, err := c.RunOnce(ctx)
			if ctx.Err() != nil {
				return nil
			}
			if err != nil {
				logger.Error("cleanup cycle incomplete", "code", "CLEANUP_RETRY")
			}
			logger.Info("cleanup cycle", "retired", report.Retired, "removed", report.Removed, "grants_pruned", report.GrantsPruned, "temporary_removed", report.TemporaryRemoved)
			timer.Reset(interval)
		}
	}
}
