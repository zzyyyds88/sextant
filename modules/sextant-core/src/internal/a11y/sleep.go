package a11y

import (
	"context"
	"time"
)

func sleepCtx(ctx context.Context, seconds int) <-chan time.Time {
	return time.After(time.Duration(seconds) * time.Second)
}
