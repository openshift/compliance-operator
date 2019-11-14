package compliancescan

import (
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	complianceoperatorv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/complianceoperator/v1alpha1"
)

var _ = Describe("Testing compliancescan controller phases", func() {

	var (
		compliancescaninstance *complianceoperatorv1alpha1.ComplianceScan
		reconciler             ReconcileComplianceScan
		logger                 logr.Logger
	)

	BeforeEach(func() {
		logger = zapr.NewLogger(zap.NewNop())
		compliancescaninstance = &complianceoperatorv1alpha1.ComplianceScan{}

		objs := []runtime.Object{
			compliancescaninstance,
		}

		scheme := scheme.Scheme
		scheme.AddKnownTypes(complianceoperatorv1alpha1.SchemeGroupVersion, compliancescaninstance)

		client := fake.NewFakeClientWithScheme(scheme, objs...)
		reconciler = ReconcileComplianceScan{client: client, scheme: scheme}
	})

	Context("On the PENDING phase", func() {
		It("should update the compliancescan instance to phase LAUNCHING", func() {
			result, err := reconciler.phasePendingHandler(compliancescaninstance, logger)
			Expect(result).NotTo(BeNil())
			Expect(err).To(BeNil())
			Expect(compliancescaninstance.Status.Phase).To(Equal(complianceoperatorv1alpha1.PhaseLaunching))
		})
	})

	Context("On the LAUNCHING phase", func() {
		It("should update the compliancescan instance to phase RUNNING", func() {
			result, err := reconciler.phaseLaunchingHandler(compliancescaninstance, logger)
			Expect(result).ToNot(BeNil())
			Expect(err).To(BeNil())
			Expect(compliancescaninstance.Status.Phase).To(Equal(complianceoperatorv1alpha1.PhaseRunning))
		})
	})

	Context("On the RUNNING phase", func() {
		It("should update the compliancescan instance to phase DONE", func() {
			result, err := reconciler.phaseRunningHandler(compliancescaninstance, logger)
			Expect(result).ToNot(BeNil())
			Expect(err).To(BeNil())
			Expect(compliancescaninstance.Status.Phase).To(Equal(complianceoperatorv1alpha1.PhaseDone))
		})
	})

	Context("On the DONE phase", func() {
		It("Should merely return success", func() {
			result, err := reconciler.phaseDoneHandler(compliancescaninstance, logger)
			Expect(result).ToNot(BeNil())
			Expect(err).To(BeNil())
		})
	})
})
