package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// HTTPFailureKind is the content-free category of a failed HTTP operation.
// It deliberately carries no URL, response text, header value, or transport
// cause.
type HTTPFailureKind uint8

const (
	HTTPFailureNone HTTPFailureKind = iota
	HTTPFailureCanceled
	HTTPFailureDeadline
	HTTPFailureTransientTransport
	HTTPFailureTransientAcceptedResponse
	HTTPFailureTransientResponse
	HTTPFailureTerminalResponse
	HTTPFailureContractOrLocal
)

// HTTPFailureClassification contains only values that are safe to use for
// retry and mutation-uncertainty decisions. ResponseAccepted means that a 2xx
// response was received; it does not mean that its body satisfied the caller's
// response contract.
type HTTPFailureClassification struct {
	Kind HTTPFailureKind

	StatusCode        int
	RetryAfter        time.Duration
	HasRetryAfter     bool
	RequestDispatched bool
	ResponseAccepted  bool
}

type httpStatusCandidate struct {
	code      int
	set       bool
	ambiguous bool
}

func (c *httpStatusCandidate) add(statusCode int) {
	if statusCode < 100 || statusCode > 599 {
		return
	}
	if !c.set {
		c.code = statusCode
		c.set = true
		return
	}
	if c.code != statusCode {
		c.ambiguous = true
	}
}

func (c httpStatusCandidate) value() int {
	if !c.set || c.ambiguous {
		return 0
	}
	return c.code
}

type httpFailureTraits struct {
	canceled           bool
	terminal           bool
	deadline           bool
	transientTransport bool
	transientAccepted  bool
	transientStatus    bool
	terminalStatus     bool
	dispatched         bool
	accepted           bool
	transientCode      httpStatusCandidate
	terminalCode       httpStatusCandidate
	acceptedCode       httpStatusCandidate
	contractCode       httpStatusCandidate
}

// ClassifyHTTPFailure walks the complete error tree and aggregates only
// content-free traits. Provider sanitizer nodes are classification boundaries:
// their raw cause has already been discarded and their synthetic identity must
// not be mistaken for a stronger trait (for example, a net timeout identity is
// not an explicit context deadline).
func ClassifyHTTPFailure(err error) HTTPFailureClassification {
	if err == nil {
		return HTTPFailureClassification{Kind: HTTPFailureNone}
	}

	var traits httpFailureTraits
	walkErrorTreeControlled(err, func(node error) bool {
		switch typed := node.(type) {
		case *safeTransportError:
			traits.canceled = traits.canceled || typed.canceled
			traits.terminal = traits.terminal || typed.terminal
			traits.deadline = traits.deadline || typed.deadline
			traits.transientTransport = traits.transientTransport || typed.safeReadTransient || typed.Retryable()
			traits.dispatched = traits.dispatched || typed.dispatched
			return false
		case *safeResponseError:
			traits.canceled = traits.canceled || typed.canceled
			traits.terminal = traits.terminal || typed.terminal
			traits.deadline = traits.deadline || typed.deadline
			traits.dispatched = traits.dispatched || typed.dispatched
			traits.accepted = traits.accepted || typed.accepted

			if typed.accepted {
				if typed.stage == safeResponseFailureAcceptedBodyRead && typed.safeReadTransient {
					traits.transientAccepted = true
					traits.acceptedCode.add(typed.statusCode)
				} else {
					traits.contractCode.add(typed.statusCode)
				}
			} else if typed.dispatched && typed.statusCode >= http.StatusMultipleChoices && typed.statusCode <= 599 {
				if isTransientHTTPStatus(typed.statusCode) {
					traits.transientStatus = true
					traits.transientCode.add(typed.statusCode)
				} else {
					traits.terminalStatus = true
					traits.terminalCode.add(typed.statusCode)
				}
			}
			if typed.safeReadTransient && typed.stage == safeResponseFailureAcceptedBodyRead {
				traits.transientAccepted = true
			}
			return false
		case *APIError:
			traits.dispatched = true
			if isTransientHTTPStatus(typed.StatusCode) {
				traits.transientStatus = true
				traits.transientCode.add(typed.StatusCode)
			} else if typed.StatusCode >= http.StatusMultipleChoices && typed.StatusCode <= 599 {
				traits.terminalStatus = true
				traits.terminalCode.add(typed.StatusCode)
			}
			return false
		case *safeRetryScheduledError:
			return true
		}

		switch node {
		case context.Canceled:
			traits.canceled = true
		case context.DeadlineExceeded:
			traits.deadline = true
		case http.ErrSchemeMismatch, http.ErrNotSupported, errors.ErrUnsupported:
			traits.terminal = true
		case io.EOF, io.ErrUnexpectedEOF,
			syscall.ECONNRESET, syscall.ECONNABORTED, syscall.ECONNREFUSED,
			syscall.EPIPE, syscall.ENETDOWN, syscall.ENETUNREACH, syscall.EHOSTUNREACH:
			traits.transientTransport = true
		}
		switch node.(type) {
		case x509.UnknownAuthorityError, *x509.UnknownAuthorityError,
			x509.HostnameError, *x509.HostnameError,
			x509.CertificateInvalidError, *x509.CertificateInvalidError,
			x509.SystemRootsError, *x509.SystemRootsError,
			x509.ConstraintViolationError, *x509.ConstraintViolationError,
			x509.InsecureAlgorithmError, *x509.InsecureAlgorithmError,
			x509.UnhandledCriticalExtension, *x509.UnhandledCriticalExtension,
			*tls.CertificateVerificationError,
			tls.RecordHeaderError, *tls.RecordHeaderError,
			tls.AlertError, *tls.AlertError,
			*http.ProtocolError,
			net.InvalidAddrError, *net.InvalidAddrError,
			net.UnknownNetworkError, *net.UnknownNetworkError,
			*net.ParseError,
			url.InvalidHostError, *url.InvalidHostError:
			traits.terminal = true
		}
		if netErr, ok := node.(net.Error); ok && (netErr.Timeout() || netErr.Temporary()) {
			traits.transientTransport = true
		}
		return true
	})

	classification := HTTPFailureClassification{
		Kind:              HTTPFailureContractOrLocal,
		RequestDispatched: traits.dispatched || traits.accepted,
		ResponseAccepted:  traits.accepted,
	}

	// Exact global precedence. The order of transient sub-kinds is fixed as
	// transport, accepted body, then status so a typed terminal status can never
	// turn a stronger joined failure into absence.
	switch {
	case traits.canceled:
		classification.Kind = HTTPFailureCanceled
	case traits.terminal:
		classification.Kind = HTTPFailureContractOrLocal
	case traits.deadline:
		classification.Kind = HTTPFailureDeadline
	case traits.transientTransport:
		classification.Kind = HTTPFailureTransientTransport
		classification.StatusCode = traits.transientCode.value()
		if classification.StatusCode == 0 {
			classification.StatusCode = traits.terminalCode.value()
		}
	case traits.transientAccepted:
		classification.Kind = HTTPFailureTransientAcceptedResponse
		classification.StatusCode = traits.acceptedCode.value()
	case traits.transientStatus:
		classification.Kind = HTTPFailureTransientResponse
		classification.StatusCode = traits.transientCode.value()
	case traits.terminalStatus:
		classification.Kind = HTTPFailureTerminalResponse
		classification.StatusCode = traits.terminalCode.value()
	default:
		classification.StatusCode = traits.contractCode.value()
	}

	// Status and schedule metadata remain available when a transient trait wins,
	// but are suppressed whenever cancellation, terminal transport/configuration,
	// or deadline controls the result. Exact-status predicates still require a
	// response kind, so a joined transient transport plus HTTP 404 is not absence.
	if classification.StatusCode != 0 && !traits.canceled && !traits.terminal && !traits.deadline {
		retryAfter, ok := safeRetryScheduleFromError(err)
		if ok {
			classification.RetryAfter = retryAfter.delay
			classification.HasRetryAfter = true
		}
	}
	return classification
}

func walkErrorTree(err error, visit func(error)) {
	walkErrorTreeControlled(err, func(node error) bool {
		visit(node)
		return true
	})
}

func walkErrorTreeControlled(err error, visit func(error) bool) {
	stack := []error{err}
	visited := make(map[error]struct{})
	for len(stack) > 0 {
		last := len(stack) - 1
		node := stack[last]
		stack = stack[:last]
		if node == nil {
			continue
		}
		if reflect.ValueOf(node).Comparable() {
			if _, ok := visited[node]; ok {
				continue
			}
			visited[node] = struct{}{}
		}
		if !visit(node) {
			continue
		}
		switch wrapped := node.(type) {
		case interface{ Unwrap() []error }:
			children := wrapped.Unwrap()
			for i := len(children) - 1; i >= 0; i-- {
				stack = append(stack, children[i])
			}
		case interface{ Unwrap() error }:
			stack = append(stack, wrapped.Unwrap())
		}
	}
}

func isTransientHTTPStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooManyRequests ||
		statusCode >= http.StatusInternalServerError && statusCode <= 599
}

const maxAcceptedRetryAfter = 5 * time.Minute

type safeRetryAfterSpec struct {
	delay    time.Duration
	deadline time.Time
}

// safeRetryScheduledError keeps an absolute HTTP-date out of the exported
// classification value and exported APIError. Its formatter deliberately
// renders only the wrapped provider error's already-sanitized message.
type safeRetryScheduledError struct {
	err      error
	schedule safeRetryAfterSpec
}

func (e *safeRetryScheduledError) Error() string { return e.err.Error() }
func (e *safeRetryScheduledError) Unwrap() error { return e.err }
func (e *safeRetryScheduledError) Format(state fmt.State, verb rune) {
	message := e.Error()
	switch verb {
	case 'q':
		_, _ = fmt.Fprintf(state, "%q", message)
	case 'x':
		_, _ = fmt.Fprintf(state, "%x", message)
	case 'X':
		_, _ = fmt.Fprintf(state, "%X", message)
	default:
		_, _ = io.WriteString(state, message)
	}
}

func withSafeRetrySchedule(err error, schedule safeRetryAfterSpec, ok bool) error {
	if err == nil || !ok {
		return err
	}
	return &safeRetryScheduledError{err: err, schedule: schedule}
}

func safeRetryScheduleFromError(err error) (safeRetryAfterSpec, bool) {
	var selected safeRetryAfterSpec
	found := false
	walkErrorTree(err, func(node error) {
		scheduled, ok := node.(*safeRetryScheduledError)
		if !ok {
			return
		}
		candidate := scheduled.schedule
		// Multiple schedules are not expected from one request, but joined errors
		// still need an order-independent conservative result.
		if !found || candidate.delay > selected.delay ||
			(candidate.delay == selected.delay && candidate.deadline.After(selected.deadline)) {
			selected = candidate
			found = true
		}
	})
	return selected, found
}

func safeRetryAfter(headers http.Header, now time.Time, maximum time.Duration) (time.Duration, bool) {
	spec, ok := safeRetryAfterWithDeadline(headers, now, maximum)
	return spec.delay, ok
}

func safeRetryAfterWithDeadline(headers http.Header, now time.Time, maximum time.Duration) (safeRetryAfterSpec, bool) {
	values := headers.Values("Retry-After")
	if len(values) != 1 {
		return safeRetryAfterSpec{}, false
	}
	return parseRetryAfterSpec(values[0], now, maximum)
}

// parseRetryAfter converts either delta-seconds or an HTTP-date to a bounded
// duration. It returns false for negative, malformed, overflowed, excessive,
// or ambiguous values and never returns or stores the original header text.
func parseRetryAfter(value string, now time.Time, maximum time.Duration) (time.Duration, bool) {
	spec, ok := parseRetryAfterSpec(value, now, maximum)
	return spec.delay, ok
}

func parseRetryAfterSpec(value string, now time.Time, maximum time.Duration) (safeRetryAfterSpec, bool) {
	if value == "" || value != strings.TrimSpace(value) || maximum < 0 {
		return safeRetryAfterSpec{}, false
	}
	if strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		seconds, err := strconv.ParseUint(value, 10, 64)
		if err != nil || seconds > uint64(maximum/time.Second) {
			return safeRetryAfterSpec{}, false
		}
		return safeRetryAfterSpec{delay: time.Duration(seconds) * time.Second}, true
	}

	when, err := http.ParseTime(value)
	if err != nil || when.Before(now) {
		return safeRetryAfterSpec{}, false
	}
	delay := when.Sub(now)
	if delay < 0 || delay > maximum {
		return safeRetryAfterSpec{}, false
	}
	return safeRetryAfterSpec{delay: delay, deadline: when}, true
}
