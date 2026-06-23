package engine

import (
	"crypto/rand"
	"errors"
	"math/big"
	"time"

	"primeradiant.com/toil/internal/definitions"
)

// RetryableError wraps an error to indicate it may succeed on retry.
type RetryableError struct {
	Err error
}

func (e *RetryableError) Error() string { return e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

// IsRetryable returns true if the error (or any wrapped error) is a RetryableError.
func IsRetryable(err error) bool {
	var re *RetryableError
	return errors.As(err, &re)
}

// Retryable wraps an error to mark it as retryable.
func Retryable(err error) error {
	return &RetryableError{Err: err}
}

// retryDelay calculates the delay before a retry attempt.
func retryDelay(policy *definitions.RetryPolicy, attempt int) time.Duration {
	initial := parseDurationOrDefault(policy.InitialDelay, 1*time.Second)
	maxDelay := parseDurationOrDefault(policy.MaxDelay, 30*time.Second)

	var delay time.Duration
	if policy.Backoff == backoffFixed {
		delay = initial
	} else {
		// exponential: initial * 2^(attempt-1), capped to avoid overflow
		exp := attempt - 1
		if exp > 62 {
			delay = maxDelay
		} else {
			delay = initial * (1 << uint(exp))
		}
	}
	if delay > maxDelay {
		delay = maxDelay
	}
	if policy.Jitter {
		// Apply +/- 50% jitter
		jitterRange := delay / 2
		if jitterRange > 0 {
			n, _ := rand.Int(rand.Reader, big.NewInt(int64(jitterRange*2)))
			delay = delay - jitterRange + time.Duration(n.Int64())
		}
	}
	return delay
}

func parseDurationOrDefault(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}
