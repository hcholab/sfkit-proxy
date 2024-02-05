package util

import (
	"context"
	"errors"
	"net"

	"github.com/cenkalti/backoff/v4"
)

func Go(ctx context.Context, errs chan<- error, op func() error) {
	go func() {
		select {
		case <-ctx.Done():
		case errs <- op():
		}
	}()
}

// Retry retries a given operation until the operation
// returns a PermanentError, or the context is canceled.
func Retry(ctx context.Context, op backoff.Operation) func() error {
	return func() (err error) {
		for {
			b := backoff.NewExponentialBackOff()
			b.MaxElapsedTime = 0
			bCtx := backoff.WithContext(b, ctx)

			if err = backoff.Retry(op, bCtx); err != nil {
				return
			}
		}
	}
}

func Permanent(err error) error {
	if e, ok := err.(*backoff.PermanentError); ok {
		return e
	}
	return backoff.Permanent(err)
}

func IsTimeout(err error) bool {
	if e, ok := err.(*backoff.PermanentError); ok {
		return IsTimeout(e.Err)
	}
	var nerr net.Error
	return errors.As(err, &nerr) && nerr.Timeout()
}

func IsCanceledOrTimeout(err error) bool {
	return errors.Is(err, context.Canceled) || IsTimeout(err)
}
