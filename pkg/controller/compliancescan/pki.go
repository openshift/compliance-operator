package compliancescan

import (
	"context"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"

	compv1alpha1 "github.com/ComplianceAsCode/compliance-operator/pkg/apis/compliance/v1alpha1"
	"github.com/ComplianceAsCode/compliance-operator/pkg/controller/common"
)

func (r *ReconcileComplianceScan) handleRootCASecret(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	exist, err := secretExists(r.Client, RootCAPrefix+instance.Name, common.GetComplianceOperatorNamespace())
	if err != nil {
		return err
	}
	if exist {
		return nil
	}

	logger.Info("creating CA", "ComplianceScan.Name", instance.Name)
	secret, err := makeCASecret(instance, common.GetComplianceOperatorNamespace())
	if err != nil {
		return err
	}

	// Create the CA secret.
	err = r.Client.Create(context.TODO(), secret)
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	return nil
}

func (r *ReconcileComplianceScan) handleResultServerSecret(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	exist, err := secretExists(r.Client, ServerCertPrefix+instance.Name, common.GetComplianceOperatorNamespace())
	if err != nil {
		return err
	}
	if exist {
		return nil
	}

	logger.Info("creating server cert", "ComplianceScan.Name", instance.Name)
	secret, err := makeServerCertSecret(r.Client, instance, common.GetComplianceOperatorNamespace())
	if err != nil {
		return err
	}

	// Create the server cert secret.
	err = r.Client.Create(context.TODO(), secret)
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (r *ReconcileComplianceScan) handleResultClientSecret(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	exist, err := secretExists(r.Client, ClientCertPrefix+instance.Name, common.GetComplianceOperatorNamespace())
	if err != nil {
		return err
	}
	if exist {
		return nil
	}

	logger.Info("creating Client cert", "ComplianceScan.Name", instance.Name)
	secret, err := makeClientCertSecret(r.Client, instance, common.GetComplianceOperatorNamespace())
	if err != nil {
		return err
	}

	// Create the Client cert secret.
	err = r.Client.Create(context.TODO(), secret)
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	return nil
}

func (r *ReconcileComplianceScan) deleteRootCASecret(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	logger.Info("deleting CA", "ComplianceScan.Name", instance.Name)
	ns := common.GetComplianceOperatorNamespace()
	secret := certSecret(getCASecretName(instance), ns, []byte{}, []byte{}, []byte{})
	return r.deleteSecret(secret)
}

func (r *ReconcileComplianceScan) deleteResultServerSecret(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	logger.Info("deleting server cert", "ComplianceScan.Name", instance.Name)
	ns := common.GetComplianceOperatorNamespace()
	secret := certSecret(getServerCertSecretName(instance), ns, []byte{}, []byte{}, []byte{})
	return r.deleteSecret(secret)
}

func (r *ReconcileComplianceScan) deleteResultClientSecret(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	logger.Info("deleting Client cert", "ComplianceScan.Name", instance.Name)
	ns := common.GetComplianceOperatorNamespace()
	secret := certSecret(getClientCertSecretName(instance), ns, []byte{}, []byte{}, []byte{})
	return r.deleteSecret(secret)
}

func (r *ReconcileComplianceScan) deleteSecret(secret *corev1.Secret) error {
	// Delete the Client cert secret.
	err := r.Client.Delete(context.TODO(), secret)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	return nil
}
