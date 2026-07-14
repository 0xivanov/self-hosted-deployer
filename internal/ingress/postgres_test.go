package ingress

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	batchv1 "k8s.io/api/batch/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestPostgresClusterForAppExactManifest(t *testing.T) {
	cfg := testPostgresAppConfig(appconfig.PostgresConnectionModeManaged)

	got := postgresClusterForApp(cfg, DefaultNamespace)
	want := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "postgresql.cnpg.io/v1",
		"kind":       "Cluster",
		"metadata": map[string]any{
			"name":      "my-api-db",
			"namespace": DefaultNamespace,
			"labels": map[string]any{
				"app.kubernetes.io/name":       "my-api-db",
				"app.kubernetes.io/instance":   "my-api-db",
				"app.kubernetes.io/component":  "database",
				"app.kubernetes.io/managed-by": "deployer",
				"deployer.io/database-for":     "my-api",
				"deployer.io/retention-policy": "retain",
			},
		},
		"spec": map[string]any{
			"instances": int64(3),
			"imageName": testPostgresImage,
			"bootstrap": map[string]any{
				"initdb": map[string]any{
					"database":      "money_manager",
					"owner":         "money_manager",
					"dataChecksums": true,
				},
			},
			"affinity": map[string]any{
				"enablePodAntiAffinity": true,
				"topologyKey":           "kubernetes.io/hostname",
				"podAntiAffinityType":   "required",
				"nodeSelector": map[string]any{
					"kubernetes.io/arch": "arm64",
				},
			},
			"storage": map[string]any{
				"size":         "20Gi",
				"storageClass": "local-path",
			},
			"postgresql": map[string]any{
				"parameters": map[string]any{
					"password_encryption": "scram-sha-256",
				},
				"pg_hba": []any{
					"hostnossl all all all reject",
					"hostssl money_manager money_manager all scram-sha-256",
				},
				"synchronous": map[string]any{
					"method":         "any",
					"number":         int64(1),
					"dataDurability": "required",
					"failoverQuorum": true,
				},
			},
		},
	}}

	if !reflect.DeepEqual(got.Object, want.Object) {
		t.Fatalf("unexpected PostgreSQL Cluster manifest:\n got: %#v\nwant: %#v", got.Object, want.Object)
	}
	if _, exists := got.GetLabels()["deployer.io/app"]; exists {
		t.Fatalf("database labels must not use deployer.io/app: %#v", got.GetLabels())
	}
}

func TestControllerCreatesAndUpdatesPostgresPreservingMetadata(t *testing.T) {
	cfg := testPostgresAppConfig(appconfig.PostgresConnectionModeManaged)
	existing := postgresClusterForApp(cfg, DefaultNamespace)
	existing.SetResourceVersion("17")
	existing.SetUID(types.UID("cluster-uid"))
	existing.SetGeneration(4)
	existing.SetAnnotations(map[string]string{"cnpg.io/reconciliationLoop": "enabled"})
	existing.SetFinalizers([]string{"postgresql.cnpg.io/operator"})
	existing.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "v1",
		Kind:       "ConfigMap",
		Name:       "database-owner",
		UID:        types.UID("owner-uid"),
	}})
	labels := existing.GetLabels()
	labels["ops.example.com/tier"] = "critical"
	existing.SetLabels(labels)
	existing.Object["status"] = map[string]any{"phase": "Cluster in healthy state"}

	dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme(), existing)
	clientset := fake.NewSimpleClientset(postgresReadyNodes(3, "arm64")...)
	controller := testPostgresController(clientset, dynamicClient)
	if err := controller.reconcilePostgres(context.Background(), cfg); err != nil {
		t.Fatalf("update PostgreSQL Cluster: %v", err)
	}
	got, err := controller.databases.Get(context.Background(), "my-api-db", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated PostgreSQL Cluster: %v", err)
	}
	image, _, _ := unstructured.NestedString(got.Object, "spec", "imageName")
	if image != cfg.Database.Postgres.Image {
		t.Fatalf("got image %q, want %q", image, cfg.Database.Postgres.Image)
	}
	storageSize, _, _ := unstructured.NestedString(got.Object, "spec", "storage", "size")
	if storageSize != cfg.Database.Postgres.Storage.Size {
		t.Fatalf("expected storage size %q, got %q", cfg.Database.Postgres.Storage.Size, storageSize)
	}
	if got.GetResourceVersion() != "17" || got.GetUID() != types.UID("cluster-uid") || got.GetGeneration() != 4 {
		t.Fatalf("server metadata was not preserved: %#v", got.Object["metadata"])
	}
	if got.GetAnnotations()["cnpg.io/reconciliationLoop"] != "enabled" ||
		got.GetLabels()["ops.example.com/tier"] != "critical" ||
		!reflect.DeepEqual(got.GetFinalizers(), existing.GetFinalizers()) ||
		!reflect.DeepEqual(got.GetOwnerReferences(), existing.GetOwnerReferences()) {
		t.Fatalf("custom metadata was not preserved: %#v", got.Object["metadata"])
	}
	phase, _, _ := unstructured.NestedString(got.Object, "status", "phase")
	if phase != "Cluster in healthy state" {
		t.Fatalf("status was not preserved, got %q", phase)
	}
}

func TestControllerUpdatePreservesUnownedPostgresSpec(t *testing.T) {
	cfg := testPostgresAppConfig(appconfig.PostgresConnectionModeManaged)
	existing := postgresClusterForApp(cfg, DefaultNamespace)
	spec := existing.Object["spec"].(map[string]any)
	spec["backup"] = map[string]any{"retentionPolicy": "30d"}
	spec["plugins"] = []any{map[string]any{
		"name":       "barman-cloud.cloudnative-pg.io",
		"parameters": map[string]any{"serverName": "money-manager"},
	}}
	spec["monitoring"] = map[string]any{"enablePodMonitor": true}
	spec["resources"] = map[string]any{"requests": map[string]any{"memory": "512Mi"}}
	spec["certificates"] = map[string]any{"serverTLSSecret": "custom-server-tls"}
	spec["nodeMaintenanceWindow"] = map[string]any{"inProgress": false, "reusePVC": true}
	affinity := spec["affinity"].(map[string]any)
	affinity["tolerations"] = []any{map[string]any{
		"key": "database", "operator": "Equal", "value": "true", "effect": "NoSchedule",
	}}
	nodeSelector := affinity["nodeSelector"].(map[string]any)
	nodeSelector["storage.example.com/class"] = "durable"
	storage := spec["storage"].(map[string]any)
	storage["resizeInUseVolumes"] = true
	postgresql := spec["postgresql"].(map[string]any)
	postgresql["parameters"] = map[string]any{"shared_buffers": "256MB"}
	postgresql["pg_hba"] = []any{
		"hostssl money_manager money_manager 10.0.0.0/8 scram-sha-256",
		"host all auditor 10.0.0.0/8 scram-sha-256",
	}

	dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme(), existing)
	clientset := fake.NewSimpleClientset(postgresReadyNodes(3, "arm64")...)
	controller := testPostgresController(clientset, dynamicClient)
	for range 2 {
		if err := controller.reconcilePostgres(context.Background(), cfg); err != nil {
			t.Fatalf("update PostgreSQL Cluster: %v", err)
		}
	}
	got, err := controller.databases.Get(context.Background(), "my-api-db", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated PostgreSQL Cluster: %v", err)
	}

	preserved := []struct {
		name   string
		fields []string
		want   any
	}{
		{name: "backup", fields: []string{"spec", "backup"}, want: spec["backup"]},
		{name: "plugins", fields: []string{"spec", "plugins"}, want: spec["plugins"]},
		{name: "monitoring", fields: []string{"spec", "monitoring"}, want: spec["monitoring"]},
		{name: "resources", fields: []string{"spec", "resources"}, want: spec["resources"]},
		{name: "certificates", fields: []string{"spec", "certificates"}, want: spec["certificates"]},
		{
			name:   "maintenance window",
			fields: []string{"spec", "nodeMaintenanceWindow"},
			want:   spec["nodeMaintenanceWindow"],
		},
		{
			name:   "custom node selector",
			fields: []string{"spec", "affinity", "nodeSelector", "storage.example.com/class"},
			want:   "durable",
		},
		{
			name:   "tolerations",
			fields: []string{"spec", "affinity", "tolerations"},
			want:   affinity["tolerations"],
		},
		{
			name:   "storage options",
			fields: []string{"spec", "storage", "resizeInUseVolumes"},
			want:   true,
		},
		{
			name:   "PostgreSQL parameters",
			fields: []string{"spec", "postgresql", "parameters"},
			want: map[string]any{
				"password_encryption": postgresSCRAMEncryption,
				"shared_buffers":      "256MB",
			},
		},
	}
	for _, field := range preserved {
		value, found, err := unstructured.NestedFieldCopy(got.Object, field.fields...)
		if err != nil || !found || !reflect.DeepEqual(value, field.want) {
			t.Fatalf("unowned %s was not preserved: got %#v found=%t err=%v", field.name, value, found, err)
		}
	}

	rules, found, err := unstructured.NestedSlice(got.Object, "spec", "postgresql", "pg_hba")
	wantRules := []any{
		postgresRejectNonTLSHBA,
		postgresApplicationSCRAMHBA("money_manager", "money_manager"),
		"hostssl money_manager money_manager 10.0.0.0/8 scram-sha-256",
		"host all auditor 10.0.0.0/8 scram-sha-256",
	}
	if err != nil || !found || !reflect.DeepEqual(rules, wantRules) {
		t.Fatalf("custom pg_hba rules were not preserved securely: got %#v found=%t err=%v", rules, found, err)
	}
}

func TestControllerRejectsUnsafePostgresUpdates(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*appconfig.Config)
		wantDetail string
	}{
		{
			name: "PostgreSQL major version",
			mutate: func(cfg *appconfig.Config) {
				cfg.Database.Postgres.Image = "ghcr.io/cloudnative-pg/postgresql:18.4@sha256:" + strings.Repeat("b", 64)
			},
			wantDetail: "immutable PostgreSQL image cannot change",
		},
		{
			name: "PostgreSQL same-major image",
			mutate: func(cfg *appconfig.Config) {
				cfg.Database.Postgres.Image = strings.TrimSuffix(testPostgresImage, "a") + "b"
			},
			wantDetail: "immutable PostgreSQL image cannot change",
		},
		{
			name: "placement architecture",
			mutate: func(cfg *appconfig.Config) {
				cfg.Placement.Arch = "linux/amd64"
			},
			wantDetail: "immutable placement architecture",
		},
		{
			name: "bootstrap database",
			mutate: func(cfg *appconfig.Config) {
				cfg.Database.Postgres.Database = "other_database"
			},
			wantDetail: "immutable bootstrap database",
		},
		{
			name: "bootstrap owner",
			mutate: func(cfg *appconfig.Config) {
				cfg.Database.Postgres.Owner = "other_owner"
			},
			wantDetail: "immutable bootstrap owner",
		},
		{
			name: "storage class",
			mutate: func(cfg *appconfig.Config) {
				cfg.Database.Postgres.Storage.StorageClass = "other-storage"
			},
			wantDetail: "immutable storage class",
		},
		{
			name: "storage decrease",
			mutate: func(cfg *appconfig.Config) {
				cfg.Database.Postgres.Storage.Size = "10Gi"
			},
			wantDetail: "immutable PostgreSQL storage size cannot change",
		},
		{
			name: "storage increase",
			mutate: func(cfg *appconfig.Config) {
				cfg.Database.Postgres.Storage.Size = "30Gi"
			},
			wantDetail: "immutable PostgreSQL storage size cannot change",
		},
		{
			name: "instance increase",
			mutate: func(cfg *appconfig.Config) {
				cfg.Database.Postgres.Instances = 4
			},
			wantDetail: "immutable PostgreSQL instance count cannot change",
		},
		{
			name: "synchronous replica count",
			mutate: func(cfg *appconfig.Config) {
				cfg.Database.Postgres.Synchronous.Replicas = 2
			},
			wantDetail: "immutable PostgreSQL synchronous replica count cannot change",
		},
		{
			name: "data durability",
			mutate: func(cfg *appconfig.Config) {
				cfg.Database.Postgres.Synchronous.DataDurability = appconfig.PostgresDataDurabilityPreferred
			},
			wantDetail: "immutable PostgreSQL data durability cannot change",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := testPostgresAppConfig(appconfig.PostgresConnectionModeManaged)
			cluster := postgresClusterForApp(original, DefaultNamespace)
			dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme(), cluster)
			nodes := postgresReadyNodes(3, "arm64")
			amdNodes := postgresReadyNodes(3, "amd64")
			for _, object := range amdNodes {
				node := object.(*corev1.Node)
				node.Name = "amd-" + node.Name
			}
			nodes = append(nodes, amdNodes...)
			clientset := fake.NewSimpleClientset(nodes...)
			controller := testPostgresController(clientset, dynamicClient)
			requested := testPostgresAppConfig(appconfig.PostgresConnectionModeManaged)
			tt.mutate(&requested)

			err := controller.reconcilePostgres(context.Background(), requested)
			if err == nil || !strings.Contains(err.Error(), tt.wantDetail) {
				t.Fatalf("expected %q rejection, got %v", tt.wantDetail, err)
			}
			for _, action := range dynamicClient.Actions() {
				if action.GetVerb() == "update" {
					t.Fatalf("unsafe change must be rejected before update: %#v", action)
				}
			}
		})
	}
}

func TestControllerRejectsPostgresScaleDownBeforeUpdate(t *testing.T) {
	original := testPostgresAppConfig(appconfig.PostgresConnectionModeManaged)
	original.Database.Postgres.Instances = 4
	cluster := postgresClusterForApp(original, DefaultNamespace)
	dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme(), cluster)
	clientset := fake.NewSimpleClientset(postgresReadyNodes(4, "arm64")...)
	controller := testPostgresController(clientset, dynamicClient)
	requested := testPostgresAppConfig(appconfig.PostgresConnectionModeManaged)

	err := controller.reconcilePostgres(context.Background(), requested)
	if err == nil || !strings.Contains(err.Error(), "immutable PostgreSQL instance count cannot change from 4 to 3") {
		t.Fatalf("expected destructive scale-down rejection, got %v", err)
	}
	for _, action := range dynamicClient.Actions() {
		if action.GetVerb() == "update" {
			t.Fatalf("PostgreSQL scale-down must be rejected before update: %#v", action)
		}
	}
}

func TestControllerCreatesPostgresBeforeAppDeployment(t *testing.T) {
	cfg := testPostgresAppConfig(appconfig.PostgresConnectionModeManaged)
	clientset := fake.NewSimpleClientset(postgresReadyNodes(3, "arm64")...)
	dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme())
	controller := testPostgresController(clientset, dynamicClient)
	order := []string{}
	dynamicClient.PrependReactor("create", "clusters", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		order = append(order, "database")
		return false, nil, nil
	})
	clientset.PrependReactor("create", "deployments", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		order = append(order, "deployment")
		return false, nil, nil
	})

	if err := controller.Reconcile(context.Background(), cfg, nil, ""); err != nil {
		t.Fatalf("reconcile app with PostgreSQL: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"database", "deployment"}) {
		t.Fatalf("expected database before deployment, got %#v", order)
	}
}

func TestPostgresConnectionModes(t *testing.T) {
	t.Run("managed injects CloudNativePG app URI", func(t *testing.T) {
		cfg := testPostgresAppConfig(appconfig.PostgresConnectionModeManaged)
		deployment, err := deploymentForApp(cfg, DefaultNamespace, "")
		if err != nil {
			t.Fatalf("render managed deployment: %v", err)
		}
		env := deployment.Spec.Template.Spec.Containers[0].Env
		if len(env) != 4 || env[0].Name != "DATABASE_URL" || env[0].ValueFrom == nil ||
			env[0].ValueFrom.SecretKeyRef == nil || env[0].ValueFrom.SecretKeyRef.Name != "my-api-db-app" ||
			env[0].ValueFrom.SecretKeyRef.Key != "uri" {
			t.Fatalf("unexpected managed PostgreSQL environment: %#v", env)
		}
		if env[1].Name != "PGSSLMODE" || env[1].Value != "require" ||
			env[2].Name != "PGCHANNELBINDING" || env[2].Value != "require" ||
			env[3].Name != "PGREQUIREAUTH" || env[3].Value != "scram-sha-256" {
			t.Fatalf("managed PostgreSQL transport security is incomplete: %#v", env)
		}
	})

	t.Run("external keeps ordinary app secret and still reconciles Cluster", func(t *testing.T) {
		cfg := testPostgresAppConfig(appconfig.PostgresConnectionModeExternal)
		cfg.Secrets = []string{"DATABASE_URL"}
		deployment, err := deploymentForApp(cfg, DefaultNamespace, "secret-revision")
		if err != nil {
			t.Fatalf("render external deployment: %v", err)
		}
		env := deployment.Spec.Template.Spec.Containers[0].Env
		if len(env) != 1 || env[0].ValueFrom == nil || env[0].ValueFrom.SecretKeyRef == nil ||
			env[0].ValueFrom.SecretKeyRef.Name != "my-api" || env[0].ValueFrom.SecretKeyRef.Key != "DATABASE_URL" {
			t.Fatalf("unexpected external PostgreSQL environment: %#v", env)
		}

		dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme())
		clientset := fake.NewSimpleClientset(postgresReadyNodes(3, "arm64")...)
		controller := testPostgresController(clientset, dynamicClient)
		if err := controller.reconcilePostgres(context.Background(), cfg); err != nil {
			t.Fatalf("reconcile external-mode PostgreSQL Cluster: %v", err)
		}
		if _, err := controller.databases.Get(context.Background(), "my-api-db", metav1.GetOptions{}); err != nil {
			t.Fatalf("expected external connection mode to retain managed Cluster: %v", err)
		}
	})
}

func TestControllerReportsMissingCloudNativePGPrerequisite(t *testing.T) {
	cfg := testPostgresAppConfig(appconfig.PostgresConnectionModeManaged)
	dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme())
	clientset := fake.NewSimpleClientset(postgresReadyNodes(3, "arm64")...)
	controller := testPostgresController(clientset, dynamicClient)
	missingResource := apierrors.NewNotFound(
		schema.GroupResource{Group: postgresClusterResource.Group, Resource: postgresClusterResource.Resource},
		"",
	)
	dynamicClient.PrependReactor("get", "clusters", func(k8stesting.Action) (bool, k8sruntime.Object, error) {
		return true, nil, missingResource
	})

	err := controller.reconcilePostgres(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "install the CloudNativePG operator") {
		t.Fatalf("expected CloudNativePG prerequisite error, got %v", err)
	}
	_, err = controller.DatabaseStatus(context.Background(), cfg.Name)
	if err == nil || !strings.Contains(err.Error(), "Cluster CRD") {
		t.Fatalf("expected status prerequisite error, got %v", err)
	}
}

func TestControllerRefusesToAdoptUnownedPostgresCluster(t *testing.T) {
	cfg := testPostgresAppConfig(appconfig.PostgresConnectionModeManaged)
	cluster := postgresClusterForApp(cfg, DefaultNamespace)
	cluster.SetLabels(map[string]string{
		"app.kubernetes.io/managed-by": "cloudnative-pg",
		"team.example.com/owner":       "database-team",
	})
	dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme(), cluster)
	clientset := fake.NewSimpleClientset(postgresReadyNodes(3, "arm64")...)
	controller := testPostgresController(clientset, dynamicClient)
	cfg.Database.Postgres.Image = strings.Replace(testPostgresImage, "a", "d", 64)

	err := controller.reconcilePostgres(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "ownership conflict") {
		t.Fatalf("expected unowned Cluster rejection, got %v", err)
	}
	for _, action := range dynamicClient.Actions() {
		if action.GetVerb() == "update" {
			t.Fatalf("unowned Cluster must not be updated: %#v", action)
		}
	}
	got, err := controller.databases.Get(context.Background(), cluster.GetName(), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get unowned Cluster: %v", err)
	}
	image, _, _ := unstructured.NestedString(got.Object, "spec", "imageName")
	if image != testPostgresImage || got.GetLabels()["team.example.com/owner"] != "database-team" {
		t.Fatalf("unowned Cluster was mutated: %#v", got.Object)
	}
}

func TestControllerPreflightsAllPostgresGeneratedResourceNames(t *testing.T) {
	clusterName := "my-api-db"
	tests := []struct {
		name         string
		resourceKind string
		resourceName string
		object       k8sruntime.Object
	}{
		{
			name:         "any service",
			resourceKind: "Service",
			resourceName: clusterName + "-any",
			object: &corev1.Service{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-any", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "read service",
			resourceKind: "Service",
			resourceName: clusterName + "-r",
			object: &corev1.Service{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-r", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "read-only service",
			resourceKind: "Service",
			resourceName: clusterName + "-ro",
			object: &corev1.Service{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-ro", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "read-write service",
			resourceKind: "Service",
			resourceName: clusterName + "-rw",
			object: &corev1.Service{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-rw", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "app secret",
			resourceKind: "Secret",
			resourceName: clusterName + "-app",
			object: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-app", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "CA secret",
			resourceKind: "Secret",
			resourceName: clusterName + "-ca",
			object: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-ca", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "server secret",
			resourceKind: "Secret",
			resourceName: clusterName + "-server",
			object: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-server", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "replication secret",
			resourceKind: "Secret",
			resourceName: clusterName + "-replication",
			object: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-replication", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "superuser secret",
			resourceKind: "Secret",
			resourceName: clusterName + "-superuser",
			object: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-superuser", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "pull secret",
			resourceKind: "Secret",
			resourceName: clusterName + "-pull",
			object: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-pull", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "service account",
			resourceKind: "ServiceAccount",
			resourceName: clusterName,
			object: &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName, Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "initial data volume",
			resourceKind: "PersistentVolumeClaim",
			resourceName: clusterName + "-1",
			object: &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-1", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "second replica data volume",
			resourceKind: "PersistentVolumeClaim",
			resourceName: clusterName + "-2",
			object: &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-2", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "third replica data volume",
			resourceKind: "PersistentVolumeClaim",
			resourceName: clusterName + "-3",
			object: &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-3", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "initial instance pod",
			resourceKind: "Pod",
			resourceName: clusterName + "-1",
			object: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-1", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "second replica pod",
			resourceKind: "Pod",
			resourceName: clusterName + "-2",
			object: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-2", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "third replica pod",
			resourceKind: "Pod",
			resourceName: clusterName + "-3",
			object: &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-3", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "initial bootstrap job",
			resourceKind: "Job",
			resourceName: clusterName + "-1-initdb",
			object: &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-1-initdb", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "second replica join job",
			resourceKind: "Job",
			resourceName: clusterName + "-2-join",
			object: &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-2-join", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "third replica join job",
			resourceKind: "Job",
			resourceName: clusterName + "-3-join",
			object: &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-3-join", Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "primary election lease",
			resourceKind: "Lease",
			resourceName: clusterName,
			object: &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName, Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "instance manager role",
			resourceKind: "Role",
			resourceName: clusterName,
			object: &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName, Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "instance manager role binding",
			resourceKind: "RoleBinding",
			resourceName: clusterName,
			object: &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName, Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "cluster disruption budget",
			resourceKind: "PodDisruptionBudget",
			resourceName: clusterName,
			object: &policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName, Namespace: DefaultNamespace,
			}},
		},
		{
			name:         "primary disruption budget",
			resourceKind: "PodDisruptionBudget",
			resourceName: clusterName + "-primary",
			object: &policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{
				Name: clusterName + "-primary", Namespace: DefaultNamespace,
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := append(postgresReadyNodes(3, "arm64"), tt.object)
			clientset := fake.NewSimpleClientset(objects...)
			dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme())
			controller := testPostgresController(clientset, dynamicClient)

			err := controller.reconcilePostgres(
				context.Background(),
				testPostgresAppConfig(appconfig.PostgresConnectionModeManaged),
			)
			if err == nil || !strings.Contains(err.Error(), tt.resourceKind) ||
				!strings.Contains(err.Error(), tt.resourceName) || !strings.Contains(err.Error(), "already exists") {
				t.Fatalf("expected generated-resource collision, got %v", err)
			}
			for _, action := range dynamicClient.Actions() {
				if action.GetVerb() == "create" {
					t.Fatalf("Cluster must not be created after collision: %#v", action)
				}
			}
		})
	}
}

func TestControllerPreflightsExistingPostgresFailoverQuorum(t *testing.T) {
	const clusterName = "my-api-db"
	quorum := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "postgresql.cnpg.io/v1",
		"kind":       "FailoverQuorum",
		"metadata": map[string]any{
			"name":      clusterName,
			"namespace": DefaultNamespace,
		},
	}}
	clientset := fake.NewSimpleClientset(postgresReadyNodes(3, "arm64")...)
	dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme(), quorum)
	controller := testPostgresController(clientset, dynamicClient)

	err := controller.reconcilePostgres(
		context.Background(),
		testPostgresAppConfig(appconfig.PostgresConnectionModeManaged),
	)
	if err == nil || !strings.Contains(err.Error(), "FailoverQuorum") ||
		!strings.Contains(err.Error(), clusterName) || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected FailoverQuorum collision, got %v", err)
	}
	for _, action := range dynamicClient.Actions() {
		if action.GetVerb() == "create" {
			t.Fatalf("Cluster must not be created after collision: %#v", action)
		}
	}
}

func TestControllerPostgresCreationPreflightFailsClosed(t *testing.T) {
	cfg := testPostgresAppConfig(appconfig.PostgresConnectionModeManaged)
	clientset := fake.NewSimpleClientset(postgresReadyNodes(3, "arm64")...)
	dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme())
	controller := &Controller{
		namespace: DefaultNamespace,
		nodes:     clientset.CoreV1().Nodes(),
		databases: dynamicClient.Resource(postgresClusterResource).Namespace(DefaultNamespace),
	}

	err := controller.reconcilePostgres(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "clients are required") {
		t.Fatalf("expected fail-closed generated-resource preflight, got %v", err)
	}
}

func TestControllerProtectsCNPGResourcesFromCollidingApps(t *testing.T) {
	t.Run("Service", func(t *testing.T) {
		const appName = "money-db-rw"
		cnpgService := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      appName,
				Namespace: DefaultNamespace,
				Labels: map[string]string{
					managedByLabel:    "cloudnative-pg",
					"cnpg.io/cluster": "money-db",
				},
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"cnpg.io/cluster": "money-db", "role": "primary"},
				Ports:    []corev1.ServicePort{{Name: "postgresql", Port: 5432}},
			},
		}
		objects := append(postgresReadyNodes(1, "arm64"), cnpgService)
		clientset := fake.NewSimpleClientset(objects...)
		dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme())
		controller := testPostgresController(clientset, dynamicClient)
		cfg := testAppConfig()
		cfg.Name = appName
		cfg.Routing.Domain = ""

		err := controller.Reconcile(context.Background(), cfg, nil, "")
		if err == nil || !strings.Contains(err.Error(), "Service") || !strings.Contains(err.Error(), "ownership conflict") {
			t.Fatalf("expected colliding Service reconciliation rejection, got %v", err)
		}
		got, err := controller.services.Get(context.Background(), appName, metav1.GetOptions{})
		if err != nil || !reflect.DeepEqual(got.Spec, cnpgService.Spec) {
			t.Fatalf("CNPG Service was mutated: %#v err=%v", got, err)
		}

		err = controller.Delete(context.Background(), appName)
		if err == nil || !strings.Contains(err.Error(), "ownership conflict") {
			t.Fatalf("expected colliding Service deletion rejection, got %v", err)
		}
		if _, err := controller.services.Get(context.Background(), appName, metav1.GetOptions{}); err != nil {
			t.Fatalf("CNPG Service was deleted: %v", err)
		}
	})

	t.Run("Secret", func(t *testing.T) {
		const appName = "money-db-app"
		cnpgSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      appName,
				Namespace: DefaultNamespace,
				Labels: map[string]string{
					managedByLabel:    "cloudnative-pg",
					"cnpg.io/cluster": "money-db",
				},
			},
			Data: map[string][]byte{"uri": []byte("postgresql://operator-generated")},
		}
		objects := append(postgresReadyNodes(1, "arm64"), cnpgSecret)
		clientset := fake.NewSimpleClientset(objects...)
		dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme())
		controller := testPostgresController(clientset, dynamicClient)
		cfg := testAppConfig()
		cfg.Name = appName
		cfg.Routing.Domain = ""
		cfg.Secrets = []string{"DATABASE_URL"}

		err := controller.Reconcile(
			context.Background(),
			cfg,
			map[string]string{"DATABASE_URL": "postgresql://attacker-controlled"},
			"secret-revision",
		)
		if err == nil || !strings.Contains(err.Error(), "Secret") || !strings.Contains(err.Error(), "ownership conflict") {
			t.Fatalf("expected colliding Secret reconciliation rejection, got %v", err)
		}
		got, err := controller.appSecrets.Get(context.Background(), appName, metav1.GetOptions{})
		if err != nil || string(got.Data["uri"]) != "postgresql://operator-generated" || len(got.Data) != 1 {
			t.Fatalf("CNPG Secret was mutated: %#v err=%v", got, err)
		}

		err = controller.Delete(context.Background(), appName)
		if err == nil || !strings.Contains(err.Error(), "ownership conflict") {
			t.Fatalf("expected colliding Secret deletion rejection, got %v", err)
		}
		if _, err := controller.appSecrets.Get(context.Background(), appName, metav1.GetOptions{}); err != nil {
			t.Fatalf("CNPG Secret was deleted: %v", err)
		}
	})
}

func TestControllerRejectsInsufficientPostgresNodes(t *testing.T) {
	cfg := testPostgresAppConfig(appconfig.PostgresConnectionModeManaged)
	nodes := postgresReadyNodes(2, "arm64")
	tainted := testPostgresNode("worker-tainted", "arm64")
	tainted.Spec.Taints = []corev1.Taint{{Key: "node-role.kubernetes.io/control-plane", Effect: corev1.TaintEffectNoSchedule}}
	nodes = append(nodes, tainted, testPostgresNode("worker-amd64", "amd64"))
	clientset := fake.NewSimpleClientset(nodes...)
	dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme())
	controller := testPostgresController(clientset, dynamicClient)

	err := controller.reconcilePostgres(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "requires 3") ||
		!strings.Contains(err.Error(), `architecture "arm64"`) || !strings.Contains(err.Error(), "found 2") {
		t.Fatalf("expected capacity error excluding tainted and wrong-architecture nodes, got %v", err)
	}
	for _, action := range dynamicClient.Actions() {
		if action.GetVerb() == "create" || action.GetVerb() == "update" {
			t.Fatalf("database must not be created after failed capacity preflight: %#v", action)
		}
	}
}

func TestControllerAllowsExistingPostgresReconcileDuringNodeFailure(t *testing.T) {
	cfg := testPostgresAppConfig(appconfig.PostgresConnectionModeManaged)
	cluster := postgresClusterForApp(cfg, DefaultNamespace)
	dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme(), cluster)
	clientset := fake.NewSimpleClientset()
	controller := testPostgresController(clientset, dynamicClient)

	if err := controller.reconcilePostgres(context.Background(), cfg); err != nil {
		t.Fatalf("reconcile existing PostgreSQL during node failure: %v", err)
	}
	updated := false
	for _, action := range dynamicClient.Actions() {
		if action.GetVerb() == "update" {
			updated = true
		}
	}
	if !updated {
		t.Fatalf("expected existing Cluster policy reconciliation, got %#v", dynamicClient.Actions())
	}
}

func TestControllerDeleteRetainsPostgresClusterAndPVCs(t *testing.T) {
	cfg := testPostgresAppConfig(appconfig.PostgresConnectionModeManaged)
	cluster := postgresClusterForApp(cfg, DefaultNamespace)
	dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme(), cluster)
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-api-db-1",
			Namespace: DefaultNamespace,
			Labels:    map[string]string{"cnpg.io/cluster": "my-api-db"},
		},
	}
	clientset := fake.NewSimpleClientset(pvc)
	controller := testPostgresController(clientset, dynamicClient)

	if err := controller.Delete(context.Background(), cfg.Name); err != nil {
		t.Fatalf("delete app resources: %v", err)
	}
	if _, err := controller.databases.Get(context.Background(), "my-api-db", metav1.GetOptions{}); err != nil {
		t.Fatalf("PostgreSQL Cluster must be retained: %v", err)
	}
	if _, err := clientset.CoreV1().PersistentVolumeClaims(DefaultNamespace).Get(
		context.Background(),
		"my-api-db-1",
		metav1.GetOptions{},
	); err != nil {
		t.Fatalf("PostgreSQL PVC must be retained: %v", err)
	}
	for _, action := range dynamicClient.Actions() {
		if action.GetVerb() == "delete" {
			t.Fatalf("Controller.Delete must not delete database resources: %#v", action)
		}
	}
}

func TestControllerDatabaseStatus(t *testing.T) {
	cfg := testPostgresAppConfig(appconfig.PostgresConnectionModeManaged)
	cluster := postgresClusterForApp(cfg, DefaultNamespace)
	cluster.Object["status"] = map[string]any{
		"phase":          "Cluster in healthy state",
		"readyInstances": int64(3),
		"currentPrimary": "my-api-db-1",
	}
	quorum := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "postgresql.cnpg.io/v1",
		"kind":       "FailoverQuorum",
		"metadata": map[string]any{
			"name":      "my-api-db",
			"namespace": DefaultNamespace,
		},
		"status": map[string]any{
			"method":        "any",
			"standbyNumber": int64(1),
			"primary":       "my-api-db-1",
			"standbyNames":  []any{"my-api-db-2", "my-api-db-3"},
		},
	}}
	dynamicClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme(), cluster, quorum)
	clientset := fake.NewSimpleClientset(
		testPostgresPod("my-api-db-1", "node-b", corev1.PodRunning, true),
		testPostgresPod("my-api-db-2", "node-a", corev1.PodRunning, true),
		testPostgresPod("my-api-db-3", "node-c", corev1.PodPending, true),
		testPostgresPod("unrelated-role", "node-z", corev1.PodRunning, false),
	)
	controller := testPostgresController(clientset, dynamicClient)

	got, err := controller.DatabaseStatus(context.Background(), cfg.Name)
	if err != nil {
		t.Fatalf("read database status: %v", err)
	}
	if !got.Present || got.Phase != "Cluster in healthy state" || got.DesiredInstances != 3 ||
		got.ReadyInstances != 3 || got.Primary != "my-api-db-1" ||
		!reflect.DeepEqual(got.RunningNodes, []string{"node-a", "node-b"}) ||
		!reflect.DeepEqual(got.RunningInstances, []string{"my-api-db-1", "my-api-db-2"}) {
		t.Fatalf("unexpected database status: %#v", got)
	}
	if got.SynchronousMethod != "any" || got.SynchronousReplicas != 1 || got.DataDurability != "required" ||
		!got.OwnedByDeployer || got.Image != testPostgresImage ||
		got.BootstrapDatabase != "money_manager" || got.BootstrapOwner != "money_manager" ||
		!got.DataChecksumsEnabled || got.StorageSize != "20Gi" || got.StorageClass != "local-path" ||
		!got.FailoverQuorumEnabled || got.AntiAffinityType != "required" ||
		got.TopologyKey != "kubernetes.io/hostname" || got.Architecture != "arm64" ||
		got.PasswordEncryption != postgresSCRAMEncryption || !got.RejectsNonTLS || !got.RequiresApplicationSCRAM ||
		!got.FailoverQuorumPresent || got.FailoverQuorumMethod != "any" ||
		got.FailoverQuorumStandbyNumber != 1 || got.FailoverQuorumPrimary != "my-api-db-1" ||
		!reflect.DeepEqual(got.FailoverQuorumStandbyNames, []string{"my-api-db-2", "my-api-db-3"}) {
		t.Fatalf("unexpected database HA status: %#v", got)
	}
	if err := unstructured.SetNestedStringSlice(
		cluster.Object,
		[]string{
			"host all all all trust",
			postgresRejectNonTLSHBA,
			postgresApplicationSCRAMHBA("money_manager", "money_manager"),
		},
		"spec",
		"postgresql",
		"pg_hba",
	); err != nil {
		t.Fatalf("prepend unsafe HBA fixture: %v", err)
	}
	if _, err := controller.databases.Update(context.Background(), cluster, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update unsafe HBA fixture: %v", err)
	}
	drifted, err := controller.DatabaseStatus(context.Background(), cfg.Name)
	if err != nil || drifted.RejectsNonTLS || drifted.RequiresApplicationSCRAM {
		t.Fatalf("prepended HBA rule must invalidate effective security status, got %#v err=%v", drifted, err)
	}
	if err := unstructured.SetNestedField(
		cluster.Object,
		false,
		"spec",
		"affinity",
		"enablePodAntiAffinity",
	); err != nil {
		t.Fatalf("disable anti-affinity in fixture: %v", err)
	}
	if _, err := controller.databases.Update(context.Background(), cluster, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update anti-affinity fixture: %v", err)
	}
	drifted, err = controller.DatabaseStatus(context.Background(), cfg.Name)
	if err != nil || drifted.AntiAffinityType != "" {
		t.Fatalf("disabled anti-affinity must not report required, got %#v err=%v", drifted, err)
	}
	labels := cluster.GetLabels()
	labels[managedByLabel] = "other-controller"
	cluster.SetLabels(labels)
	if _, err := controller.databases.Update(context.Background(), cluster, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update ownership fixture: %v", err)
	}
	drifted, err = controller.DatabaseStatus(context.Background(), cfg.Name)
	if err != nil || drifted.OwnedByDeployer {
		t.Fatalf("ownership drift must be reported, got %#v err=%v", drifted, err)
	}

	emptyClient := dynamicfake.NewSimpleDynamicClient(k8sruntime.NewScheme())
	controller.databases = emptyClient.Resource(postgresClusterResource).Namespace(DefaultNamespace)
	missing, err := controller.DatabaseStatus(context.Background(), "missing")
	if err != nil || missing.Present || missing.RunningNodes == nil {
		t.Fatalf("unexpected missing database status %#v: %v", missing, err)
	}
}

const testPostgresImage = "ghcr.io/cloudnative-pg/postgresql:17.10@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testPostgresAppConfig(connectionMode string) appconfig.Config {
	cfg := testAppConfig()
	cfg.Database.Postgres = &appconfig.PostgresConfig{
		Instances:       3,
		Image:           testPostgresImage,
		Database:        "money_manager",
		Owner:           "money_manager",
		ConnectionEnv:   "DATABASE_URL",
		ConnectionMode:  connectionMode,
		Storage:         appconfig.PostgresStorageConfig{Size: "20Gi", StorageClass: "local-path"},
		Synchronous:     appconfig.PostgresSynchronousConfig{Replicas: 1, DataDurability: "required"},
		RetentionPolicy: "retain",
	}
	return cfg
}

func postgresReadyNodes(count int, architecture string) []k8sruntime.Object {
	nodes := make([]k8sruntime.Object, 0, count)
	for i := 1; i <= count; i++ {
		nodes = append(nodes, testPostgresNode("worker-"+strconv.Itoa(i), architecture))
	}
	return nodes
}

func testPostgresNode(name string, architecture string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"kubernetes.io/arch": architecture},
		},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type:   corev1.NodeReady,
			Status: corev1.ConditionTrue,
		}}},
	}
}

func testPostgresPod(name string, node string, phase corev1.PodPhase, instanceRole bool) *corev1.Pod {
	labels := map[string]string{"cnpg.io/cluster": "my-api-db"}
	if instanceRole {
		labels["cnpg.io/podRole"] = "instance"
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: DefaultNamespace, Labels: labels},
		Spec:       corev1.PodSpec{NodeName: node},
		Status:     corev1.PodStatus{Phase: phase},
	}
}

func testPostgresController(clientset *fake.Clientset, dynamicClient dynamic.Interface) *Controller {
	return &Controller{
		namespace:       DefaultNamespace,
		tls:             TLSConfig{}.WithDefaults(),
		namespaces:      clientset.CoreV1().Namespaces(),
		ingresses:       clientset.NetworkingV1().Ingresses(DefaultNamespace),
		services:        clientset.CoreV1().Services(DefaultNamespace),
		appSecrets:      clientset.CoreV1().Secrets(DefaultNamespace),
		nodes:           clientset.CoreV1().Nodes(),
		pods:            clientset.CoreV1().Pods(DefaultNamespace),
		serviceAccounts: clientset.CoreV1().ServiceAccounts(DefaultNamespace),
		pvcs:            clientset.CoreV1().PersistentVolumeClaims(DefaultNamespace),
		pdbs:            clientset.PolicyV1().PodDisruptionBudgets(DefaultNamespace),
		deployments:     clientset.AppsV1().Deployments(DefaultNamespace),
		jobs:            clientset.BatchV1().Jobs(DefaultNamespace),
		leases:          clientset.CoordinationV1().Leases(DefaultNamespace),
		roles:           clientset.RbacV1().Roles(DefaultNamespace),
		roleBindings:    clientset.RbacV1().RoleBindings(DefaultNamespace),
		databases:       dynamicClient.Resource(postgresClusterResource).Namespace(DefaultNamespace),
		quorums:         dynamicClient.Resource(postgresFailoverQuorumResource).Namespace(DefaultNamespace),
	}
}
