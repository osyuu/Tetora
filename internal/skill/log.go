package skill

import (
	"context"

	"tetora/internal/log"
)

func logDebug(msg string, fields ...any) { log.Debug(msg, fields...) }
func logInfo(msg string, fields ...any)  { log.Info(msg, fields...) }
func logWarn(msg string, fields ...any)  { log.Warn(msg, fields...) }

func logInfoCtx(ctx context.Context, msg string, fields ...any)  { log.InfoCtx(ctx, msg, fields...) }
func logDebugCtx(ctx context.Context, msg string, fields ...any) { log.DebugCtx(ctx, msg, fields...) }

var _ = context.Background
