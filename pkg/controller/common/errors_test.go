package common

import (
	"fmt"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("NonRetriable error wrapper", func() {
	var logger logr.Logger
	BeforeEach(func() {
		logger = zapr.NewLogger(zap.NewNop())
	})

	Context("With a non-wrapped error", func() {
		var err error
		BeforeEach(func() {
			err = fmt.Errorf("Some error")
		})

		It("Should be considered retriable", func() {
			Expect(IsRetriable(err)).To(BeTrue())
		})

		It("Should return the error", func() {
			_, retriableErr := ReturnWithRetriableError(logger, err)
			Expect(retriableErr).To(Not(BeNil()))
		})
	})

	Context("With a wrapped error", func() {
		var err error
		BeforeEach(func() {
			err = WrapNonRetriableCtrlError(fmt.Errorf("Some error"))
		})

		It("Should output the same wrapped error", func() {
			Expect(err.Error()).To(Equal("Some error"))
		})

		It("Should be not considered retriable", func() {
			Expect(IsRetriable(err)).To(BeFalse())
		})

		It("Should not return the error", func() {
			_, retriableErr := ReturnWithRetriableError(logger, err)
			Expect(retriableErr).To(BeNil())
		})
	})

	Context("With a custom error handler", func() {
		var err error
		var foo string
		BeforeEach(func() {
			foo = "old value"
			err = NewRetriableCtrlErrorWithCustomHandler(func() (reconcile.Result, error) {
				foo = "new value"
				return reconcile.Result{}, nil
			}, "Some error")
		})

		It("Should output the same wrapped error", func() {
			Expect(err.Error()).To(Equal("Some error"))
		})

		It("Should be considered retriable", func() {
			Expect(IsRetriable(err)).To(BeTrue())
		})

		It("Should not return the error", func() {
			_, retriableErr := ReturnWithRetriableError(logger, err)
			Expect(retriableErr).To(BeNil())
		})

		It("Should execute the custom handler", func() {
			ReturnWithRetriableError(logger, err)
			Expect(foo).To(Equal("new value"))
		})
	})
})
