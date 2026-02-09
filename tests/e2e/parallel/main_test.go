package framework

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	ocpapi "github.com/openshift/api"
	configv1 "github.com/openshift/api/config/v1"
	imagev1 "github.com/openshift/api/image/v1"
	mcfgapi "github.com/openshift/api/machineconfiguration"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	batchv1 "k8s.io/api/batch/v1"
	core "k8s.io/api/core/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/rbac/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	clusterv1alpha1 "open-cluster-management.io/api/cluster/v1alpha1"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	psapi "k8s.io/pod-security-admission/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	dynclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/ComplianceAsCode/compliance-operator/pkg/apis"
	compv1alpha1 "github.com/ComplianceAsCode/compliance-operator/pkg/apis/compliance/v1alpha1"
	compscanctrl "github.com/ComplianceAsCode/compliance-operator/pkg/controller/compliancescan"
	compsuitectrl "github.com/ComplianceAsCode/compliance-operator/pkg/controller/compliancesuite"
	"github.com/ComplianceAsCode/compliance-operator/pkg/utils"
)

var defaultBackoff = backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries)

// readFile accepts a file path and returns the file contents.
func (f *Framework) readFile(p *string) ([]byte, error) {
	y, err := os.ReadFile(*p)
	if err != nil {
		log.Printf("unable to read contents of %s: %s", *p, err)
		return nil, err
	}
	return y, nil
}

type ObjectResouceVersioner interface {
	dynclient.Object
	metav1.Common
}

// readYAML accepts a byte string that is YAML-like and attempts to read
// it into a slice of byte strings where each element in the slice is a
// separate YAML document delimited by "---". This is useful for working
// with files that contain multiple YAML documents.
func (f *Framework) readYAML(y []byte) ([][]byte, error) {
	o := make([][]byte, 0)

	s := NewYAMLScanner(bytes.NewBuffer(y))
	for s.Scan() {
		// Grab the current YAML document
		d := s.Bytes()

		// Convert to JSON and attempt to decode it
		obj := &unstructured.Unstructured{}
		j, err := yaml.YAMLToJSON(d)
		if err != nil {
			return nil, fmt.Errorf("could not convert yaml document to json: %w", err)
		}
		if err := obj.UnmarshalJSON(j); err != nil {
			return nil, fmt.Errorf("failed to decode object spec: %w", err)
		}
		o = append(o, j)
	}
	return o, nil
}

func unmarshalJSON(j []byte) (dynclient.Object, error) {
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(j); err != nil {
		return nil, fmt.Errorf("failed to unmarshal object spec: %w", err)
	}
	return obj, nil
}

func (f *Framework) cleanUpFromYAMLFile(p *string) error {
	c, err := f.readFile(p)
	if err != nil {
		return err
	}
	documents, err := f.readYAML(c)
	if err != nil {
		return err
	}

	for _, d := range documents {
		obj, err := unmarshalJSON(d)
		if err != nil {
			return err
		}
		obj.SetNamespace(f.OperatorNamespace)
		log.Printf("deleting %s %s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName())
		if err := f.Client.Delete(context.TODO(), obj); err != nil {
			return fmt.Errorf("failed to delete %s: %w", obj.GetObjectKind().GroupVersionKind().Kind, err)
		}
	}
	return nil
}

func (f *Framework) PrintROSADebugInfo(t *testing.T) {
	// List cluster claims
	clusterClaimList := clusterv1alpha1.ClusterClaimList{}
	err := f.Client.List(context.TODO(), &clusterClaimList)
	if err != nil {
		t.Fatalf("Failed to list cluster claims: %v", err)
	}
	for _, clusterClaim := range clusterClaimList.Items {
		t.Logf("ClusterClaim: %s", clusterClaim.Name)
		t.Logf("ClusterClaim.Spec.Value: %v", clusterClaim.Spec.Value)

	}

	// List infrastructures
	infraList := configv1.InfrastructureList{}
	err = f.Client.List(context.TODO(), &infraList)
	if err != nil {
		t.Fatalf("Failed to list infrastructures: %v", err)
	}
	for _, infra := range infraList.Items {
		t.Logf("Infrastructure: %s", infra.Name)
		t.Logf("Infrastructure.Status.ControlPlaneTopology: %v", infra.Status.ControlPlaneTopology)
	}

	// print out logs for compliance-operator deployment
	podList := corev1.PodList{}
	err = f.Client.List(context.TODO(), &podList, client.InNamespace(f.OperatorNamespace))
	if err != nil {
		t.Fatalf("Failed to list pods: %v", err)
	}
	for _, pod := range podList.Items {
		// find pod named contains compliance-operator substring
		if strings.Contains(pod.Name, "compliance-operator") {
			log.Printf("Pod: %s", pod.Name)
			log.Printf("Pod.Status: %v", pod.Status)
			// print out logs for compliance-operator pod
			req := f.KubeClient.CoreV1().Pods(f.OperatorNamespace).GetLogs(pod.Name, &corev1.PodLogOptions{})
			log.Printf("Request: %v", req)
			reader, err := req.Stream(context.Background())
			if err != nil {
				t.Fatalf("Failed to get logs: %v", err)
			}
			buf := make([]byte, 1024)
			for {
				n, err := reader.Read(buf)
				if err != nil {
					break
				}
				log.Printf("Logs: %s", string(buf[:n]))
			}
		}
	}
}

func (f *Framework) CreateProfileBundle(pbName string, baselineImage string, contentFile string) (*compv1alpha1.ProfileBundle, error) {
	origPb := &compv1alpha1.ProfileBundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pbName,
			Namespace: f.OperatorNamespace,
		},
		Spec: compv1alpha1.ProfileBundleSpec{
			ContentImage: baselineImage,
			ContentFile:  contentFile,
		},
	}
	// Pass nil in as the cleanupOptions since so we don't invoke all the
	// cleanup function code in Create. Use defer to cleanup the
	// ProfileBundle at the end of the test, instead of at the end of the
	// suite.
	log.Printf("Creating ProfileBundle %s", pbName)
	if err := f.Client.Create(context.TODO(), origPb, nil); err != nil {
		return nil, err
	}
	return origPb, nil
}

func (f *Framework) cleanUpProfileBundle(p string) error {
	pb := &compv1alpha1.ProfileBundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p,
			Namespace: f.OperatorNamespace,
		},
	}
	err := f.Client.Delete(context.TODO(), pb)
	if err != nil {
		return fmt.Errorf("failed to delete ProfileBundle%s: %w", p, err)
	}
	return nil
}

func (f *Framework) createFromYAMLFile(p *string) error {
	c, err := f.readFile(p)
	if err != nil {
		return err
	}
	return f.createFromYAMLString(string(c))
}

func (f *Framework) createFromYAMLString(y string) error {
	c := []byte(y)
	documents, err := f.readYAML(c)
	if err != nil {
		return err
	}

	for _, d := range documents {
		obj, err := unmarshalJSON(d)
		if err != nil {
			return err
		}

		obj.SetNamespace(f.OperatorNamespace)
		log.Printf("creating %s %s", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName())
		err = f.Client.CreateWithoutCleanup(context.TODO(), obj)
		if err != nil {
			return err
		}
	}
	return nil
}

func (f *Framework) waitForScanCleanup() error {
	timeouterr := wait.Poll(time.Second*5, time.Minute*2, func() (bool, error) {
		var scans compv1alpha1.ComplianceScanList
		f.Client.List(context.TODO(), &scans, &dynclient.ListOptions{})
		if len(scans.Items) == 0 {
			return true, nil
		}
		log.Printf("%d scans not cleaned up\n", len(scans.Items))
		for _, i := range scans.Items {
			log.Printf("scan %s still exists in namespace %s", i.Name, i.Namespace)
		}
		return false, nil
	})

	if timeouterr != nil {
		return fmt.Errorf("timed out waiting for scans to cleanup: %w", timeouterr)

	}
	return nil
}

func (f *Framework) addFrameworks() error {
	// compliance-operator objects
	coObjs := [3]dynclient.ObjectList{&compv1alpha1.ComplianceScanList{},
		&compv1alpha1.ComplianceRemediationList{},
		&compv1alpha1.ComplianceSuiteList{},
	}

	for _, obj := range coObjs {
		err := AddToFrameworkScheme(apis.AddToScheme, obj)
		if err != nil {
			return fmt.Errorf("failed to add custom resource scheme to framework: %v", err)
		}
	}

	// Additional testing objects
	testObjs := [1]dynclient.ObjectList{
		&configv1.OAuthList{},
	}
	for _, obj := range testObjs {
		err := AddToFrameworkScheme(configv1.Install, obj)
		if err != nil {
			return fmt.Errorf("failed to add configv1 resource scheme to framework: %v", err)
		}
	}

	// MCO objects
	if f.Platform != "rosa" {
		mcoObjs := [2]dynclient.ObjectList{
			&mcfgv1.MachineConfigPoolList{},
			&mcfgv1.MachineConfigList{},
		}
		for _, obj := range mcoObjs {
			err := AddToFrameworkScheme(mcfgapi.Install, obj)
			if err != nil {
				return fmt.Errorf("failed to add custom resource scheme to framework: %v", err)
			}
		}
	}

	// ClusterClaim objects
	if f.Platform == "rosa" {
		ccObjs := [1]dynclient.ObjectList{
			&clusterv1alpha1.ClusterClaimList{},
		}
		for _, obj := range ccObjs {
			err := AddToFrameworkScheme(clusterv1alpha1.Install, obj)
			if err != nil {
				return fmt.Errorf("failed to add custom resource scheme to framework: %v", err)
			}
		}
	}

	// OpenShift objects
	ocpObjs := [2]dynclient.ObjectList{
		&imagev1.ImageStreamList{},
		&imagev1.ImageStreamTagList{},
	}
	for _, obj := range ocpObjs {
		if err := AddToFrameworkScheme(ocpapi.Install, obj); err != nil {
			return fmt.Errorf("failed to add custom resource scheme to framework: %v", err)
		}
	}

	//Schedule objects
	scObjs := [1]dynclient.ObjectList{
		&schedulingv1.PriorityClassList{},
	}
	for _, obj := range scObjs {
		if err := AddToFrameworkScheme(schedulingv1.AddToScheme, obj); err != nil {
			return fmt.Errorf("TEST SETUP: failed to add custom resource scheme to framework: %v", err)
		}
	}

	return nil
}

func (f *Framework) initializeMetricsTestResources() error {
	if _, err := f.KubeClient.RbacV1().ClusterRoles().Create(context.TODO(), &v1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "co-metrics-client",
		},
		Rules: []v1.PolicyRule{
			{
				NonResourceURLs: []string{
					"/metrics-co",
				},
				Verbs: []string{
					"get",
				},
			},
		},
	}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	if _, err := f.KubeClient.RbacV1().ClusterRoleBindings().Create(context.TODO(), &v1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "co-metrics-client",
		},
		Subjects: []v1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "default",
				Namespace: f.OperatorNamespace,
			},
		},
		RoleRef: v1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "co-metrics-client",
		},
	}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	if _, err := f.KubeClient.CoreV1().Secrets(f.OperatorNamespace).Create(context.TODO(), &core.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metrics-token",
			Namespace: f.OperatorNamespace,
			Annotations: map[string]string{
				"kubernetes.io/service-account.name": "default",
			},
		},
		Type: "kubernetes.io/service-account-token",
	}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (f *Framework) replaceNamespaceFromManifest() error {
	log.Printf("updating manifest %s with namespace %s\n", *f.NamespacedManPath, f.OperatorNamespace)
	if f.NamespacedManPath == nil {
		return fmt.Errorf("no namespaced manifest given as test argument (operator-sdk might have changed)")
	}
	c, err := f.readFile(f.NamespacedManPath)
	if err != nil {
		return err
	}

	newContents := strings.Replace(string(c), "openshift-compliance", f.OperatorNamespace, -1)

	// #nosec
	err = os.WriteFile(*f.NamespacedManPath, []byte(newContents), 0644)
	if err != nil {
		return fmt.Errorf("error writing namespaced manifest file: %s", err)
	}
	return nil
}

func (f *Framework) WaitForDeployment(name string, replicas int, retryInterval, timeout time.Duration) error {
	err := wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		deployment, err := f.KubeClient.AppsV1().Deployments(f.OperatorNamespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.Printf("Waiting for availability of Deployment: %s in Namespace: %s \n", name, f.OperatorNamespace)
				return false, nil
			}
			return false, err
		}

		if int(deployment.Status.AvailableReplicas) >= replicas {
			return true, nil
		}
		log.Printf("Waiting for full availability of %s deployment (%d/%d)\n", name,
			deployment.Status.AvailableReplicas, replicas)
		return false, nil
	})
	if err != nil {
		return err
	}
	log.Printf("Deployment available (%d/%d)\n", replicas, replicas)
	return nil
}

func (f *Framework) ensureTestNamespaceExists() error {
	// create namespace only if it doesn't already exist
	_, err := f.KubeClient.CoreV1().Namespaces().Get(context.TODO(), f.OperatorNamespace, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		ns := &core.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: f.OperatorNamespace,
				Labels: map[string]string{
					psapi.EnforceLevelLabel:                          string(psapi.LevelPrivileged),
					"security.openshift.io/scc.podSecurityLabelSync": "false",
					"openshift.io/cluster-monitoring":                "true",
				},
			},
		}

		log.Printf("creating namespace %s", f.OperatorNamespace)
		_, err = f.KubeClient.CoreV1().Namespaces().Create(context.TODO(), ns, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("namespace %s already exists: %w", f.OperatorNamespace, err)
		} else if err != nil {
			return err
		}
		return nil
	} else if apierrors.IsAlreadyExists(err) {
		log.Printf("using existing namespace %s", f.OperatorNamespace)
		return nil
	} else {
		return nil
	}

}

func (f *Framework) WaitForProfileDeprecatedWarning(t *testing.T, scanName string, profileName string) error {
	polledScan := &compv1alpha1.ComplianceScan{}

	// Wait for profile deprecation warning event
	err := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		getErr := f.Client.Get(context.TODO(), types.NamespacedName{Name: scanName, Namespace: f.OperatorNamespace}, polledScan)
		if getErr != nil {
			t.Log(getErr)
			return false, nil
		}

		profileEventList, getEventErr := f.KubeClient.CoreV1().Events(f.OperatorNamespace).List(context.TODO(), metav1.ListOptions{
			FieldSelector: "reason=DeprecatedProfile",
		})
		if getEventErr != nil {
			t.Log(getEventErr)
			return false, nil
		}

		tailoredProfileEventList, getEventErr := f.KubeClient.CoreV1().Events(f.OperatorNamespace).List(context.TODO(), metav1.ListOptions{
			FieldSelector: "reason=DeprecatedTailoredProfile",
		})
		if getEventErr != nil {
			t.Log(getEventErr)
			return false, nil
		}

		re := regexp.MustCompile(fmt.Sprintf(".*%s.*", profileName))
		for _, item := range profileEventList.Items {
			if item.InvolvedObject.Name == polledScan.Name && re.MatchString(item.Message) {
				t.Logf("Found ComplianceScan deprecated profile event: %s", item.Message)
				return true, nil
			}
		}
		for _, item := range tailoredProfileEventList.Items {
			if item.InvolvedObject.Name == polledScan.Name && re.MatchString(item.Message) {
				t.Logf("Found ComplianceScan deprecated profile event: %s", item.Message)
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("No ComplianceScan event for profile \"%s\" found", profileName)
		return err
	}
	return nil
}

func (f *Framework) WaitForDuplicatedVariableWarning(t *testing.T, tpName string, variableName string) error {
	polledTailoredProfile := &compv1alpha1.TailoredProfile{}

	// Wait for profile deprecation warning event
	err := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		getErr := f.Client.Get(context.TODO(), types.NamespacedName{Name: tpName, Namespace: f.OperatorNamespace}, polledTailoredProfile)
		if getErr != nil {
			t.Log(getErr)
			return false, nil
		}

		duplicateValuesEventList, getEventErr := f.KubeClient.CoreV1().Events(f.OperatorNamespace).List(context.TODO(), metav1.ListOptions{
			FieldSelector: "reason=DuplicatedSetValue",
		})
		if getEventErr != nil {
			t.Log(getEventErr)
			return false, nil
		}

		re := regexp.MustCompile(fmt.Sprintf(".*%s.*", variableName))

		for _, item := range duplicateValuesEventList.Items {
			if item.InvolvedObject.Name == polledTailoredProfile.Name && re.MatchString(item.Message) {
				t.Logf("Found TailoredProfile duplicated variable event: %s", item.Message)
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("No TailoredProfile event for variable \"%s\" found", variableName)
		return err
	}
	return nil
}

// waitForProfileBundleStatus will poll until the compliancescan that we're
// lookingfor reaches a certain status, or until a timeout is reached.
func (f *Framework) WaitForProfileBundleStatus(name string, status compv1alpha1.DataStreamStatusType) error {
	pb := &compv1alpha1.ProfileBundle{}
	var lastErr error
	// retry and ignore errors until timeout
	timeouterr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		lastErr = f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: f.OperatorNamespace}, pb)
		if lastErr != nil {
			if apierrors.IsNotFound(lastErr) {
				log.Printf("waiting for availability of %s ProfileBundle\n", name)
				return false, nil
			}
			log.Printf("retrying due to error: %s\n", lastErr)
			return false, nil
		}

		if pb.Status.DataStreamStatus == status {
			return true, nil
		}
		log.Printf("waiting ProfileBundle %s to become %s (%s)\n", name, status, pb.Status.DataStreamStatus)
		return false, nil
	})
	if timeouterr != nil {
		return fmt.Errorf("ProfileBundle %s failed to reach state %s", name, status)
	}
	log.Printf("ProfileBundle ready (%s)\n", pb.Status.DataStreamStatus)
	return nil
}

func (f *Framework) GetReadyProfileBundle(name, namespace string) (*compv1alpha1.ProfileBundle, error) {
	if err := f.WaitForProfileBundleStatus(name, compv1alpha1.DataStreamValid); err != nil {
		return nil, err
	}

	pb := &compv1alpha1.ProfileBundle{}
	if err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, pb); err != nil {
		return nil, err
	}

	return pb, nil
}

func (f *Framework) updateScanSettingsForDebug() error {
	if f.Platform == "rosa" {
		fmt.Printf("bypassing ScanSettings test setup because it's not supported on %s\n", f.Platform)
		return nil
	}
	for _, ssName := range []string{"default", "default-auto-apply"} {
		ss := &compv1alpha1.ScanSetting{}
		sskey := types.NamespacedName{Name: ssName, Namespace: f.OperatorNamespace}
		if err := f.Client.Get(context.TODO(), sskey, ss); err != nil {
			return err
		}

		ssCopy := ss.DeepCopy()
		ssCopy.Debug = true

		if err := f.Client.Update(context.TODO(), ssCopy); err != nil {
			return err
		}
	}
	return nil
}

func (f *Framework) ensureE2EScanSettings() error {
	if f.Platform == "rosa" {
		fmt.Printf("bypassing ScanSettings test setup because it's not supported on %s\n", f.Platform)
		return nil
	}
	for _, ssName := range []string{"default", "default-auto-apply"} {
		ss := &compv1alpha1.ScanSetting{}
		sskey := types.NamespacedName{Name: ssName, Namespace: f.OperatorNamespace}
		if err := f.Client.Get(context.TODO(), sskey, ss); err != nil {
			return err
		}

		ssCopy := ss.DeepCopy()
		ssCopy.ObjectMeta = metav1.ObjectMeta{
			Name:      "e2e-" + ssName,
			Namespace: f.OperatorNamespace,
		}
		ssCopy.Roles = []string{
			TestPoolName,
		}
		ssCopy.Debug = true

		if err := f.Client.Create(context.TODO(), ssCopy, nil); err != nil {
			return err
		}
	}

	return nil
}

func (f *Framework) deleteScanSettings(name string) error {
	if f.Platform == "rosa" {
		fmt.Printf("bypassing ScanSettings test setup because it's not supported on %s\n", f.Platform)
		return nil
	}
	ss := &compv1alpha1.ScanSetting{}
	sskey := types.NamespacedName{Name: name, Namespace: f.OperatorNamespace}
	if err := f.Client.Get(context.TODO(), sskey, ss); err != nil {
		return err
	}

	err := f.Client.Delete(context.TODO(), ss)
	if err != nil {
		return fmt.Errorf("failed to cleanup scan setting %s: %w", name, err)
	}
	return nil
}

func (f *Framework) createMachineConfigPool(n string) error {
	if f.Platform == "rosa" {
		fmt.Printf("bypassing MachineConfigPool test setup because it's not supported on %s\n", f.Platform)
		return nil
	}
	// get the worker pool
	w := "worker"
	p := &mcfgv1.MachineConfigPool{}
	getErr := backoff.RetryNotify(
		func() error {
			err := f.Client.Get(context.TODO(), types.NamespacedName{Name: w}, p)
			if apierrors.IsNotFound(err) {
				// Can't recover from this
				log.Printf("Could not find the %s Machine Config Pool to modify: %s", w, err)
			}
			// might be a transcient error
			return err
		},
		defaultBackoff,
		func(err error, interval time.Duration) {
			log.Printf("error while getting MachineConfig pool to create sub-pool from: %s. Retrying after %s", err, interval)
		})
	if getErr != nil {
		return fmt.Errorf("failed to get Machine Config Pool %s to create sub-pool from: %w", w, getErr)
	}

	nodeList, err := f.getNodesForPool(p)
	if err != nil {
		return err
	}
	// pick the first node in the list so we only have a pool of one
	node := nodeList.Items[0]

	// create a new pool with a subset of the nodes
	l := fmt.Sprintf("node-role.kubernetes.io/%s", n)

	// label nodes
	nodeCopy := node.DeepCopy()
	nodeCopy.Labels[l] = ""

	log.Printf("adding label %s to node %s\n", l, node.Name)
	updateErr := backoff.RetryNotify(
		func() error {
			return f.Client.Update(context.TODO(), nodeCopy)
		},
		defaultBackoff,
		func(err error, interval time.Duration) {
			log.Printf("failed to label node %s: %s... retrying after %s", node.Name, err, interval)
		})
	if updateErr != nil {
		log.Printf("failed to label node %s: %s\n", node.Name, l)
		return fmt.Errorf("couldn't label node %s: %w", node.Name, updateErr)
	}

	nodeLabel := make(map[string]string)
	nodeLabel[l] = ""
	poolLabels := make(map[string]string)
	poolLabels["pools.operator.machineconfiguration.openshift.io/e2e"] = ""
	newPool := &mcfgv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{Name: n, Labels: poolLabels},
		Spec: mcfgv1.MachineConfigPoolSpec{
			NodeSelector: &metav1.LabelSelector{
				MatchLabels: nodeLabel,
			},
			MachineConfigSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      mcfgv1.MachineConfigRoleLabelKey,
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{w, n},
					},
				},
			},
		},
	}

	// We create but don't clean up, we'll call a function for this since we need to
	// re-label hosts first.
	createErr := backoff.RetryNotify(
		func() error {
			err := f.Client.Create(context.TODO(), newPool, nil)
			if apierrors.IsAlreadyExists(err) {
				return nil
			}
			return err
		},
		defaultBackoff,
		func(err error, interval time.Duration) {
			log.Printf("failed to create Machine Config Pool %s: %s... retrying after %s", n, err, interval)
		})
	if createErr != nil {
		return fmt.Errorf("failed to create Machine Config Pool %s: %w", n, createErr)
	}

	// wait for pool to come up
	err = wait.PollImmediate(machineOperationRetryInterval, machineOperationTimeout, func() (bool, error) {
		pool := mcfgv1.MachineConfigPool{}
		err := f.Client.Get(context.TODO(), types.NamespacedName{Name: n}, &pool)
		if err != nil {
			log.Printf("failed to find Machine Config Pool %s\n", n)
			return false, err
		}

		for _, c := range pool.Status.Conditions {
			if c.Type == mcfgv1.MachineConfigPoolUpdated {
				if c.Status == core.ConditionTrue {
					return true, nil
				}
			}
		}

		log.Printf("%s Machine Config Pool has not updated... retrying\n", n)
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("failed waiting for Machine Config Pool %s to become available: %w", n, err)
	}

	log.Printf("successfully created Machine Config Pool %s\n", n)
	return nil
}

func (f *Framework) createInvalidMachineConfigPool(n string) error {
	if f.Platform == "rosa" {
		fmt.Printf("bypassing MachineConfigPool test setup because it's not supported on %s\n", f.Platform)
		return nil
	}
	p := &mcfgv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{Name: n},
		Spec: mcfgv1.MachineConfigPoolSpec{
			Paused: false,
		},
	}

	createErr := backoff.RetryNotify(
		func() error {
			err := f.Client.Create(context.TODO(), p, nil)
			if apierrors.IsAlreadyExists(err) {
				log.Printf("Machine Config Pool %s already exists", n)
				return nil
			}
			return err
		},
		defaultBackoff,
		func(err error, interval time.Duration) {
			log.Printf("error creating Machine Config Pool %s: %s... retrying after %s", n, err, interval)
		})
	if createErr != nil {
		return fmt.Errorf("failed to create Machine Config Pool %s: %w", n, createErr)
	}
	return nil
}

func (f *Framework) cleanUpMachineConfigPool(n string) error {
	if f.Platform == "rosa" {
		fmt.Printf("bypassing MachineConfigPool cleanup because it's not supported on %s\n", f.Platform)
		return nil
	}
	p := &mcfgv1.MachineConfigPool{}
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: n}, p)
	if err != nil {
		return fmt.Errorf("failed to get Machine Config Pool %s for cleanup: %w", n, err)
	}
	log.Printf("cleaning up Machine Config Pool %s", n)
	err = f.Client.Delete(context.TODO(), p)
	if err != nil {
		return fmt.Errorf("failed to cleanup Machine Config Pool %s: %w", n, err)
	}
	return nil
}

func (f *Framework) restoreNodeLabelsForPool(n string) error {
	if f.Platform == "rosa" {
		fmt.Printf("bypassing node label restoration because MachineConfigPools are not supported on %s\n", f.Platform)
		return nil
	}
	p := &mcfgv1.MachineConfigPool{}
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: n}, p)
	if err != nil {
		return fmt.Errorf("failed to get Machine Config Pool %s for cleanup: %w", n, err)
	}

	nodeList, err := f.getNodesForPool(p)
	nodes := nodeList.Items
	if err != nil {
		return fmt.Errorf("failed to find nodes while cleaning up Machine Config Pool %s: %w", n, err)
	}
	rmPoolLabel := utils.GetFirstNodeRoleLabel(p.Spec.NodeSelector.MatchLabels)

	err = f.removeLabelFromNode(rmPoolLabel, nodes)
	if err != nil {
		return err
	}

	// Unlabeling the nodes triggers an update of the affected nodes because the nodes
	// will then start using a different rendered pool. e.g a node that used to be labeled
	// with "e2e,worker" and becomes labeled with "worker" switches from "rendered-e2e-*"
	// to "rendered-worker-*". If we didn't wait, the node might have tried to use the
	// e2e pool that would be gone when we remove it with the next call
	err = f.waitForNodesToHaveARenderedPool(nodes, n)
	if err != nil {
		return fmt.Errorf("failed removing nodes from Machine Config Pool %s: %w", n, err)
	}
	err = wait.PollImmediate(machineOperationRetryInterval, machineOperationTimeout, func() (bool, error) {
		pool := mcfgv1.MachineConfigPool{}
		err := f.Client.Get(context.TODO(), types.NamespacedName{Name: n}, &pool)
		if err != nil {
			return false, fmt.Errorf("failed to get Machine Config Pool %s: %w", n, err)
		}
		for _, c := range pool.Status.Conditions {
			if c.Type == mcfgv1.MachineConfigPoolUpdated && c.Status == core.ConditionTrue {
				return true, nil
			}
		}

		log.Printf("the Machine Config Pool %s has not updated yet\n", n)
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("failed waiting for nodes to reboot after being unlabeled: %w", err)
	}

	return nil
}

func (f *Framework) getNodesForPool(p *mcfgv1.MachineConfigPool) (core.NodeList, error) {
	var nodeList core.NodeList
	opts := &dynclient.ListOptions{
		LabelSelector: labels.SelectorFromSet(p.Spec.NodeSelector.MatchLabels),
	}
	listErr := backoff.Retry(
		func() error {
			return f.Client.List(context.TODO(), &nodeList, opts)
		},
		defaultBackoff)
	if listErr != nil {
		return nodeList, fmt.Errorf("couldn't list nodes with selector %s: %w", p.Spec.NodeSelector.MatchLabels, listErr)
	}
	return nodeList, nil
}

func (f *Framework) removeLabelFromNode(l string, nodes []core.Node) error {
	for _, n := range nodes {
		c := n.DeepCopy()
		delete(c.Labels, l)

		fmt.Printf("removing label %s from node %s\n", l, c.Name)
		err := f.Client.Update(context.TODO(), c)
		if err != nil {
			return fmt.Errorf("failed to remove label %s from node %s: %s", l, c.Name, err)
		}
	}

	return nil
}

// waitForNodesToHaveARenderedPool waits until all nodes passed through a
// parameter transition to a rendered configuration from a pool. A typical
// use-case is when a node is unlabeled from a pool and must wait until Machine
// Config Operator makes the node use the other available pool. Only then it is
// safe to remove the pool the node was labeled with, otherwise the node might
// still use the deleted pool on next reboot and enter a Degraded state.
func (f *Framework) waitForNodesToHaveARenderedPool(nodes []core.Node, n string) error {
	p := &mcfgv1.MachineConfigPool{}
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: n}, p)
	if err != nil {
		return fmt.Errorf("failed to find Machine Config Pool %s: %w", n, err)
	}

	fmt.Printf("waiting for nodes to reach %s\n", p.Spec.Configuration.Name)
	return wait.PollImmediateInfinite(machineOperationRetryInterval, func() (bool, error) {
		for _, loopNode := range nodes {
			node := &core.Node{}
			err := backoff.Retry(func() error {
				return f.Client.Get(context.TODO(), types.NamespacedName{Name: loopNode.Name}, node)
			}, backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries))

			if err != nil {
				return false, err
			}

			fmt.Printf("Node %s has config %s, desired config %s state %s",
				node.Name,
				node.Annotations["machineconfiguration.openshift.io/currentConfig"],
				node.Annotations["machineconfiguration.openshift.io/desiredConfig"],
				node.Annotations["machineconfiguration.openshift.io/state"])

			if node.Annotations["machineconfiguration.openshift.io/desiredConfig"] != p.Spec.Configuration.Name ||
				node.Annotations["machineconfiguration.openshift.io/currentConfig"] != node.Annotations["machineconfiguration.openshift.io/desiredConfig"] {
				log.Printf("node %s still updating", node.Name)
				return false, nil
			}
			log.Printf("node %s was updated", node.Name)
		}
		log.Printf("all nodes in Machine Config Pool %s were updated successfully", n)
		return true, nil
	})
}

func (f *Framework) WaitForScanStatus(namespace, name string, targetStatus compv1alpha1.ComplianceScanStatusPhase) error {
	exampleComplianceScan := &compv1alpha1.ComplianceScan{}
	var lastErr error
	defer f.logContainerOutput(namespace, name)
	// retry and ignore errors until timeout
	timeoutErr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		lastErr = f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, exampleComplianceScan)
		if lastErr != nil {
			if apierrors.IsNotFound(lastErr) {
				log.Printf("Waiting for availability of %s compliancescan\n", name)
				return false, nil
			}
			log.Printf("Retrying. Got error: %v\n", lastErr)
			return false, nil
		}

		switch exampleComplianceScan.Status.Phase {
		case compv1alpha1.PhaseLaunching:
			// assert exampleComplianceScan.Annotations[compv1alpha1.ComplianceCheckCountAnnotation] is empty
			if exampleComplianceScan.Annotations[compv1alpha1.ComplianceCheckCountAnnotation] != "" {
				return true, fmt.Errorf("compliancescan %s should not have checkCount annotation", name)
			}
		case compv1alpha1.PhaseRunning:
			// assert exampleComplianceScan.Annotations[compv1alpha1.ComplianceCheckCountAnnotation] is empty
			if exampleComplianceScan.Annotations[compv1alpha1.ComplianceCheckCountAnnotation] != "" {
				return true, fmt.Errorf("compliancescan %s should not have checkCount annotation", name)
			}
		}

		if exampleComplianceScan.Status.Phase == targetStatus {
			return true, nil
		}
		log.Printf("Waiting for run of %s compliancescan (%s)\n", name, exampleComplianceScan.Status.Phase)
		return false, nil
	})

	if timeoutErr != nil {
		return fmt.Errorf("failed waiting for scan %s due to timeout: %s", name, timeoutErr)
	}
	if lastErr != nil {
		return fmt.Errorf("failed waiting for scan %s: %s", name, lastErr)
	}

	log.Printf("ComplianceScan ready (%s)\n", exampleComplianceScan.Status.Phase)
	return nil
}

func (f *Framework) WaitForScanSettingBindingStatus(namespace, name string, targetStatus compv1alpha1.ScanSettingBindingStatusPhase) error {
	b := &compv1alpha1.ScanSettingBinding{}
	var err error
	// retry and ignore errors until timeout
	timeoutErr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		err = f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, b)
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.Printf("Waiting for availability of %s ScanSettingBinding\n", name)
				return false, nil
			}
			log.Printf("Retrying. Got error: %v\n", err)
			return false, nil
		}

		if b.Status.Phase == targetStatus {
			return true, nil
		}
		log.Printf("Waiting for ScanSettingBinding %s to reach status %s, it is currently %s\n", name, targetStatus, b.Status.Phase)
		return false, nil
	})

	if timeoutErr != nil {
		return fmt.Errorf("failed waiting for ScanSettingBinding %s due to timeout: %s", name, timeoutErr)
	}
	if err != nil {
		return fmt.Errorf("failed waiting for ScanSettingBinding %s to reach status %s: %s", name, targetStatus, err)
	}

	log.Printf("ScanSettingBinding status %s\n", b.Status.Phase)
	return nil
}

func (f *Framework) AssertProfileInRuleAnnotation(r *compv1alpha1.Rule, expectedProfileId string) bool {
	if r.Annotations == nil {
		return false
	}
	profileIds := r.Annotations[compv1alpha1.RuleProfileAnnotationKey]
	if profileIds == "" {
		return false
	}
	return strings.Contains(profileIds, expectedProfileId)
}

func (f *Framework) EnableRuleExistInTailoredProfile(namespace, name, ruleName string) (bool, error) {
	tp := &compv1alpha1.TailoredProfile{}
	defer f.logContainerOutput(namespace, name)
	// check if rule exist in tailoredprofile enableRules
	err := f.Client.Get(context.TODO(), types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, tp)
	if err != nil {
		return false, err
	}

	hasRule := false
	for _, r := range tp.Spec.EnableRules {
		if r.Name == ruleName {
			hasRule = true
			break
		}
	}
	if hasRule {
		log.Printf("rule %s exist in tailoredprofile enableRules\n", ruleName)
		return true, nil
	} else {
		log.Printf("rule %s does not exist in tailoredprofile enableRules\n", ruleName)
		return false, nil
	}
}

func (f *Framework) WaitForTailoredProfileStatus(namespace, name string, targetStatus compv1alpha1.TailoredProfileState) error {
	tp := &compv1alpha1.TailoredProfile{}
	var lastErr error
	defer f.logContainerOutput(namespace, name)
	// retry and ignore errors until timeout
	timeoutErr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		lastErr = f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, tp)
		if lastErr != nil {
			if apierrors.IsNotFound(lastErr) {
				log.Printf("Waiting for availability of %s tailoredprofile\n", name)
				return false, nil
			}
			log.Printf("Retrying. Got error: %v\n", lastErr)
			return false, nil
		}

		if tp.Status.State == targetStatus {
			return true, nil
		}
		log.Printf("Waiting for run of %s tailoredprofile (%s)\n", name, tp.Status.State)
		return false, nil
	})

	if timeoutErr != nil {
		return fmt.Errorf("failed waiting for tailoredprofile %s due to timeout: %s", name, timeoutErr)
	}
	if lastErr != nil {
		return fmt.Errorf("failed waiting for tailoredprofile %s: %s", name, lastErr)
	}

	log.Printf("TailoredProfile ready (%s)\n", tp.Status.State)
	return nil
}

func (f *Framework) WaitForCustomRuleStatus(namespace, name string, targetPhase string) error {
	rule := &compv1alpha1.CustomRule{}
	var lastErr error
	defer f.logContainerOutput(namespace, name)
	// retry and ignore errors until timeout
	timeoutErr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		lastErr = f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, rule)
		if lastErr != nil {
			if apierrors.IsNotFound(lastErr) {
				log.Printf("Waiting for availability of %s customrule\n", name)
				return false, nil
			}
			log.Printf("Retrying. Got error: %v\n", lastErr)
			return false, nil
		}

		if rule.Status.Phase == targetPhase {
			return true, nil
		}
		log.Printf("Waiting for %s customrule to reach phase %s (current: %s)\n", name, targetPhase, rule.Status.Phase)
		if rule.Status.ErrorMessage != "" {
			log.Printf("CustomRule error message: %s\n", rule.Status.ErrorMessage)
		}
		return false, nil
	})

	if timeoutErr != nil {
		return fmt.Errorf("failed waiting for customrule %s due to timeout: %s", name, timeoutErr)
	}
	if lastErr != nil {
		return fmt.Errorf("failed waiting for customrule %s: %s", name, lastErr)
	}

	log.Printf("CustomRule ready (%s)\n", rule.Status.Phase)
	return nil
}

// waitForScanStatus will poll until the compliancescan that we're lookingfor reaches a certain status, or until
// a timeout is reached.
func (f *Framework) WaitForSuiteScansStatus(namespace, name string, targetStatus compv1alpha1.ComplianceScanStatusPhase, targetComplianceStatus compv1alpha1.ComplianceScanStatusResult) error {
	suite := &compv1alpha1.ComplianceSuite{}
	var lastErr error
	// retry and ignore errors until timeout
	defer f.logContainerOutput(namespace, name)
	timeouterr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		lastErr = f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, suite)
		if lastErr != nil {
			if apierrors.IsNotFound(lastErr) {
				log.Printf("waiting for availability of %s compliancesuite\n", name)
				return false, nil
			}
			log.Printf("retrying. Got error: %v\n", lastErr)
			return false, nil
		}

		// Loop through each scan to make sure it doesn't have the
		// rescan annotation. This makes it safer to poll for suite
		// status because we're making sure the ComplianceScan
		// controller has the opportunity to update the scan status for
		// each scan and remove the annotation before we look at the
		// overall suite status. Without this, it's possible to rerun a
		// scan and immediately pass through the
		// WaitForSuiteScansStatus function because the ComplianceScan
		// objects are still referencing the old scan data and haven't
		// been reset to "PENDING".
		for _, scanStatus := range suite.Status.ScanStatuses {
			key := types.NamespacedName{Name: scanStatus.Name, Namespace: namespace}
			scan := &compv1alpha1.ComplianceScan{}
			err := f.Client.Get(context.TODO(), key, scan)
			if err != nil {
				if apierrors.IsNotFound(err) {
					log.Printf("Waiting for scan %s to appear in suite: %s", scanStatus.Name, name)
					return false, nil
				}
				return false, fmt.Errorf("failed to get scan %s: %s", scanStatus.Name, err)
			}
			if _, exists := scan.Annotations[compv1alpha1.ComplianceScanRescanAnnotation]; !exists {
				// Move along, the rescan annotation has been
				// removed (if the caller did use it to perform
				// a rescan) by the ComplianceScan controller
				// so we know we're not looking at stale
				// results from a previous run.
				continue
			}

			log.Printf("Waiting for ComplianceScan controller to remove rescan annotation for %s", scan.Name)
			return false, nil
		}

		if suite.Status.Phase != targetStatus {
			log.Printf("waiting until suite %s reaches target status '%s'. Current status: %s", suite.Name, targetStatus, suite.Status.Phase)
			return false, nil
		}

		// The suite is now done, make sure the compliance status is expected
		if suite.Status.Result != targetComplianceStatus {
			return false, fmt.Errorf("expecting %s got %s", targetComplianceStatus, suite.Status.Result)
		}

		// If we were expecting an error, there's no use checking the scans
		if targetComplianceStatus == compv1alpha1.ResultError {
			return true, nil
		}

		// Now as a sanity check make sure that the scan statuses match the aggregated
		// suite status

		// Got the suite. There should be at least one scan or else we're still initialising
		if len(suite.Status.ScanStatuses) < 1 {
			return false, errors.New("not enough scan statuses")
		}

		//Examine the scan status both in the suite status and the scan
		for _, scanStatus := range suite.Status.ScanStatuses {
			if scanStatus.Phase != targetStatus {
				return false, fmt.Errorf("suite in status %s but scan wrapper %s in status %s", targetStatus, scanStatus.Name, scanStatus.Phase)
			}

			// If the status was present in the suite, then /any/ error
			// should fail the test as the scans should be read /from/
			// the scan itself
			f.WaitForScanStatus(namespace, scanStatus.Name, targetStatus)
		}

		return true, nil
	})

	// Error in function call
	if lastErr != nil {
		return lastErr
	}

	// Timeout
	if timeouterr != nil {
		return timeouterr
	}

	log.Printf("All scans in ComplianceSuite have finished (%s)\n", suite.Name)
	return nil
}

func (f *Framework) RescanSuite(suiteName, namespace string) error {
	scanList := &compv1alpha1.ComplianceScanList{}
	labelSelector, err := labels.Parse(compv1alpha1.SuiteLabel + "=" + suiteName)
	if err != nil {
		return fmt.Errorf("Failed to parse label selector: %s", err)
	}

	opts := &client.ListOptions{
		LabelSelector: labelSelector,
	}
	err = f.Client.List(context.TODO(), scanList, opts)
	if err != nil {
		return fmt.Errorf("Failed to get scans for suite %s", suiteName)
	}

	// Add rescan annotation to each scan
	for i := range scanList.Items {
		scan := &scanList.Items[i]
		err := f.RescanScan(scan.Name, scan.Namespace)
		if err != nil {
			return fmt.Errorf("Failed to apply rescan annotation to scan %s: %s", scan.Name, err)
		}
	}
	return nil
}

func (f *Framework) RescanScan(scanName, namespace string) error {
	key := types.NamespacedName{Name: scanName, Namespace: namespace}

	interval := 5 * time.Second
	retries := uint64(30)
	bo := backoff.WithMaxRetries(backoff.NewConstantBackOff(interval), retries)
	err := backoff.RetryNotify(func() error {
		s := &compv1alpha1.ComplianceScan{}
		err := f.Client.Get(context.TODO(), key, s)
		if err != nil {
			return fmt.Errorf("failed to get scan %s: %s", scanName, err)
		}

		// Add rescan annotation
		if s.Annotations == nil {
			s.Annotations = make(map[string]string)
		}
		s.Annotations[compv1alpha1.ComplianceScanRescanAnnotation] = ""

		return f.Client.Update(context.TODO(), s)
	}, bo, func(err error, d time.Duration) {
		log.Printf("Failed to add rescan annotation to scan %s after %s: %s", scanName, d.String(), err)
	})
	if err != nil {
		return fmt.Errorf("Failed to trigger rescan for scan %s: %s", scanName, err)
	}
	log.Printf("Triggered rescan for scan %s", scanName)
	return nil
}

func (f *Framework) logContainerOutput(namespace, name string) {
	logContainerOutputEnv := os.Getenv("LOG_CONTAINER_OUTPUT")
	if logContainerOutputEnv == "" {
		return
	}

	// Try all container/init variants for each pod and the pod itself (self), log nothing if the container is not applicable.
	containers := []string{"self", "api-resource-collector", "log-collector", "scanner", "content-container"}
	artifacts := os.Getenv("ARTIFACT_DIR")
	if artifacts == "" {
		return
	}
	pods, err := f.getPodsForScan(name)
	if err != nil {
		log.Printf("Warning: Error getting pods for container logging: %s", err)
	} else {
		for _, pod := range pods {
			for _, con := range containers {
				logOpts := &core.PodLogOptions{}
				if con != "self" {
					logOpts.Container = con
				}
				req := f.KubeClient.CoreV1().Pods(namespace).GetLogs(pod.Name, logOpts)
				podLogs, err := req.Stream(context.TODO())
				if err != nil {
					// Silence this error if the container is not valid for the pod
					if !apierrors.IsBadRequest(err) {
						log.Printf("error getting logs for %s/%s: reason: %v, err: %v\n", pod.Name, con, apierrors.ReasonForError(err), err)
					}
					continue
				}
				buf := new(bytes.Buffer)
				_, err = io.Copy(buf, podLogs)
				if err != nil {
					log.Printf("error copying logs for %s/%s: %v\n", pod.Name, con, err)
					continue
				}
				logs := buf.String()
				if len(logs) == 0 {
					log.Printf("no logs for %s/%s\n", pod.Name, con)
				} else {
					err := writeToArtifactsDir(artifacts, name, pod.Name, con, logs)
					if err != nil {
						log.Printf("error writing logs for %s/%s: %v\n", pod.Name, con, err)
					} else {
						log.Printf("wrote logs for %s/%s\n", pod.Name, con)
					}
				}
			}
		}
	}
}

func (f *Framework) getPodsForScan(scanName string) ([]core.Pod, error) {
	selectPods := map[string]string{
		compv1alpha1.ComplianceScanLabel: scanName,
	}
	var pods core.PodList
	lo := &dynclient.ListOptions{
		LabelSelector: labels.SelectorFromSet(selectPods),
	}
	err := f.Client.List(context.TODO(), &pods, lo)
	if err != nil {
		return nil, err
	}
	return pods.Items, nil
}

func (f *Framework) AssertScanIsCompliant(name, namespace string) error {
	cs := &compv1alpha1.ComplianceScan{}
	defer f.logContainerOutput(namespace, name)
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, cs)
	if err != nil {
		return err
	}
	if cs.Status.Result != compv1alpha1.ResultCompliant {
		return fmt.Errorf("scan result was %s instead of %s", cs.Status.Result, compv1alpha1.ResultCompliant)
	}
	return nil
}

// AssertComplianceCheckResultTimestamps checks if the timestamps in the compliance check results are within the expected range
func (f *Framework) AssertComplianceCheckResultTimestamps(scanName, namespace string) error {
	cs := &compv1alpha1.ComplianceScan{}
	defer f.logContainerOutput(namespace, scanName)
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: scanName, Namespace: namespace}, cs)
	if err != nil {
		return err
	}
	ccr := &compv1alpha1.ComplianceCheckResultList{}
	lo := &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{
			"compliance.openshift.io/scan-name": scanName,
		}),
	}
	err = f.Client.List(context.TODO(), ccr, lo)
	if err != nil {
		return err
	}
	for _, check := range ccr.Items {
		annotations := check.GetAnnotations()
		if annotations == nil {
			return fmt.Errorf("Check %s has no annotations", check.Name)
		}
		if annotations[compv1alpha1.LastScannedTimestampAnnotation] == "" {
			return fmt.Errorf("Check %s has no timestamp annotation", check.Name)
		}
		timestamp, err := time.Parse(time.RFC3339, annotations[compv1alpha1.LastScannedTimestampAnnotation])
		if err != nil {
			return fmt.Errorf("Error parsing timestamp for check %s: %v", check.Name, err)
		}
		if timestamp.Before(cs.Status.StartTimestamp.Time) || timestamp.After(cs.Status.EndTimestamp.Time) {
			return fmt.Errorf("Timestamp for check %s is not within the expected range: %v", check.Name, timestamp)
		}
	}
	return nil
}

// AssertScanGUIDMatches checks if the scan has the expected GUID
func (f *Framework) AssertScanGUIDMatches(name, namespace, expectedGUID string) error {
	cs := &compv1alpha1.ComplianceScan{}
	defer f.logContainerOutput(namespace, name)
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, cs)
	if err != nil {
		return err
	}
	if cs.Labels[compv1alpha1.ProfileGuidLabel] != expectedGUID {
		return fmt.Errorf("Expected GUID %s for scan %s, got %s", expectedGUID, name, cs.Labels[compv1alpha1.ProfileGuidLabel])
	}
	return nil
}

// AssertProfileGUIDMatches checks if the profile has the expected GUID
func (f *Framework) AssertProfileGUIDMatches(name, namespace, expectedGUID string) error {
	profile := &compv1alpha1.Profile{}
	defer f.logContainerOutput(namespace, name)
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, profile)
	if err != nil {
		return err
	}
	if profile.Labels[compv1alpha1.ProfileGuidLabel] != expectedGUID {
		return fmt.Errorf("Expected GUID %s for profile %s, got %s", expectedGUID, name, profile.Labels[compv1alpha1.ProfileGuidLabel])
	}
	return nil
}

func (f *Framework) AssertScanIsNonCompliant(name, namespace string) error {
	cs := &compv1alpha1.ComplianceScan{}
	defer f.logContainerOutput(namespace, name)
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, cs)
	if err != nil {
		return err
	}
	if cs.Status.Result != compv1alpha1.ResultNonCompliant {
		return fmt.Errorf("scan result was %s instead of %s", cs.Status.Result, compv1alpha1.ResultNonCompliant)
	}
	return nil
}

func (f *Framework) AssertScanIsNotApplicable(name, namespace string) error {
	cs := &compv1alpha1.ComplianceScan{}
	defer f.logContainerOutput(namespace, name)
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, cs)
	if err != nil {
		return err
	}
	if cs.Status.Result != compv1alpha1.ResultNotApplicable {
		return fmt.Errorf("scan result was %s instead of %s", cs.Status.Result, compv1alpha1.ResultNotApplicable)
	}
	return nil
}

func (f *Framework) AssertScanIsInError(name, namespace string) error {
	cs := &compv1alpha1.ComplianceScan{}
	defer f.logContainerOutput(namespace, name)
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, cs)
	if err != nil {
		return err
	}
	if cs.Status.Result != compv1alpha1.ResultError {
		return fmt.Errorf("scan result was %s instead of %s", cs.Status.Result, compv1alpha1.ResultError)
	}
	if cs.Status.ErrorMessage == "" {
		return fmt.Errorf("scan 'errormsg' is empty, but it should be set")
	}
	return nil
}

func (f *Framework) AssertScanHasValidPVCReference(scanName, namespace string) error {
	scan := &compv1alpha1.ComplianceScan{}
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: scanName, Namespace: namespace}, scan)
	if err != nil {
		return err
	}
	pvc := &core.PersistentVolumeClaim{}
	pvcName := scan.Status.ResultsStorage.Name
	pvcNamespace := scan.Status.ResultsStorage.Namespace
	return f.Client.Get(context.TODO(), types.NamespacedName{Name: pvcName, Namespace: pvcNamespace}, pvc)
}

func (f *Framework) AssertScanHasValidPVCReferenceWithSize(scanName, size, namespace string) error {
	scan := &compv1alpha1.ComplianceScan{}
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: scanName, Namespace: namespace}, scan)
	if err != nil {
		return err
	}
	pvc := &core.PersistentVolumeClaim{}
	pvcName := scan.Status.ResultsStorage.Name
	pvcNamespace := scan.Status.ResultsStorage.Namespace
	err = f.Client.Get(context.TODO(), types.NamespacedName{Name: pvcName, Namespace: pvcNamespace}, pvc)
	if err != nil {
		return err
	}
	qty := resource.MustParse(size)
	if qty.Cmp(*pvc.Status.Capacity.Storage()) != 0 {
		expected := qty.String()
		current := pvc.Status.Capacity.Storage().String()
		return fmt.Errorf("Error: PVC '%s' storage doesn't match expected value. Has '%s', Expected '%s'", pvc.Name, current, expected)
	}
	return nil
}

func (f *Framework) AssertARFReportExistsInPVC(scanName, namespace string) error {
	pvcName, err := f.GetRawResultClaimNameFromScan(namespace, scanName)
	if err != nil {
		return err
	}

	arfFormatCheckerPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scanName + "-arf-checker",
			Namespace: namespace,
		},
		Spec: core.PodSpec{
			RestartPolicy: core.RestartPolicyNever,
			Containers: []core.Container{
				{
					Name:    "format-checker",
					Image:   "registry.access.redhat.com/ubi8/ubi-minimal",
					Command: []string{"/bin/bash", "-c", "ls /scan-results/0 2>/dev/null | grep -q '.xml.bzip2' && exit 0 || exit 1"},
					VolumeMounts: []core.VolumeMount{
						{
							Name:      "scan-results",
							MountPath: "/scan-results",
						},
					},
				},
			},
			Volumes: []core.Volume{
				{
					Name: "scan-results",
					VolumeSource: core.VolumeSource{
						PersistentVolumeClaim: &core.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
		},
	}
	if err = f.Client.Create(context.TODO(), arfFormatCheckerPod, nil); err != nil {
		return fmt.Errorf("failed to create %s pod: %s", arfFormatCheckerPod.Name, err)
	}
	defer f.Client.Delete(context.TODO(), arfFormatCheckerPod)

	pod := &core.Pod{}
	key := types.NamespacedName{Name: arfFormatCheckerPod.Name, Namespace: namespace}
	return wait.Poll(2*time.Second, 10*time.Second, func() (bool, error) {
		if err := f.Client.Get(context.TODO(), key, pod); err != nil {
			return false, nil
		}
		if pod.Status.Phase == core.PodSucceeded {
			return true, nil
		}
		if pod.Status.Phase == core.PodFailed {
			return false, fmt.Errorf("Scan results in the PVC are not in the required ARF format (scan: %s, pvc: %s, pod: %s)", scanName, pvcName, pod.Name)
		}
		return false, nil
	})
}

func (f *Framework) AssertScanExists(name, namespace string) error {
	cs := &compv1alpha1.ComplianceScan{}
	defer f.logContainerOutput(namespace, name)
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, cs)
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("Failed to assert ComplianceScan %s exists: %w", name, err)
	}
	return err
}

func (f *Framework) AssertScanDoesNotExist(name, namespace string) error {
	cs := &compv1alpha1.ComplianceScan{}
	defer f.logContainerOutput(namespace, name)
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, cs)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (f *Framework) AssertComplianceSuiteDoesNotExist(name, namespace string) error {
	cs := &compv1alpha1.ComplianceSuite{}
	defer f.logContainerOutput(namespace, name)
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, cs)
	if apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("Failed to assert ComplianceSuite %s does not exist.", name)
}

func (f *Framework) AssertScanSettingBindingConditionIsReady(name string, namespace string) error {
	ssb := &compv1alpha1.ScanSettingBinding{}
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, ssb)
	if err != nil {
		return fmt.Errorf("Failed to get ScanSettingBinding %s: %w", name, err)
	}
	condition := ssb.Status.Conditions.GetCondition("Ready")
	if condition != nil && condition.Status == "True" {
		return nil
	}
	return fmt.Errorf("expected ScanSettingBinding %s %s condition to be valid", ssb.Name, condition.Type)

}

func (f *Framework) AssertScanSettingBindingConditionIsSuspended(name string, namespace string) error {
	ssb := &compv1alpha1.ScanSettingBinding{}
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, ssb)
	if err != nil {
		return fmt.Errorf("Failed to get ScanSettingBinding %s: %w", name, err)
	}

	condition := ssb.Status.Conditions.GetCondition("Ready")
	if condition != nil && condition.Status == "False" && condition.Reason == "Suspended" {
		return nil
	}
	return fmt.Errorf("expected ScanSettingBinding %s %s condition to reflect suspended status", ssb.Name, condition.Type)

}

func (f *Framework) ScanHasWarnings(scanName, namespace string) error {
	cs := &compv1alpha1.ComplianceScan{}
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: scanName, Namespace: namespace}, cs)
	if err != nil {
		return err
	}
	if cs.Status.Warnings == "" {
		return fmt.Errorf("E2E-FAILURE: Excepted the scan %s to contain a warning", scanName)
	}
	return nil
}

// GetNodesWithSelector lists nodes according to a specific selector
func (f *Framework) GetNodesWithSelector(labelselector map[string]string) ([]core.Node, error) {
	var nodes core.NodeList
	lo := &dynclient.ListOptions{
		LabelSelector: labels.SelectorFromSet(labelselector),
	}
	listErr := backoff.Retry(
		func() error {
			return f.Client.List(context.TODO(), &nodes, lo)
		},
		defaultBackoff)
	if listErr != nil {
		return nodes.Items, fmt.Errorf("couldn't list nodes with selector %s: %w", labelselector, listErr)
	}
	return nodes.Items, nil
}

// GetConfigMapsFromScan lists the configmaps from the specified openscap scan instance
func (f *Framework) GetConfigMapsFromScan(scaninstance *compv1alpha1.ComplianceScan) ([]core.ConfigMap, error) {
	var configmaps core.ConfigMapList
	labelselector := map[string]string{
		compv1alpha1.ComplianceScanLabel: scaninstance.Name,
		compv1alpha1.ResultLabel:         "",
	}
	lo := &dynclient.ListOptions{
		LabelSelector: labels.SelectorFromSet(labelselector),
	}
	err := f.Client.List(context.TODO(), &configmaps, lo)
	if err != nil {
		return configmaps.Items, err
	}
	return configmaps.Items, nil
}

func (f *Framework) GetPodsForScan(scanName string) ([]core.Pod, error) {
	selectPods := map[string]string{
		compv1alpha1.ComplianceScanLabel: scanName,
	}
	var pods core.PodList
	lo := &dynclient.ListOptions{
		LabelSelector: labels.SelectorFromSet(selectPods),
	}
	err := f.Client.List(context.TODO(), &pods, lo)
	if err != nil {
		return nil, err
	}
	return pods.Items, nil
}

// WaitForRemediationState will poll until the complianceRemediation that we're lookingfor gets applied, or until
// a timeout is reached.
func (f *Framework) WaitForRemediationState(name, namespace string, state compv1alpha1.RemediationApplicationState) error {
	rem := &compv1alpha1.ComplianceRemediation{}
	var lastErr error
	// retry and ignore errors until timeout
	timeouterr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		lastErr = f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, rem)
		if lastErr != nil {
			if apierrors.IsNotFound(lastErr) {
				log.Printf("Waiting for availability of %s ComplianceRemediation\n", name)
				return false, nil
			}
			log.Printf("Retrying. Got error: %v\n", lastErr)
			return false, nil
		}

		if rem.Status.ApplicationState == state {
			return true, nil
		}
		log.Printf("Waiting for run of %s ComplianceRemediation (%s)\n", name, rem.Status.ApplicationState)
		return false, nil
	})
	// Error in function call
	if lastErr != nil {
		return lastErr
	}
	// Timeout
	if timeouterr != nil {
		return timeouterr
	}
	log.Printf("ComplianceRemediation ready (%s)\n", rem.Status.ApplicationState)
	return nil
}

func (f *Framework) WaitForObjectToExist(name, namespace string, obj dynclient.Object) error {
	var lastErr error
	// retry and ignore errors until timeout
	timeouterr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		lastErr = f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, obj)
		if lastErr != nil {
			if apierrors.IsNotFound(lastErr) {
				log.Printf("Waiting for availability of %s ComplianceRemediation\n", name)
				return false, nil
			}
			log.Printf("Retrying. Got error: %v\n", lastErr)
			return false, nil
		}

		return true, nil
	})
	// Error in function call
	if lastErr != nil {
		return lastErr
	}
	// Timeout
	if timeouterr != nil {
		return timeouterr
	}

	log.Printf("Object found '%s' found\n", name)
	return nil
}

func (f *Framework) WaitForObjectToUpdate(name, namespace string, obj ObjectResouceVersioner) error {
	var lastErr error

	initialVersion := obj.GetResourceVersion()

	// retry and ignore errors until timeout
	timeouterr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		lastErr = f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, obj)
		if lastErr != nil {
			log.Printf("Retrying. Got error: %v\n", lastErr)
			return false, nil
		}
		if obj.GetResourceVersion() == initialVersion {
			log.Printf("Retrying. Object still doesn't update. got version %s ... wanted %s\n", obj.GetResourceVersion(), initialVersion)
			return false, nil
		}

		return true, nil
	})
	// Error in function call
	if lastErr != nil {
		return lastErr
	}
	// Timeout
	if timeouterr != nil {
		return timeouterr
	}

	log.Printf("Object found '%s' found\n", name)
	return nil
}

func (f *Framework) SuiteErrorMessageMatchesRegex(namespace, name, regexToMatch string) error {
	log.Printf("Fetching suite: '%s'\n", name)
	cs := &compv1alpha1.ComplianceSuite{}
	key := types.NamespacedName{Name: name, Namespace: namespace}
	err := f.Client.Get(context.TODO(), key, cs)
	if err != nil {
		return err
	}
	re := regexp.MustCompile(regexToMatch)
	if !re.MatchString(cs.Status.ErrorMessage) {
		return fmt.Errorf("the error message found in the compliance suite '%s' "+
			"didn't match the expected regex. Found: '%s', Expected regex: '%s'",
			name, cs.Status.ErrorMessage, regexToMatch)
	}
	return nil
}

// WaitForReScanStatus will poll until the compliancescan that we're lookingfor reaches a certain status for a re-scan, or until
// a timeout is reached.
func (f *Framework) WaitForReScanStatus(namespace, name string, targetStatus compv1alpha1.ComplianceScanStatusPhase) error {
	foundScan := &compv1alpha1.ComplianceScan{}
	// unset initial index
	var scanIndex int64 = -1
	var lastErr error
	// retry and ignore errors until timeout
	timeouterr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		lastErr = f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, foundScan)
		if lastErr != nil {
			if apierrors.IsNotFound(lastErr) {
				log.Printf("Waiting for availability of %s compliancescan\n", name)
				return false, nil
			}
			log.Printf("Retrying. Got error: %v\n", lastErr)
			return false, nil
		}
		// Set index
		if scanIndex == -1 {
			scanIndex = foundScan.Status.CurrentIndex
			log.Printf("Initial scan index set to %d. Waiting for re-scan\n", scanIndex)
			return false, nil
		} else if foundScan.Status.CurrentIndex == scanIndex {
			log.Printf("re-scan hasn't taken place. CurrentIndex %d. Waiting for re-scan\n", scanIndex)
			return false, nil
		}

		if foundScan.Status.Phase == targetStatus {
			return true, nil
		}
		log.Printf("Waiting for run of %s compliancescan (%s)\n", name, foundScan.Status.Phase)
		return false, nil
	})
	// Error in function call
	if lastErr != nil {
		return lastErr
	}
	// Timeout
	if timeouterr != nil {
		return timeouterr
	}
	log.Printf("ComplianceScan ready (%s)\n", foundScan.Status.Phase)
	return nil
}

func (f *Framework) GetRawResultClaimNameFromScan(namespace, scanName string) (string, error) {
	scan := &compv1alpha1.ComplianceScan{}
	key := types.NamespacedName{Name: scanName, Namespace: namespace}
	log.Printf("Getting scan to fetch raw storage reference from it: %s/%s", namespace, scanName)
	err := f.Client.Get(context.TODO(), key, scan)
	if err != nil {
		return "", err
	}

	referenceName := scan.Status.ResultsStorage.Name
	if referenceName == "" {
		return "", fmt.Errorf("ResultStorage reference in scan '%s' was empty", scanName)
	}
	return referenceName, nil
}

func GetRotationCheckerWorkload(namespace, rawResultName string) *core.Pod {
	return &core.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rotation-checker",
			Namespace: namespace,
		},
		Spec: core.PodSpec{
			RestartPolicy: core.RestartPolicyOnFailure,
			Containers: []core.Container{
				{
					Name:    "checker",
					Image:   "registry.access.redhat.com/ubi8/ubi-minimal",
					Command: []string{"/bin/bash", "-c", "ls /raw-results | grep -v 'lost+found'"},
					VolumeMounts: []core.VolumeMount{
						{
							Name:      "raw-results",
							MountPath: "/raw-results",
							ReadOnly:  true,
						},
					},
				},
			},
			Volumes: []core.Volume{
				{
					Name: "raw-results",
					VolumeSource: core.VolumeSource{
						PersistentVolumeClaim: &core.PersistentVolumeClaimVolumeSource{
							ClaimName: rawResultName,
							ReadOnly:  true,
						},
					},
				},
			},
		},
	}
}

func (f *Framework) AssertResultStorageHasExpectedItemsAfterRotation(expected int, namespace, checkerPodName string) error {
	// wait for pod to be ready
	pod := &core.Pod{}
	key := types.NamespacedName{Name: checkerPodName, Namespace: namespace}
	log.Printf("Waiting until the raw result checker workload is done: %s/%s", namespace, checkerPodName)
	timeouterr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		err := f.Client.Get(context.TODO(), key, pod)
		if err != nil {
			log.Printf("Got an error while fetching the result checker workload. retrying: %s", err)
			return false, nil
		}
		if pod.Status.Phase == core.PodSucceeded {
			return true, nil
		} else if pod.Status.Phase == core.PodFailed {
			log.Printf("Pod failed!")
			return true, fmt.Errorf("status checker pod failed unexpectedly: %s", pod.Status.Message)
		}
		log.Printf("Pod not done. retrying.")
		return false, nil
	})
	if timeouterr != nil {
		return timeouterr
	}
	logopts := &core.PodLogOptions{
		Container: "checker",
	}
	log.Printf("raw result checker workload is done. Getting logs.")
	req := f.KubeClient.CoreV1().Pods(namespace).GetLogs(checkerPodName, logopts)
	podLogs, err := req.Stream(context.Background())
	if err != nil {
		return err
	}
	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return fmt.Errorf("error in copy information from podLogs to buffer")
	}
	logs := buf.String()
	got := len(strings.Split(strings.Trim(logs, "\n"), "\n"))
	if got != expected {
		return fmt.Errorf(
			"unexpected number of directories came from the result checker.\n"+
				" Expected: %d. Got: %d. Output:\n%s", expected, got, logs)
	}
	log.Printf("raw result checker's output matches rotation policy.")
	return nil
}

func WaitForPod(podCallback wait.ConditionFunc) error {
	return wait.PollImmediate(RetryInterval, Timeout, podCallback)
}

func CheckPodPriorityClass(c kubernetes.Interface, podName, namespace, priorityClass string) wait.ConditionFunc {
	return func() (bool, error) {
		pod, err := c.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}

		if apierrors.IsNotFound(err) {
			log.Printf("Pod %s not found yet\n", podName)
			return false, nil
		}

		if pod.Spec.PriorityClassName != priorityClass {
			log.Printf("pod %s has priority class %s, expected %s\n", podName, pod.Spec.PriorityClassName, priorityClass)
			return true, nil
		}

		return true, nil
	}
}

func (f *Framework) WaitForCronJobWithSchedule(namespace, suiteName, schedule string) error {
	job := &batchv1.CronJob{}
	jobName := compsuitectrl.GetRerunnerName(suiteName)
	var lastErr error
	// retry and ignore errors until timeout
	timeouterr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		lastErr = f.Client.Get(context.TODO(), types.NamespacedName{Name: jobName, Namespace: namespace}, job)
		if lastErr != nil {
			if apierrors.IsNotFound(lastErr) {
				log.Printf("waiting for availability of %s CronJob\n", jobName)
				return false, nil
			}
			log.Printf("Retrying. Got error: %v\n", lastErr)
			return false, nil
		}

		if job.Spec.Schedule != schedule {
			log.Printf("Retrying. Schedule in found job (%s) doesn't match excpeted schedule: %s\n",
				job.Spec.Schedule, schedule)
			return false, nil
		}

		return true, nil
	})
	// Error in function call
	if lastErr != nil {
		return lastErr
	}
	// Timeout
	if timeouterr != nil {
		return timeouterr
	}
	log.Printf("Found %s CronJob\n", jobName)
	return nil
}

func CheckPodLimit(c kubernetes.Interface, podName, namespace, cpuLimit, memLimit string) wait.ConditionFunc {
	return func() (bool, error) {
		pod, err := c.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}

		if apierrors.IsNotFound(err) {
			log.Printf("Pod %s not found yet\n", podName)
			return false, nil
		}

		for i := range pod.Spec.Containers {
			cnt := &pod.Spec.Containers[i]
			if cnt.Name != compscanctrl.PlatformScanResourceCollectorName && cnt.Name != compscanctrl.OpenSCAPScanContainerName {
				continue
			}

			if cnt.Resources.Limits.Cpu().String() != cpuLimit {
				return false, fmt.Errorf("container %s in pod %s has cpu limit %s, expected %s", cnt.Name, podName, cnt.Resources.Limits.Cpu().String(), cpuLimit)
			}

			if cnt.Resources.Limits.Memory().String() != memLimit {
				return false, fmt.Errorf("container %s in pod %s has memory limit %s, expected %s", cnt.Name, podName, cnt.Resources.Limits.Cpu().String(), cpuLimit)
			}
		}

		return true, nil
	}
}

func (f *Framework) AssertHasCheck(suiteName, scanName string, check compv1alpha1.ComplianceCheckResult) error {
	var getCheck compv1alpha1.ComplianceCheckResult

	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: check.Name, Namespace: check.Namespace}, &getCheck)
	if err != nil {
		return err
	}

	if getCheck.Status != check.Status {
		return fmt.Errorf("expected result %s got result %s", check.Status, getCheck.Status)
	}

	if getCheck.ID != check.ID {
		return fmt.Errorf("expected ID %s got ID %s", check.ID, getCheck.ID)
	}

	if getCheck.Labels == nil {
		return fmt.Errorf("complianceCheckResult has no labels")
	}

	if getCheck.Labels[compv1alpha1.SuiteLabel] != suiteName {
		return fmt.Errorf("did not find expected suite name label %s, found %s", suiteName, getCheck.Labels[compv1alpha1.SuiteLabel])
	}

	if getCheck.Labels[compv1alpha1.ComplianceScanLabel] != scanName {
		return fmt.Errorf("did not find expected scan name label %s, found %s", scanName, getCheck.Labels[compv1alpha1.ComplianceScanLabel])
	}

	if getCheck.Labels[compv1alpha1.ComplianceCheckResultSeverityLabel] != string(getCheck.Severity) {
		return fmt.Errorf("did not find expected severity name label %s, found %s", suiteName, getCheck.Labels[compv1alpha1.ComplianceCheckResultSeverityLabel])
	}

	if getCheck.Labels[compv1alpha1.ComplianceCheckResultStatusLabel] != string(getCheck.Status) {
		return fmt.Errorf("did not find expected status name label %s, found %s", suiteName, getCheck.Labels[compv1alpha1.ComplianceCheckResultStatusLabel])
	}

	return nil
}

func (f *Framework) AssertHasRemediations(suiteName, scanName, roleLabel string, remNameList []string) error {
	var scanSuiteMapNames = make(map[string]bool)
	var scanSuiteRemediations []compv1alpha1.ComplianceRemediation

	// FIXME: This is a temporary hack. At the moment, the ARF parser is too slow
	// and it might take a bit for the remediations to appear. It would be cleaner
	// to signify somehow that the remediations were already processed, but in the
	// meantime, poll for 5 minutes while the remediations are being created
	err := wait.PollImmediate(RetryInterval, Timeout, func() (bool, error) {
		var listErr error
		scanSuiteRemediations, listErr = f.getRemediationsFromScan(suiteName, scanName)
		if listErr != nil {
			log.Printf("failed listing remediations, retrying: %s", listErr)
		}
		for idx := range scanSuiteRemediations {
			rem := &scanSuiteRemediations[idx]
			scanSuiteMapNames[rem.Name] = true
		}

		for _, expRem := range remNameList {
			_, ok := scanSuiteMapNames[expRem]
			if !ok {
				log.Printf("expected remediation %s not found, retrying...", expRem)
				return false, nil
			}
		}
		log.Printf("expected remediations found")
		return true, nil
	})

	if err != nil {
		return fmt.Errorf("failed waiting for remediations to appear: %s", err)
	}
	return nil
}

func (f *Framework) getRemediationsFromScan(suiteName, scanName string) ([]compv1alpha1.ComplianceRemediation, error) {
	var scanSuiteRemediations compv1alpha1.ComplianceRemediationList

	scanSuiteSelector := make(map[string]string)
	scanSuiteSelector[compv1alpha1.SuiteLabel] = suiteName
	scanSuiteSelector[compv1alpha1.ComplianceScanLabel] = scanName

	listOpts := dynclient.ListOptions{
		LabelSelector: labels.SelectorFromSet(scanSuiteSelector),
	}

	if err := f.Client.List(context.TODO(), &scanSuiteRemediations, &listOpts); err != nil {
		return nil, err
	}
	return scanSuiteRemediations.Items, nil
}

func (f *Framework) AssertCheckRemediation(name, namespace string, shouldHaveRem bool) error {
	var getCheck compv1alpha1.ComplianceCheckResult

	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, &getCheck)
	if err != nil {
		return err
	}

	_, hasRemLabel := getCheck.Labels[compv1alpha1.ComplianceCheckResultHasRemediation]
	if hasRemLabel != shouldHaveRem {
		return fmt.Errorf("unexpected label found: %v (expected: %s)", getCheck.Labels, strconv.FormatBool(shouldHaveRem))
	}

	// Also make sure a remediation with the same name exists (or not)
	var getRem compv1alpha1.ComplianceRemediation
	var hasRem bool

	err = f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, &getRem)
	if apierrors.IsNotFound(err) {
		hasRem = false
	} else if err != nil {
		return err
	} else {
		hasRem = true
	}

	if hasRemLabel != shouldHaveRem {
		return fmt.Errorf("unexpected remediation object: expected: %s, found: %s", strconv.FormatBool(shouldHaveRem), strconv.FormatBool(hasRem))
	}

	return nil
}

func (f *Framework) AssertRemediationExists(name, namespace string) error {
	var r compv1alpha1.ComplianceRemediation
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, &r)
	if err != nil {
		return fmt.Errorf("Failed to assert ComplianceRemediation %s exists: %w", name, err)
	}
	return nil
}

func (f *Framework) AssertRemediationDoesNotExists(name, namespace string) error {
	var r compv1alpha1.ComplianceRemediation
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, &r)
	if apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("Failed to assert ComplianceRemediation %s does not exist: %w", name, err)
	}
	return fmt.Errorf("Failed to assert ComplianceRemediation %s does not exist.", name)
}

func (f *Framework) TaintNode(node *core.Node, taint core.Taint) error {
	taintedNode := node.DeepCopy()
	if taintedNode.Spec.Taints == nil {
		taintedNode.Spec.Taints = []core.Taint{}
	}
	taintedNode.Spec.Taints = append(taintedNode.Spec.Taints, taint)
	log.Printf("tainting node: %s", taintedNode.Name)
	return f.Client.Update(context.TODO(), taintedNode)
}

func (f *Framework) UntaintNode(nodeName, taintKey string) error {
	var lastErr error

	timeoutErr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		taintedNode := &core.Node{}
		nodeKey := types.NamespacedName{Name: nodeName}
		if err := f.Client.Get(context.TODO(), nodeKey, taintedNode); err != nil {
			log.Printf("failed to get node: %s", nodeName)
			return false, nil
		}
		untaintedNode := taintedNode.DeepCopy()
		untaintedNode.Spec.Taints = []core.Taint{}
		for _, taint := range taintedNode.Spec.Taints {
			if taint.Key != taintKey {
				untaintedNode.Spec.Taints = append(untaintedNode.Spec.Taints, taint)
			}
		}

		log.Printf("removing taint from node: %s", nodeName)
		lastErr = f.Client.Update(context.TODO(), untaintedNode)
		if lastErr != nil {
			log.Printf("failed to remove taint from node %s... retrying", nodeName)
			return false, nil
		}
		return true, nil
	})

	if timeoutErr != nil {
		return fmt.Errorf("couldn't remove node taint. Timed out: %w", timeoutErr)
	}
	if lastErr != nil {
		return fmt.Errorf("couldn't remove node taint. Errored out: %w", lastErr)
	}
	return nil
}

func (f *Framework) WaitForRemediationToBeAutoApplied(remName, remNamespace string, pool *mcfgv1.MachineConfigPool) error {
	rem := &compv1alpha1.ComplianceRemediation{}
	var lastErr error
	timeouterr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		lastErr = f.Client.Get(context.TODO(), types.NamespacedName{Name: remName, Namespace: remNamespace}, rem)
		if apierrors.IsNotFound(lastErr) {
			log.Printf("waiting for remediation %s to become available", remName)
			return false, nil
		}
		if lastErr != nil {
			log.Printf("failed to get remediation, retrying: %s\n", lastErr)
			return false, nil
		}
		log.Printf("found remediation: %s", remName)
		return true, nil
	})
	if lastErr != nil {
		return fmt.Errorf("failed to get remediation %s: %s", remName, lastErr)
	}
	if timeouterr != nil {
		return fmt.Errorf("timed out getting remediation %s: %s", remName, timeouterr)
	}

	preNoop := func() error {
		return nil
	}

	predicate := func(pool *mcfgv1.MachineConfigPool) (bool, error) {
		// When checking if a MC is applied to a pool, we can't check the pool status
		// when the pool is paused..
		source := pool.Status.Configuration.Source
		if pool.Spec.Paused == true {
			source = pool.Spec.Configuration.Source
		}

		for _, mc := range source {
			if mc.Name == rem.GetMcName() {
				// When applying a remediation, check that the MC *is* in the pool
				log.Printf("Remediation %s present in pool %s, returning true", mc.Name, pool.Name)
				return true, nil
			}
		}

		log.Printf("Remediation %s not present in pool %s, returning false", rem.GetMcName(), pool.Name)
		return false, nil
	}

	err := f.WaitForMachinePoolUpdate(pool.Name, preNoop, predicate, pool)
	if err != nil {
		return fmt.Errorf("failed waiting for pool to update after applying MC: %v", err)
	}

	log.Printf("machines updated with remediation")
	err = f.WaitForNodesToBeReady()
	if err != nil {
		return err
	}

	fmt.Printf("Remediation applied to machines and machines rebooted\n")
	return nil
}

type machineConfigActionFunc func() error
type poolPredicate func(pool *mcfgv1.MachineConfigPool) (bool, error)

// waitForMachinePoolUpdate retrieves the original version of a MCP, then performs an
// action passed in as a parameter and then waits until a MCP passes a predicate
// If a pool is already given (poolPre), that will be used to check the previous state of the pool.
func (f *Framework) WaitForMachinePoolUpdate(name string, action machineConfigActionFunc, predicate poolPredicate, poolPre *mcfgv1.MachineConfigPool) error {
	if poolPre == nil {
		// initialize empty pool if it wasn't already given
		poolPre = &mcfgv1.MachineConfigPool{}
		err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name}, poolPre)
		if err != nil {
			return fmt.Errorf("could not find pool before update: %s", err)
		}
	}
	log.Printf("Pre-update, MC Pool %s has generation %d\n", poolPre.Name, poolPre.Status.ObservedGeneration)

	err := action()
	if err != nil {
		return fmt.Errorf("action failed: %s", err)
	}

	err = wait.PollImmediate(machineOperationRetryInterval, machineOperationTimeout, func() (bool, error) {
		pool := &mcfgv1.MachineConfigPool{}
		err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name}, pool)
		if err != nil {
			// even not found is a hard error here
			log.Printf("could not find the pool after update: %s\n", err)
			return false, err
		}

		ok, err := predicate(pool)
		if err != nil {
			log.Printf("predicate failed %s", err)
			return false, err
		}

		if !ok {
			log.Printf("predicate not true yet, waiting")
			return false, nil
		}

		log.Printf("will check for update, Gen: %d, previous %d updated %d/%d unavailable %d",
			pool.Status.ObservedGeneration, poolPre.Status.ObservedGeneration,
			pool.Status.UpdatedMachineCount, pool.Status.MachineCount,
			pool.Status.UnavailableMachineCount)

		// Check if the pool has finished updating yet. If the pool was paused, we just check that
		// the generation was increased and wait for machines to reboot separately
		if (pool.Status.ObservedGeneration != poolPre.Status.ObservedGeneration) &&
			pool.Spec.Paused == true || ((pool.Status.UpdatedMachineCount == pool.Status.MachineCount) &&
			(pool.Status.UnavailableMachineCount == 0)) {
			log.Printf("pool updated successfully")
			return true, nil
		}

		log.Printf("pool has not updated yet. Gen: %d, expected %d updated %d/%d unavailable %d",
			pool.Status.ObservedGeneration, poolPre.Status.ObservedGeneration,
			pool.Status.UpdatedMachineCount, pool.Status.MachineCount,
			pool.Status.UnavailableMachineCount)
		return false, nil
	})

	if err != nil {
		return err
	}

	return nil
}

// waitForNodesToBeReady waits until all the nodes in the cluster have
// reached the expected machineConfig.
func (f *Framework) WaitForNodesToBeReady() error {
	err := wait.PollImmediate(machineOperationRetryInterval, machineOperationTimeout, func() (bool, error) {
		var nodes core.NodeList

		f.Client.List(context.TODO(), &nodes, &dynclient.ListOptions{})
		for _, node := range nodes.Items {
			log.Printf("node %s has config %s, desired config %s state %s\n",
				node.Name,
				node.Annotations["machineconfiguration.openshift.io/currentConfig"],
				node.Annotations["machineconfiguration.openshift.io/desiredConfig"],
				node.Annotations["machineconfiguration.openshift.io/state"])

			if (node.Annotations["machineconfiguration.openshift.io/currentConfig"] != node.Annotations["machineconfiguration.openshift.io/desiredConfig"]) ||
				(node.Annotations["machineconfiguration.openshift.io/state"] != "Done") {
				log.Printf("node %s still updating", node.Name)
				return false, nil
			}
			log.Printf("node %s was updated", node.Name)
		}

		log.Printf("all machines updated")
		return true, nil
	})

	if err != nil {
		return fmt.Errorf("failed waiting for nodes to be ready: %s", err)
	}
	return nil
}

func (f *Framework) ReRunScan(scanName, namespace string) error {
	scanKey := types.NamespacedName{Name: scanName, Namespace: namespace}
	err := backoff.Retry(func() error {
		foundScan := &compv1alpha1.ComplianceScan{}
		geterr := f.Client.Get(context.TODO(), scanKey, foundScan)
		if geterr != nil {
			return geterr
		}

		scapCopy := foundScan.DeepCopy()
		if scapCopy.Annotations == nil {
			scapCopy.Annotations = make(map[string]string)
		}
		scapCopy.Annotations[compv1alpha1.ComplianceScanRescanAnnotation] = ""
		return f.Client.Update(context.TODO(), scapCopy)
	}, defaultBackoff)

	if err != nil {
		return fmt.Errorf("failed to annotate scan %s for rerun: %w", scanName, err)
	}

	log.Printf("rerunning scan %s", scanName)
	return nil
}

func (f *Framework) PauseMachinePool(poolName string) error {
	return f.modMachinePoolPause(poolName, true)
}

func (f *Framework) ResumeMachinePool(poolName string) error {
	return f.modMachinePoolPause(poolName, false)
}

func (f *Framework) modMachinePoolPause(poolName string, pause bool) error {
	pool := &mcfgv1.MachineConfigPool{}
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: poolName}, pool)
	if err != nil {
		return fmt.Errorf("could not find pool to modify: %s", err)
	}

	poolCopy := pool.DeepCopy()
	poolCopy.Spec.Paused = pause
	err = f.Client.Update(context.TODO(), poolCopy)
	if err != nil {
		return fmt.Errorf("failed to update pool: %s", err)
	}

	return nil
}

func (f *Framework) ApplyRemediationAndCheck(namespace, name, pool string) error {
	rem := &compv1alpha1.ComplianceRemediation{}
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, rem)
	if err != nil {
		return err
	}
	log.Printf("remediation %s found\n", name)

	applyRemediation := func() error {
		rem.Spec.Apply = true
		err = f.Client.Update(context.TODO(), rem)
		if err != nil {
			return fmt.Errorf("failed to apply remediation: %s", err)
		}
		log.Printf("applied remediation %s\n", name)
		return nil
	}

	predicate := func(pool *mcfgv1.MachineConfigPool) (bool, error) {
		// When checking if a MC is applied to a pool, we can't check the pool status
		// when the pool is paused..
		source := pool.Status.Configuration.Source
		if pool.Spec.Paused == true {
			source = pool.Spec.Configuration.Source
		}

		for _, mc := range source {
			if mc.Name == rem.GetMcName() {
				// When applying a remediation, check that the MC *is* in the pool
				log.Printf("remediation %s present in pool %s, returning true\n", mc.Name, pool.Name)
				return true, nil
			}
		}

		log.Printf("remediation %s not present in pool %s, returning false\n", rem.GetMcName(), pool.Name)
		return false, nil
	}

	err = f.WaitForMachinePoolUpdate(pool, applyRemediation, predicate, nil)
	if err != nil {
		return fmt.Errorf("failed to wait for pool to update after applying MC: %v", err)
	}

	log.Printf("machines updated with remediation")
	return nil
}

func (f *Framework) UnApplyRemediationAndCheck(namespace, name, pool string) error {
	rem := &compv1alpha1.ComplianceRemediation{}
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, rem)
	if err != nil {
		return err
	}
	log.Printf("remediation found\n")

	applyRemediation := func() error {
		rem.Spec.Apply = false
		err = f.Client.Update(context.TODO(), rem)
		if err != nil {
			return fmt.Errorf("cannot apply remediation: %s", err)
		}
		log.Printf("remediation applied\n")
		return nil
	}

	predicate := func(pool *mcfgv1.MachineConfigPool) (bool, error) {
		// We want to check that the MC created by the operator went away. Let's
		// poll the pool until we no longer see the remediation in the status
		for _, mc := range pool.Status.Configuration.Source {
			if mc.Name == rem.GetMcName() {
				log.Printf("remediation %s present in pool %s, returning false\n", mc.Name, pool.Name)
				return false, nil
			}
		}

		log.Printf("Remediation %s not present in pool %s, returning true\n", rem.GetMcName(), pool.Name)
		return true, nil
	}

	err = f.WaitForMachinePoolUpdate(pool, applyRemediation, predicate, nil)
	if err != nil {
		return fmt.Errorf("failed to wait for pool to update after applying MachineConfig: %v", err)
	}

	log.Printf("machines updated with remediation\n")
	return nil
}

func (f *Framework) runPod(namespace string, podToRun *core.Pod) (*core.Pod, error) {
	pod, err := f.KubeClient.CoreV1().Pods(namespace).Create(context.TODO(), podToRun, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	if err := waitForPod(initContainerCompleted(f.KubeClient, pod.Name, namespace)); err != nil {
		return nil, err
	}

	return pod, nil
}

func waitForPod(podCallback wait.ConditionFunc) error {
	return wait.PollImmediate(RetryInterval, Timeout, podCallback)
}

func initContainerCompleted(c kubernetes.Interface, name, namespace string) wait.ConditionFunc {
	return func() (bool, error) {
		pod, err := c.CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
		if apierrors.IsNotFound(err) {
			log.Printf("pod %s not found yet\n", name)
			return false, nil
		}

		for _, initStatus := range pod.Status.InitContainerStatuses {
			// the init container must have passed the readiness probe
			if initStatus.Ready == false {
				log.Println("init container not ready yet")
				return false, nil
			}

			// the init container must have terminated
			if initStatus.State.Terminated == nil {
				log.Println("init container terminated")
				return false, nil
			}

			if initStatus.State.Terminated.ExitCode != 0 {
				return true, errors.New("the init container failed")
			} else {
				fmt.Printf("init container in pod %s has finished\n", name)
				return true, nil
			}
		}

		fmt.Printf("init container in pod %s not finished yet\n", name)
		return false, nil
	}
}

// createAndRemoveEtcSecurettyOnNode creates a pod that creates the file /etc/securetty on node, returns the pod
// object for the caller to delete at which point the pod, before exiting, removes the file
func (f *Framework) CreateAndRemoveEtcSecurettyOnNode(namespace, name, nodeName string) (*core.Pod, error) {
	return f.runPod(namespace, createAndRemoveEtcSecurettyPod(namespace, name, nodeName))
}

func createAndRemoveEtcSecurettyPod(namespace, name, nodeName string) *core.Pod {
	return privCommandTuplePodOnHost(namespace, name, nodeName, "touch /hostroot/etc/securetty", []string{"rm", "-f", "/hostroot/etc/securetty"})
}

// privCommandTuplePodOnHost returns a pod that calls commandPre in an init container, then sleeps for an hour
// and registers commandPost to be run in a PreStop handler.
func privCommandTuplePodOnHost(namespace, name, nodeName, commandPre string, commandPost []string) *core.Pod {
	runAs := int64(0)
	priv := true

	return &core.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: core.PodSpec{
			InitContainers: []core.Container{
				{
					Name:    name + "-init",
					Image:   "busybox",
					Command: []string{"/bin/sh"},
					Args:    []string{"-c", commandPre},
					VolumeMounts: []core.VolumeMount{
						{
							Name:      "hostroot",
							MountPath: "/hostroot",
						},
					},
					SecurityContext: &core.SecurityContext{
						Privileged: &priv,
						RunAsUser:  &runAs,
					},
				},
			},
			Containers: []core.Container{
				{
					Name:    name,
					Image:   "busybox",
					Command: []string{"/bin/sh"},
					Args:    []string{"-c", "sleep 3600"},
					VolumeMounts: []core.VolumeMount{
						{
							Name:      "hostroot",
							MountPath: "/hostroot",
						},
					},
					SecurityContext: &core.SecurityContext{
						Privileged: &priv,
						RunAsUser:  &runAs,
					},
					Lifecycle: &core.Lifecycle{
						PreStop: &core.LifecycleHandler{
							Exec: &core.ExecAction{Command: commandPost},
						},
					},
				},
			},
			Volumes: []core.Volume{
				{
					Name: "hostroot",
					VolumeSource: core.VolumeSource{
						HostPath: &core.HostPathVolumeSource{
							Path: "/",
						},
					},
				},
			},
			RestartPolicy: "Never",
			NodeSelector: map[string]string{
				core.LabelHostname: nodeName,
			},
			ServiceAccountName: "resultscollector",
		},
	}
}

func (f *Framework) UpdateSuiteContentImage(newImg, suiteName, suiteNs string) error {
	var lastErr error
	timeoutErr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		suite := &compv1alpha1.ComplianceSuite{}
		// Now update the suite with a different image that contains different remediations
		lastErr = f.Client.Get(context.TODO(), types.NamespacedName{Name: suiteName, Namespace: suiteNs}, suite)
		if lastErr != nil {
			log.Printf("got error while trying to get suite %s. Retrying... - %s\n", suiteName, lastErr)
			return false, nil
		}
		modSuite := suite.DeepCopy()
		modSuite.Spec.Scans[0].ContentImage = newImg
		lastErr = f.Client.Update(context.TODO(), modSuite)
		if lastErr != nil {
			log.Printf("got error while trying to update suite %s. Retrying... - %s\n", suiteName, lastErr)
			return false, nil
		}
		return true, nil
	})

	if timeoutErr != nil {
		return fmt.Errorf("couldn't update suite's content image. Timed out: %w", timeoutErr)
	}
	if lastErr != nil {
		return fmt.Errorf("couldn't update suite's content image. Errored out: %w", lastErr)
	}
	return nil
}

func (f *Framework) AssertRemediationIsObsolete(namespace, name string) error {
	err, isObsolete := f.remediationIsObsolete(namespace, name)
	if err != nil {
		return err
	}
	if !isObsolete {
		return fmt.Errorf("expected remediation %s to be obsolete", name)
	}
	return nil
}

func (f *Framework) AssertRemediationIsCurrent(namespace, name string) error {
	err, isObsolete := f.remediationIsObsolete(namespace, name)
	if err != nil {
		return err
	}
	if isObsolete {
		return fmt.Errorf("expected remediation %s to be current", name)
	}
	return nil
}

func (f *Framework) remediationIsObsolete(namespace, name string) (error, bool) {
	rem := &compv1alpha1.ComplianceRemediation{}
	var lastErr error
	timeouterr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		lastErr := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, rem)
		if lastErr != nil {
			return false, nil
		}
		return true, nil
	})
	if lastErr != nil {
		return fmt.Errorf("got error trying to get remediation's obsolescence: %w", lastErr), false
	}
	if timeouterr != nil {
		return fmt.Errorf("timed out trying to get remediation's obsolescence: %w", lastErr), false
	}
	log.Printf("remediation %s found\n", name)

	if rem.Status.ApplicationState == compv1alpha1.RemediationOutdated && rem.Spec.Outdated.Object != nil {
		return nil, true
	}

	return nil, false
}

func (f *Framework) RemoveObsoleteRemediationAndCheck(namespace, name, renderedMcName, pool string) error {
	rem := &compv1alpha1.ComplianceRemediation{}
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, rem)
	if err != nil {
		return err
	}
	log.Printf("remediation %s found", name)

	removeObsoleteContents := func() error {
		log.Printf("pre-update %v", rem.Status)
		remCopy := rem.DeepCopy()
		remCopy.Spec.Apply = true
		remCopy.Spec.Outdated.Object = nil
		err = f.Client.Update(context.TODO(), remCopy)
		if err != nil {
			return err
		}
		log.Println("obsolete data removed")

		rem2 := &compv1alpha1.ComplianceRemediation{}
		f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, rem2)
		log.Printf("post-update %v", rem2.Status)

		return nil
	}

	// Get the MachineConfigPool before the remediation has been made current
	// This way, we can check that it changed without race-conditions
	poolBeforeRemediation := &mcfgv1.MachineConfigPool{}
	err = f.Client.Get(context.TODO(), types.NamespacedName{Name: TestPoolName}, poolBeforeRemediation)
	if err != nil {
		return err
	}

	obsoleteMc := &mcfgv1.MachineConfig{}
	err = f.Client.Get(context.TODO(), types.NamespacedName{Name: renderedMcName}, obsoleteMc)
	if err != nil {
		return err
	}

	predicate := func(pool *mcfgv1.MachineConfigPool) (bool, error) {
		// make sure the composite remediation has been re-rendered
		currentMc := &mcfgv1.MachineConfig{}
		err = f.Client.Get(context.TODO(), types.NamespacedName{Name: renderedMcName}, currentMc)
		if err != nil {
			return false, err
		}

		if currentMc.Generation == obsoleteMc.Generation {
			log.Printf("MachineConfig %s still has generation %d, looping", renderedMcName, currentMc.Generation)
			return false, nil
		}
		log.Printf("MachineConfig has been re-rendered from %d to %d", obsoleteMc.Generation, currentMc.Generation)
		return true, nil
	}

	err = f.WaitForMachinePoolUpdate(pool, removeObsoleteContents, predicate, poolBeforeRemediation)
	if err != nil {
		return fmt.Errorf("failed to wait for pool to update after applying MC: %v", err)
	}

	fmt.Printf("machines updated with remediation that is no longer obsolete")
	return nil
}

func (f *Framework) WaitForGenericRemediationToBeAutoApplied(remName, remNamespace string) error {
	rem := &compv1alpha1.ComplianceRemediation{}
	var lastErr error
	timeouterr := wait.Poll(RetryInterval, Timeout, func() (bool, error) {
		lastErr = f.Client.Get(context.TODO(), types.NamespacedName{Name: remName, Namespace: remNamespace}, rem)
		if apierrors.IsNotFound(lastErr) {
			log.Printf("waiting for availability of %s remediation\n", remName)
			return false, nil
		}
		if lastErr != nil {
			log.Printf("retrying. Got error: %v\n", lastErr)
			return false, nil
		}
		log.Printf("found remediation: %s\n", remName)
		if rem.Status.ApplicationState == compv1alpha1.RemediationNotApplied || rem.Status.ApplicationState == compv1alpha1.RemediationPending {
			log.Printf("retrying. remediation not yet applied. Remediation Name: %s, ApplicationState: %s\n", remName, rem.Status.ApplicationState)
		}
		// wait for the remediation to get applied
		time.Sleep(5 * time.Second)
		return true, nil
	})
	if lastErr != nil {
		return fmt.Errorf("failed getting remediation before it was applied: %s", lastErr)
	}
	if timeouterr != nil {
		return fmt.Errorf("timed out waiting for remediation to be applied: %s", timeouterr)
	}
	log.Printf("machines updated with remediation")
	err := f.WaitForNodesToBeReady()
	if err != nil {
		return err
	}
	return nil
}

func (f *Framework) AssertCronJobIsSuspended(name string) error {
	job := &batchv1.CronJob{}
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: f.OperatorNamespace}, job)
	if err != nil {
		return err
	}
	if !*job.Spec.Suspend {
		msg := fmt.Sprintf("Expected CronJob %s to be suspended", name)
		return errors.New(msg)
	}
	log.Printf("CronJob %s is suspended", name)
	return nil
}

func (f *Framework) AssertCronJobIsNotSuspended(name string) error {
	job := &batchv1.CronJob{}
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: f.OperatorNamespace}, job)
	if err != nil {
		return err
	}
	if *job.Spec.Suspend {
		msg := fmt.Sprintf("Expected CronJob %s to be active", name)
		return errors.New(msg)
	}
	log.Printf("CronJob %s is active", name)
	return nil
}

func (f *Framework) AssertScanDoesNotContainCheck(scanName, checkName, namespace string) error {
	var getCheck compv1alpha1.ComplianceCheckResult
	err := f.Client.Get(context.TODO(), types.NamespacedName{Name: checkName, Namespace: namespace}, &getCheck)
	if apierrors.IsNotFound(err) {
		// The check doesn't exist at all, which means it also doesn't exist in *any* scans
		log.Printf("didn't find ComplianceCheckResult %s", checkName)
		return nil
	} else if err != nil {
		return err
	}
	// Make sure the check we found actually belongs to the scan we're given. This is extra
	// validation in case the same check is used across other scans.
	if getCheck.Labels[compv1alpha1.ComplianceScanLabel] == scanName {
		return fmt.Errorf("found ComplianceCheckResult %s in ComplianceScan %s", checkName, scanName)
	}
	return nil
}

func (f *Framework) assertRuleType(ruleName, namespace, expectedRuleType string) error {
	var r compv1alpha1.Rule
	key := types.NamespacedName{Name: ruleName, Namespace: namespace}
	err := f.Client.Get(context.TODO(), key, &r)
	if err != nil {
		return err
	}
	if r.CheckType != expectedRuleType {
		m := fmt.Sprintf("expected rule %s to be %s type, found %s type instead", r.Name, expectedRuleType, r.CheckType)
		return errors.New(m)
	}
	return nil
}

func (f *Framework) AssertRuleIsNodeType(ruleName, namespace string) error {
	return f.assertRuleType(ruleName, namespace, "Node")
}

func (f *Framework) AssertRuleIsPlatformType(ruleName, namespace string) error {
	return f.assertRuleType(ruleName, namespace, "Platform")
}
