package ingress

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCandidateServiceBlocksLegacyMutationBeforeWrites(t *testing.T) {
	for _, marker := range []string{"activation", "bootstrap", "selector"} {
		t.Run(marker, func(t *testing.T) {
			for _, operation := range []string{"reconcile", "delete", "service-update", "service-delete"} {
				t.Run(operation, func(t *testing.T) {
					cfg := testAppConfig()
					service := serviceForApp(cfg, DefaultNamespace)
					switch marker {
					case "activation":
						service.Annotations[activationOperationAnnotation] = ""
					case "bootstrap":
						service.Annotations[initialCandidateRequestAnnotation] = "request"
					case "selector":
						service.Spec.Selector[candidateGenerationLabel] = "malformed"
					}
					client := fake.NewSimpleClientset(service, testReadyWorker())
					c := &Controller{namespace: DefaultNamespace, services: client.CoreV1().Services(DefaultNamespace), nodes: client.CoreV1().Nodes()}
					var err error
					switch operation {
					case "reconcile":
						err = c.Reconcile(context.Background(), cfg, nil, "")
					case "delete":
						err = c.Delete(context.Background(), cfg.Name)
					case "service-update":
						err = c.reconcileService(context.Background(), cfg)
					case "service-delete":
						err = c.deleteService(context.Background(), cfg.Name)
					}
					if !errors.Is(err, ErrCandidateManagedApp) {
						t.Fatalf("legacy mutation not rejected: %v", err)
					}
					for _, action := range client.Actions() {
						switch action.GetVerb() {
						case "create", "update", "patch", "delete":
							t.Fatalf("legacy mutation occurred: %s %s", action.GetVerb(), action.GetResource().Resource)
						}
					}
				})
			}
		})
	}
}

func TestUnmarkedServiceRemainsLegacy(t *testing.T) {
	service := &corev1.Service{}
	if candidateManagedService(service) {
		t.Fatal("legacy service classified as candidate")
	}
}
