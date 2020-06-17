package common

import (
	"fmt"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ErrorHandler defines a function that handles errors
// It can be used in a non retriable or a retriable error.
type ErrorHandler func() (reconcile.Result, error)

// NonRetriableCtrlError wraps errors with the addition of having
// the information for the error being non-retriable
type NonRetriableCtrlError struct {
	err           error
	canRetry      bool
	customHandler ErrorHandler
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

// HasCustomHandler checks whether an error has a custom handler
func (cerr NonRetriableCtrlError) HasCustomHandler() bool {
	return cerr.customHandler != nil
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

// NewRetriableCtrlErrorWithCustomHandler creates an error with the RetriableCtrlError interface
// This error is retriable has a custom handler
func NewRetriableCtrlErrorWithCustomHandler(customHandler ErrorHandler, errorFmt string, args ...interface{}) *NonRetriableCtrlError {
	return &NonRetriableCtrlError{
		canRetry:      true,
		customHandler: customHandler,
		err:           fmt.Errorf(errorFmt, args...),
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

// HasCustomHandler returns whether the error has a custom handler
// or not
func HasCustomHandler(err error) bool {
	ccErr, ok := err.(*NonRetriableCtrlError)
	if ok {
		return ccErr.HasCustomHandler()
	}
	return false
}

// CallCustomHandler calls the custom handler for an error if it has one
func CallCustomHandler(err error) (reconcile.Result, error) {
	ccErr, ok := err.(*NonRetriableCtrlError)
	if ok {
		return ccErr.customHandler()
	}
	return reconcile.Result{}, nil
}

// ReturnWithRetriableError will check if the error is retriable we return it.
// If it's not retriable, we return nil so the reconciler doesn't keep looping.
func ReturnWithRetriableError(log logr.Logger, err error) (reconcile.Result, error) {
	if IsRetriable(err) {
		if HasCustomHandler(err) {
			return CallCustomHandler(err)
		}
		log.Error(err, "Retriable error")
		return reconcile.Result{}, err
	}
	if HasCustomHandler(err) {
		return CallCustomHandler(err)
	}
	log.Error(err, "Non-retriable error")
	return reconcile.Result{}, nil
}
