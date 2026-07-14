package ingress

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	appOwnershipLabel = "deployer.io/app"
	managedByLabel    = "app.kubernetes.io/managed-by"
	managedByDeployer = "deployer"
)

func requireAppResourceOwnership(
	resourceKind string,
	resourceName string,
	appName string,
	labels map[string]string,
) error {
	if labels[appOwnershipLabel] != appName {
		return fmt.Errorf(
			"%s %q ownership conflict: expected label %s=%q, found %q",
			resourceKind,
			resourceName,
			appOwnershipLabel,
			appName,
			labels[appOwnershipLabel],
		)
	}
	if managedBy, exists := labels[managedByLabel]; exists && managedBy != managedByDeployer {
		return fmt.Errorf(
			"%s %q ownership conflict: expected label %s=%q, found %q",
			resourceKind,
			resourceName,
			managedByLabel,
			managedByDeployer,
			managedBy,
		)
	}
	return nil
}

func ownedDeleteOptions(object metav1.Object) metav1.DeleteOptions {
	options := metav1.DeleteOptions{}
	if uid := object.GetUID(); uid != "" {
		options.Preconditions = &metav1.Preconditions{UID: &uid}
	}
	return options
}
