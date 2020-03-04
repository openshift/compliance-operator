package compliancescan

import (
	"context"
	"fmt"

	"github.com/davecgh/go-spew/spew"

	"github.com/openshift/compliance-operator/pkg/controller/common"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		nodeinstance1          *corev1.Node
		nodeinstance2          *corev1.Node
	)

	BeforeEach(func() {
		logger = zapr.NewLogger(zap.NewNop())
		objs := []runtime.Object{}

		// test instance
		compliancescaninstance = &complianceoperatorv1alpha1.ComplianceScan{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
		}
		objs = append(objs, compliancescaninstance)

		// Nodes in the deployment
		nodeinstance1 = &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
			},
		}
		nodeinstance2 = &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-2",
			},
		}

		caSecret, _ := makeCASecret(compliancescaninstance, common.GetComplianceOperatorNamespace())
		spew.Dump(caSecret)
		serverSecret, _ := serverCertSecret(compliancescaninstance, caSecret.Data[corev1.TLSCertKey], caSecret.Data[corev1.TLSPrivateKeyKey], common.GetComplianceOperatorNamespace())
		spew.Dump(serverSecret)
		clientSecret, _ := clientCertSecret(compliancescaninstance, caSecret.Data[corev1.TLSCertKey], caSecret.Data[corev1.TLSPrivateKeyKey], common.GetComplianceOperatorNamespace())
		spew.Dump(clientSecret)

		objs = append(objs, nodeinstance1, nodeinstance2, caSecret, serverSecret, clientSecret)
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

			// We should have scheduled a pod per node
			nodes, _ := getTargetNodes(&reconciler, compliancescaninstance)
			var pods corev1.PodList
			reconciler.client.List(context.TODO(), &pods)
			Expect(len(pods.Items)).To(Equal(len(nodes.Items)))
		})
	})

	Context("On the RUNNING phase", func() {
		Context("With no pods in the cluster", func() {
			It("should update the compliancescan instance to phase LAUNCHING", func() {
				result, err := reconciler.phaseRunningHandler(compliancescaninstance, logger)
				Expect(result).ToNot(BeNil())
				Expect(err).To(BeNil())
				Expect(compliancescaninstance.Status.Phase).To(Equal(complianceoperatorv1alpha1.PhaseLaunching))
			})
		})

		Context("With two pods in the cluster", func() {
			BeforeEach(func() {
				// Create the pods for the test
				podName1 := fmt.Sprintf("%s-%s-pod", compliancescaninstance.Name, nodeinstance1.Name)
				reconciler.client.Create(context.TODO(), &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: podName1,
					},
				})

				podName2 := fmt.Sprintf("%s-%s-pod", compliancescaninstance.Name, nodeinstance2.Name)
				reconciler.client.Create(context.TODO(), &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: podName2,
					},
				})

				// Set the pod-node mappings in labels
				setPodForNodeName(compliancescaninstance, nodeinstance1.Name, podName1)
				setPodForNodeName(compliancescaninstance, nodeinstance2.Name, podName2)
				reconciler.client.Update(context.TODO(), compliancescaninstance)

				// Set state to RUNNING
				compliancescaninstance.Status.Phase = complianceoperatorv1alpha1.PhaseRunning
				reconciler.client.Status().Update(context.TODO(), compliancescaninstance)
			})

			It("should stay in RUNNING state", func() {
				result, err := reconciler.phaseRunningHandler(compliancescaninstance, logger)
				Expect(result).ToNot(BeNil())
				Expect(err).To(BeNil())
				Expect(compliancescaninstance.Status.Phase).To(Equal(complianceoperatorv1alpha1.PhaseRunning))
			})
		})

		Context("With two pods that succeeded in the cluster", func() {
			BeforeEach(func() {
				// Create the pods for the test
				podName1 := fmt.Sprintf("%s-%s-pod", compliancescaninstance.Name, nodeinstance1.Name)
				reconciler.client.Create(context.TODO(), &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: podName1,
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
					},
				})

				podName2 := fmt.Sprintf("%s-%s-pod", compliancescaninstance.Name, nodeinstance2.Name)
				reconciler.client.Create(context.TODO(), &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: podName2,
					},
					Status: corev1.PodStatus{
						Phase: corev1.PodSucceeded,
					},
				})

				// Set the pod-node mappings in labels
				setPodForNodeName(compliancescaninstance, nodeinstance1.Name, podName1)
				setPodForNodeName(compliancescaninstance, nodeinstance2.Name, podName2)
				reconciler.client.Update(context.TODO(), compliancescaninstance)

				// Set state to RUNNING
				compliancescaninstance.Status.Phase = complianceoperatorv1alpha1.PhaseRunning
				reconciler.client.Status().Update(context.TODO(), compliancescaninstance)
			})

			It("should move to AGGREGATING state", func() {
				result, err := reconciler.phaseRunningHandler(compliancescaninstance, logger)
				Expect(result).ToNot(BeNil())
				Expect(err).To(BeNil())
				Expect(compliancescaninstance.Status.Phase).To(Equal(complianceoperatorv1alpha1.PhaseAggregating))
			})
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
