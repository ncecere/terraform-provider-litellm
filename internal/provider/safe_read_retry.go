package provider

import (
	"context"
	"math/rand"
	"net/http"
	"time"
)

// safeReadRetryPolicy bounds retries for idempotent GET and HEAD operations.
// It is package-local until resource reads are deliberately migrated in a
// later issue #202 phase.
type safeReadRetryPolicy struct {
	maxAttempts   int
	maxElapsed    time.Duration
	initialDelay  time.Duration
	maxDelay      time.Duration
	maxRetryAfter time.Duration
}

// safeReadRetryHooks makes time, sleeping, and jitter deterministic in tests.
// Production callers use real time, a context-aware timer, and equal jitter.
type safeReadRetryHooks struct {
	now        func() time.Time
	sleep      func(context.Context, time.Duration) error
	randomUnit func() float64
}

var defaultSafeReadRetryPolicy = safeReadRetryPolicy{
	maxAttempts:   4,
	maxElapsed:    30 * time.Second,
	initialDelay:  250 * time.Millisecond,
	maxDelay:      4 * time.Second,
	maxRetryAfter: 30 * time.Second,
}

func defaultSafeReadRetryHooks() safeReadRetryHooks {
	return safeReadRetryHooks{
		now: time.Now,
		sleep: func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
		randomUnit: rand.Float64,
	}
}

// DoReadWithResponse performs a bounded, cancellable retry of a GET or HEAD
// request. It retries only safe transient transport failures and exact HTTP
// 408, 429, or 5xx responses. Existing resource and data-source reads continue
// to use the single-attempt API until a later issue #202 migration phase.
func (c *Client) DoReadWithResponse(ctx context.Context, method, requestPath string, body interface{}, result interface{}) error {
	return c.doReadWithResponsePolicy(ctx, method, requestPath, body, result, defaultSafeReadRetryPolicy, defaultSafeReadRetryHooks())
}

func (c *Client) doReadWithResponsePolicy(ctx context.Context, method, requestPath string, body interface{}, result interface{}, policy safeReadRetryPolicy, hooks safeReadRetryHooks) error {
	if method != http.MethodGet && method != http.MethodHead {
		return &safeResponseError{kind: "safe read requests require GET or HEAD"}
	}
	if err := validateSafeReadRetryPolicy(policy, hooks); err != nil {
		return err
	}
	return retrySafeRead(ctx, policy, hooks, func(attemptCtx context.Context) error {
		_, err := c.doRequestWithResponseOptions(attemptCtx, method, requestPath, body, result, clientRequestOptions{now: hooks.now})
		return err
	})
}

func validateSafeReadRetryPolicy(policy safeReadRetryPolicy, hooks safeReadRetryHooks) error {
	if policy.maxAttempts < 1 || policy.maxElapsed <= 0 || policy.initialDelay < 0 ||
		policy.maxDelay < policy.initialDelay || policy.maxRetryAfter < 0 ||
		hooks.now == nil || hooks.sleep == nil || hooks.randomUnit == nil {
		return &safeResponseError{kind: "safe read retry policy is invalid"}
	}
	return nil
}

func retrySafeRead(ctx context.Context, policy safeReadRetryPolicy, hooks safeReadRetryHooks, operation func(context.Context) error) error {
	start := hooks.now()
	retryCtx, cancel := context.WithTimeout(ctx, policy.maxElapsed)
	defer cancel()

	for attempt := 1; attempt <= policy.maxAttempts; attempt++ {
		if err := retryCtx.Err(); err != nil {
			return safeTransportFailure(err)
		}
		if hooks.now().Sub(start) >= policy.maxElapsed {
			return safeTransportFailure(context.DeadlineExceeded)
		}

		err := operation(retryCtx)
		if err == nil {
			return nil
		}
		classification := ClassifyHTTPFailure(err)
		if attempt == policy.maxAttempts || !retryableSafeReadClassification(classification) {
			return err
		}

		delay := safeReadRetryDelay(policy, hooks, attempt)
		if classification.HasRetryAfter {
			retryAfter := classification.RetryAfter
			schedule, hasSchedule := safeRetryScheduleFromError(err)
			if hasSchedule && !schedule.deadline.IsZero() {
				retryAfter = schedule.deadline.Sub(hooks.now())
				if retryAfter < 0 {
					retryAfter = 0
				}
				if retryAfter > policy.maxRetryAfter {
					retryAfter = policy.maxRetryAfter
				}
				// An HTTP-date is an absolute server time, not a duration that
				// restarts after a slow response-body read. The absolute value
				// remains solely on the private provider error wrapper.
				delay = retryAfter
			} else {
				if retryAfter > policy.maxRetryAfter {
					retryAfter = policy.maxRetryAfter
				}
				if retryAfter > delay {
					delay = retryAfter
				}
			}
		}
		remaining := policy.maxElapsed - hooks.now().Sub(start)
		if remaining <= 0 {
			return safeTransportFailure(context.DeadlineExceeded)
		}
		if delay > remaining {
			delay = remaining
		}
		if err := hooks.sleep(retryCtx, delay); err != nil {
			return safeTransportFailure(err)
		}
	}
	return &safeResponseError{kind: "safe read retry attempts were exhausted"}
}

func retryableSafeReadClassification(classification HTTPFailureClassification) bool {
	return classification.Kind == HTTPFailureTransientTransport ||
		classification.Kind == HTTPFailureTransientAcceptedResponse ||
		classification.Kind == HTTPFailureTransientResponse
}

func safeReadRetryDelay(policy safeReadRetryPolicy, hooks safeReadRetryHooks, failedAttempt int) time.Duration {
	base := policy.initialDelay
	for exponent := 1; exponent < failedAttempt && base < policy.maxDelay; exponent++ {
		if base > policy.maxDelay/2 {
			base = policy.maxDelay
			break
		}
		base *= 2
	}
	if base > policy.maxDelay {
		base = policy.maxDelay
	}
	if base <= 0 {
		return 0
	}

	unit := hooks.randomUnit()
	if unit < 0 {
		unit = 0
	} else if unit > 1 {
		unit = 1
	}
	minimum := base / 2
	return minimum + time.Duration(float64(base-minimum)*unit)
}
