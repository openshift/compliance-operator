package compliancescan

import (
	"context"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	compv1alpha1 "github.com/openshift/compliance-operator/pkg/apis/compliance/v1alpha1"
)

func (r *ReconcileComplianceScan) handleRootCASecret(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	exist, err := secretExists(r.client, RootCAPrefix+instance.Name, instance.GetNamespace())
	if err != nil {
		return err
	}
	if exist {
		return nil
	}

	logger.Info("creating CA", "ComplianceScan.Name", instance.Name)
	secret, err := makeCASecret(instance, instance.GetNamespace())
	if err != nil {
		return err
	}

	if err = controllerutil.SetControllerReference(instance, secret, r.scheme); err != nil {
		return err
	}

	// Create the CA secret.
	err = r.client.Create(context.TODO(), secret)
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	return nil
}

func (r *ReconcileComplianceScan) handleResultServerSecret(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	exist, err := secretExists(r.client, ServerCertPrefix+instance.Name, instance.GetNamespace())
	if err != nil {
		return err
	}
	if exist {
		return nil
	}

	logger.Info("creating server cert", "ComplianceScan.Name", instance.Name)
	secret, err := makeServerCertSecret(r.client, instance, instance.GetNamespace())
	if err != nil {
		return err
	}

	if err = controllerutil.SetControllerReference(instance, secret, r.scheme); err != nil {
		return err
	}

	// Create the server cert secret.
	err = r.client.Create(context.TODO(), secret)
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (r *ReconcileComplianceScan) handleResultClientSecret(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	exist, err := secretExists(r.client, ClientCertPrefix+instance.Name, instance.GetNamespace())
	if err != nil {
		return err
	}
	if exist {
		return nil
	}

	logger.Info("creating client cert", "ComplianceScan.Name", instance.Name)
	secret, err := makeClientCertSecret(r.client, instance, instance.GetNamespace())
	if err != nil {
		return err
	}

	if err = controllerutil.SetControllerReference(instance, secret, r.scheme); err != nil {
		return err
	}

	// Create the client cert secret.
	err = r.client.Create(context.TODO(), secret)
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	return nil
}

func (r *ReconcileComplianceScan) deleteRootCASecret(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	logger.Info("deleting CA", "ComplianceScan.Name", instance.Name)
	ns := instance.GetNamespace()
	secret := certSecret(getCASecretName(instance), ns, []byte{}, []byte{}, []byte{})
	return r.deleteSecret(secret)
}

func (r *ReconcileComplianceScan) deleteResultServerSecret(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	logger.Info("deleting server cert", "ComplianceScan.Name", instance.Name)
	ns := instance.GetNamespace()
	secret := certSecret(getServerCertSecretName(instance), ns, []byte{}, []byte{}, []byte{})
	return r.deleteSecret(secret)
}

func (r *ReconcileComplianceScan) deleteResultClientSecret(instance *compv1alpha1.ComplianceScan, logger logr.Logger) error {
	logger.Info("deleting client cert", "ComplianceScan.Name", instance.Name)
	ns := instance.GetNamespace()
	secret := certSecret(getClientCertSecretName(instance), ns, []byte{}, []byte{}, []byte{})
	return r.deleteSecret(secret)
}

func (r *ReconcileComplianceScan) deleteSecret(secret *corev1.Secret) error {
	// Delete the client cert secret.
	err := r.client.Delete(context.TODO(), secret)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	return nil
}
