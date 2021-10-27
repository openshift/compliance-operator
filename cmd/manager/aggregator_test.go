package main

import (
	"context"
	"fmt"

	backoff "github.com/cenkalti/backoff/v4"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	ocpcfgv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	fakerec "k8s.io/client-go/tools/record"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

type aggregatorCrClientFake struct {
	client      runtimeclient.Client
	scheme      *runtime.Scheme
	recorder    record.EventRecorder
	fakevgetter *fakeversionget
}

func (accrif *aggregatorCrClientFake) getClient() runtimeclient.Client {
	return accrif.client
}

func (accrif *aggregatorCrClientFake) getScheme() *runtime.Scheme {
	return accrif.scheme
}

func (accrif *aggregatorCrClientFake) getRecorder() record.EventRecorder {
	return accrif.recorder
}

func (accrif *aggregatorCrClientFake) getClientset() *kubernetes.Clientset {
	return nil
}

func (accrif *aggregatorCrClientFake) versionGetter() discovery.ServerVersionInterface {
	return accrif.fakevgetter
}

func (accrif *aggregatorCrClientFake) useEventRecorder(source string, config *rest.Config) error {
	return nil
}

type fakeversionget struct {
	version  string
	throwErr bool
}

func (vg *fakeversionget) setVersion(version string) {
	vg.version = version
}

func (vg *fakeversionget) setThrowErr(throw bool) {
	vg.throwErr = throw
}

func (vg *fakeversionget) ServerVersion() (*version.Info, error) {
	if !vg.throwErr {
		return &version.Info{GitVersion: vg.version}, nil
	}
	return nil, backoff.Permanent(fmt.Errorf("Some error"))
}

var _ = Describe("Aggregator Tests", func() {
	Context("Empty Remediations", func() {
		It("Creates an empty remediation", func() {
			var (
				namespace          = "test-ns"
				remediationName    = "testRem"
				targetNodeSelector = map[string]string{
					"hops": "malt",
				}
			)
			nodeScanSettings := compv1alpha1.ComplianceScanSpec{
				ScanType:     compv1alpha1.ScanTypeNode,
				NodeSelector: targetNodeSelector,
			}

			nodeScan := &compv1alpha1.ComplianceScan{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testScanNode",
					Namespace: namespace,
				},
				Spec: nodeScanSettings,
			}
			emptyRem := &compv1alpha1.ComplianceRemediation{
				ObjectMeta: metav1.ObjectMeta{
					Name:      remediationName,
					Namespace: namespace,
				},
				Spec: compv1alpha1.ComplianceRemediationSpec{
					ComplianceRemediationSpecMeta: compv1alpha1.ComplianceRemediationSpecMeta{
						Apply: true,
					},
					Current: compv1alpha1.ComplianceRemediationPayload{
						Object: &unstructured.Unstructured{
							Object: map[string]interface{}{},
						},
					},
				},
			}
			canCreate, _ := canCreateRemediationObject(nodeScan, emptyRem.Spec.Current.Object)
			Expect(canCreate).To(BeFalse())
		})
	})

	Context("Version filtering Remediations", func() {
		var scan *compv1alpha1.ComplianceScan
		var rem *compv1alpha1.ComplianceRemediation
		var crClient *aggregatorCrClientFake
		var fakerecorder *fakerec.FakeRecorder
		var ctx context.Context

		BeforeEach(func() {
			ctx = context.Background()
			scheme := getScheme()

			scan = &compv1alpha1.ComplianceScan{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
			}
			rem = &compv1alpha1.ComplianceRemediation{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "foo",
					Namespace:   "bar",
					Annotations: map[string]string{},
				},
			}

			client := fake.NewFakeClientWithScheme(scheme, scan, rem)
			fakerecorder = fakerec.NewFakeRecorder(1)
			crClient = &aggregatorCrClientFake{
				scheme:      scheme,
				client:      client,
				recorder:    fakerecorder,
				fakevgetter: &fakeversionget{},
			}
		})

		When("Using the openshift-specific annotation", func() {
			var clusterOp *ocpcfgv1.ClusterOperator

			BeforeEach(func() {
				clusterOp = &ocpcfgv1.ClusterOperator{
					ObjectMeta: metav1.ObjectMeta{
						Name: "openshift-apiserver",
					},
				}
				createErr := crClient.client.Create(ctx, clusterOp)
				Expect(createErr).To(BeNil())
			})

			It("Does not skip the remediation if it's applicable", func() {
				clusterOp.Status.Versions = append(clusterOp.Status.Versions,
					ocpcfgv1.OperandVersion{
						Name:    apiserverOperatorName,
						Version: "4.10.0-0.nightly-2021-10-16-173656",
					},
				)
				updateErr := crClient.client.Status().Update(ctx, clusterOp)
				Expect(updateErr).To(BeNil())

				rem.Annotations[compv1alpha1.OCPVersionDependencyAnnotation] = ">4.9.0"
				skip, why := shouldSkipRemediation(scan, rem, crClient)
				Expect(skip).To(BeFalse())
				Expect(why).To(BeEmpty())
				Expect(fakerecorder.Events).To(HaveLen(0))
			})

			It("Does not skip the remediation if it's applicable and URL-encoded", func() {
				clusterOp.Status.Versions = append(clusterOp.Status.Versions,
					ocpcfgv1.OperandVersion{
						Name:    apiserverOperatorName,
						Version: "4.10.0-0.nightly-2021-10-16-173656",
					},
				)
				updateErr := crClient.client.Status().Update(ctx, clusterOp)
				Expect(updateErr).To(BeNil())

				rem.Annotations[compv1alpha1.OCPVersionDependencyAnnotation] = "&gt;=4.9.0"
				skip, why := shouldSkipRemediation(scan, rem, crClient)
				Expect(why).To(BeEmpty())
				Expect(skip).To(BeFalse())
				Expect(fakerecorder.Events).To(HaveLen(0))
			})

			It("Skips the remediation if it's not applicable", func() {
				clusterOp.Status.Versions = append(clusterOp.Status.Versions,
					ocpcfgv1.OperandVersion{
						Name:    apiserverOperatorName,
						Version: "4.10.16",
					},
				)
				updateErr := crClient.client.Status().Update(ctx, clusterOp)
				Expect(updateErr).To(BeNil())

				rem.Annotations[compv1alpha1.OCPVersionDependencyAnnotation] = ">4.10.17"
				skip, why := shouldSkipRemediation(scan, rem, crClient)
				Expect(skip).To(BeTrue())
				Expect(why).To(ContainSubstring("Cluster doesn't match version range"))
				Expect(fakerecorder.Events).To(HaveLen(1))
			})

			It("Skips the remediation if it's unable to get the needed obejct", func() {
				deleteErr := crClient.client.Delete(ctx, clusterOp)
				Expect(deleteErr).To(BeNil())

				rem.Annotations[compv1alpha1.OCPVersionDependencyAnnotation] = ">4.10.17"
				skip, why := shouldSkipRemediation(scan, rem, crClient)
				Expect(skip).To(BeTrue())
				Expect(why).To(ContainSubstring("Unable to get OCP Version"))
				Expect(fakerecorder.Events).To(HaveLen(1))
			})

			It("Skips the remediation if expected version field isn't there", func() {
				rem.Annotations[compv1alpha1.OCPVersionDependencyAnnotation] = ">4.10.17"
				skip, why := shouldSkipRemediation(scan, rem, crClient)
				Expect(skip).To(BeTrue())
				Expect(why).To(ContainSubstring("Unable to find OCP Version"))
				Expect(fakerecorder.Events).To(HaveLen(1))
			})

			It("Skips the remediation if cluster operator OCP version is malformed", func() {
				clusterOp.Status.Versions = append(clusterOp.Status.Versions,
					ocpcfgv1.OperandVersion{
						Name:    apiserverOperatorName,
						Version: "4.10-foo",
					},
				)
				updateErr := crClient.client.Status().Update(ctx, clusterOp)
				Expect(updateErr).To(BeNil())

				rem.Annotations[compv1alpha1.OCPVersionDependencyAnnotation] = ">4.10.17"
				skip, why := shouldSkipRemediation(scan, rem, crClient)
				Expect(skip).To(BeTrue())
				Expect(why).To(ContainSubstring("Unable to parse version"))
				Expect(fakerecorder.Events).To(HaveLen(1))
			})
		})

		When("Using the kubernetes-specific annotation", func() {
			It("Does not skip the remediation if it's applicable", func() {
				crClient.fakevgetter.setVersion("v1.22.1+9312243")
				rem.Annotations[compv1alpha1.K8SVersionDependencyAnnotation] = ">1.22.0"
				skip, why := shouldSkipRemediation(scan, rem, crClient)
				Expect(why).To(BeEmpty())
				Expect(skip).To(BeFalse())
				Expect(fakerecorder.Events).To(HaveLen(0))
			})

			It("Skips the remediation if it's not applicable", func() {
				crClient.fakevgetter.setVersion("1.22.1")
				rem.Annotations[compv1alpha1.K8SVersionDependencyAnnotation] = "<1.21.0"
				skip, why := shouldSkipRemediation(scan, rem, crClient)
				Expect(skip).To(BeTrue())
				Expect(why).To(ContainSubstring("Cluster doesn't match version range"))
				Expect(fakerecorder.Events).To(HaveLen(1))
			})

			It("Skips the remediation if there's an error getting the version", func() {
				crClient.fakevgetter.setThrowErr(true)
				rem.Annotations[compv1alpha1.K8SVersionDependencyAnnotation] = ">1.22.1"
				skip, why := shouldSkipRemediation(scan, rem, crClient)
				Expect(skip).To(BeTrue())
				Expect(why).To(ContainSubstring("Some error"))
				Expect(fakerecorder.Events).To(HaveLen(1))
			})

			It("Skips the remediation if kubelet version is malformed", func() {
				crClient.fakevgetter.setVersion("1.22.1azxdf")
				rem.Annotations[compv1alpha1.K8SVersionDependencyAnnotation] = ">4.10.17"
				skip, why := shouldSkipRemediation(scan, rem, crClient)
				Expect(skip).To(BeTrue())
				Expect(why).To(ContainSubstring("Unable to parse version"))
				Expect(fakerecorder.Events).To(HaveLen(1))
			})
		})
	})
})
