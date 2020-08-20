package compliancescan

import (
	"context"

	"github.com/go-logr/logr"
	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/openshift/compliance-operator/pkg/controller/common"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	rawStorageAllocationErrorPrefix = "Couldn't allocate raw storage: "
)

var (
	defaultAccessMode = []corev1.PersistentVolumeAccessMode{"ReadWriteOnce"}
)

// handles the necessary things to store and expose the raw results from the scan. This implies
// that the PVC gets created, and, if necessary, the scan instance will get updated too.
// Returns whether the reconcile loop should continue or not, and an error if encountered.
func (r *ReconcileComplianceScan) handleRawResultsForScan(instance *compv1alpha1.ComplianceScan, logger logr.Logger) (bool, error) {
	// Create PVC
	pvc := getPVCForScan(instance)
	logger.Info("Creating PVC for scan", "PersistentVolumeClaim.Name", pvc.Name, "PersistentVolumeClaim.Namespace", pvc.Namespace)
	if err := r.client.Create(context.TODO(), pvc); err != nil && !errors.IsAlreadyExists(err) {
		// Handle resource limit issues
		if errors.IsForbidden(err) {
			scanCopy := instance.DeepCopy()
			scanCopy.Status.Phase = compv1alpha1.PhaseDone
			scanCopy.Status.Result = compv1alpha1.ResultError
			scanCopy.Status.ErrorMessage = rawStorageAllocationErrorPrefix + err.Error()
			return false, r.client.Status().Update(context.TODO(), scanCopy)
		}
		return false, err
	}
	if instanceNeedsResultStorageReference(instance, pvc) {
		scanCopy := instance.DeepCopy()
		scanCopy.Status.ResultsStorage.Kind = pvc.Kind
		scanCopy.Status.ResultsStorage.APIVersion = pvc.APIVersion
		scanCopy.Status.ResultsStorage.Name = pvc.Name
		scanCopy.Status.ResultsStorage.Namespace = pvc.Namespace
		logger.Info("Updating scan status with raw result reference")
		return false, r.client.Status().Update(context.TODO(), scanCopy)
	}
	return true, nil
}

func (r *ReconcileComplianceScan) deleteRawResultsForScan(instance *compv1alpha1.ComplianceScan) error {
	pvc := getPVCForScan(instance)
	if err := r.client.Delete(context.TODO(), pvc); err != nil && !errors.IsNotFound(err) {
		return err
	}
	return nil
}

func getPVCForScan(instance *compv1alpha1.ComplianceScan) *corev1.PersistentVolumeClaim {
	storageSize := instance.Spec.RawResultStorage.Size
	if storageSize == "" {
		storageSize = compv1alpha1.DefaultRawStorageSize
	}
	accessModes := instance.Spec.RawResultStorage.PVAccessModes
	if len(accessModes) == 0 {
		accessModes = defaultAccessMode
	}

	return &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "PersistentVolumeClaim",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      getPVCForScanName(instance.Name),
			Namespace: common.GetComplianceOperatorNamespace(),
			Labels: map[string]string{
				compv1alpha1.ComplianceScanLabel: instance.Name,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: instance.Spec.RawResultStorage.StorageClassName,
			AccessModes:      accessModes,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(storageSize),
				},
			},
		},
	}
}

func instanceNeedsResultStorageReference(scan *compv1alpha1.ComplianceScan, pvc *corev1.PersistentVolumeClaim) bool {
	return scan.Status.ResultsStorage.Name != pvc.Name ||
		scan.Status.ResultsStorage.Namespace != pvc.Namespace
}

// GetPVCForScanName Get's the PVC name for a scan
func getPVCForScanName(scanName string) string {
	return scanName
}
