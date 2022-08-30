package e2eutil

import (
	"context"
	"testing"
	"time"

	test "github.com/ComplianceAsCode/compliance-operator/tests/e2e/framework"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// WaitForDeployment checks to see if a given deployment has a certain number of available replicas after a specified
// amount of time. If the deployment does not have the required number of replicas after 5 * retries seconds,
// the function returns an error. This can be used in multiple ways, like verifying that a required resource is ready
// before trying to use it, or to test. Failure handling, like simulated in SimulatePodFail.
func WaitForDeployment(t *testing.T, kubeclient kubernetes.Interface, namespace, name string, replicas int,
	retryInterval, timeout time.Duration) error {
	return waitForDeployment(t, kubeclient, namespace, name, replicas, retryInterval, timeout, false)
}

// WaitForOperatorDeployment has the same functionality as WaitForDeployment but will no wait for the deployment if the
// test was run with a locally run operator (--up-local flag)
func WaitForOperatorDeployment(t *testing.T, kubeclient kubernetes.Interface, namespace, name string, replicas int,
	retryInterval, timeout time.Duration) error {
	return waitForDeployment(t, kubeclient, namespace, name, replicas, retryInterval, timeout, true)
}

func waitForDeployment(t *testing.T, kubeclient kubernetes.Interface, namespace, name string, replicas int,
	retryInterval, timeout time.Duration, isOperator bool) error {
	if isOperator && test.Global.LocalOperator {
		t.Log("Operator is running locally; skip waitForDeployment")
		return nil
	}
	err := wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		deployment, err := kubeclient.AppsV1().Deployments(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				t.Logf("Waiting for availability of Deployment: %s in Namespace: %s \n", name, namespace)
				return false, nil
			}
			return false, err
		}

		if int(deployment.Status.AvailableReplicas) >= replicas {
			return true, nil
		}
		t.Logf("Waiting for full availability of %s deployment (%d/%d)\n", name,
			deployment.Status.AvailableReplicas, replicas)
		return false, nil
	})
	if err != nil {
		return err
	}
	t.Logf("Deployment available (%d/%d)\n", replicas, replicas)
	return nil
}

func WaitForDeletion(t *testing.T, dynclient client.Client, obj client.Object, retryInterval,
	timeout time.Duration) error {

	key := client.ObjectKeyFromObject(obj)

	kind := obj.GetObjectKind().GroupVersionKind().Kind
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := wait.Poll(retryInterval, timeout, func() (done bool, err error) {
		err = dynclient.Get(ctx, key, obj)
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		t.Logf("Waiting for %s %s to be deleted\n", kind, key)
		return false, nil
	})
	if err != nil {
		return err
	}
	t.Logf("%s %s was deleted\n", kind, key)
	return nil
}
