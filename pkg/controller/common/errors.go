package common

import (
	"fmt"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// NonRetriableCtrlError wraps errors with the addition of having
// the information for the error being non-retriable
type NonRetriableCtrlError struct {
	err      error
	canRetry bool
}

func (cerr NonRetriableCtrlError) Error() string {
	return cerr.err.Error()
}

// blank assignment to verify that RetriableCtrlError implements error
var _ error = &NonRetriableCtrlError{}

// IsRetriable exposes if the error is retriable or not
func (cerr NonRetriableCtrlError) IsRetriable() bool {
	return cerr.canRetry
}

// WrapNonRetriableCtrlError wraps an error with the RetriableCtrlError interface
func WrapNonRetriableCtrlError(err error) *NonRetriableCtrlError {
	return &NonRetriableCtrlError{
		canRetry: false,
		err:      err,
	}
}

// NewNonRetriableCtrlError creates an error with the RetriableCtrlError interface
func NewNonRetriableCtrlError(errorFmt string, args ...interface{}) *NonRetriableCtrlError {
	return &NonRetriableCtrlError{
		canRetry: false,
		err:      fmt.Errorf(errorFmt, args...),
	}
}

// IsRetriable returns whether the error is retriable or not using the
// NonRetriableCtrlError interface
func IsRetriable(err error) bool {
	ccErr, ok := err.(*NonRetriableCtrlError)
	if ok {
		return ccErr.IsRetriable()
	}
	return true
}

// ReturnWithRetriableError will check if the error is retriable we return it.
// If it's not retriable, we return nil so the reconciler doesn't keep looping.
func ReturnWithRetriableError(log logr.Logger, err error) (reconcile.Result, error) {
	if IsRetriable(err) {
		log.Error(err, "Retriable error")
		return reconcile.Result{}, err
	}
	log.Error(err, "Non-retriable error")
	return reconcile.Result{}, nil
}
