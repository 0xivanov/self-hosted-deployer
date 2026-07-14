package ingress

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/appconfig"
	"github.com/0xivanov/self-hosted-deployer/internal/domain"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	postgresClusterSuffix   = "-db"
	postgresAppSecretSuffix = "-db-app"
	postgresRejectNonTLSHBA = "hostnossl all all all reject"
	postgresSCRAMEncryption = "scram-sha-256"
)

func (c *Controller) reconcilePostgres(ctx context.Context, cfg appconfig.Config) error {
	cfg.Normalize()
	if cfg.Database.Postgres == nil {
		return nil
	}
	if c.databases == nil {
		return cloudNativePGPrerequisiteError(nil)
	}

	desired := postgresClusterForApp(cfg, c.namespace)
	existing, err := c.databases.Get(ctx, desired.GetName(), metav1.GetOptions{})
	if postgresAPIUnavailable(err) {
		return cloudNativePGPrerequisiteError(err)
	}
	if apierrors.IsNotFound(err) {
		if err := c.ensurePostgresCapacity(ctx, cfg); err != nil {
			return err
		}
		if err := c.ensurePostgresGeneratedResourcesAvailable(
			ctx,
			desired.GetName(),
			cfg.Database.Postgres.Instances,
		); err != nil {
			return err
		}
		if _, err := c.databases.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
			if postgresAPIUnavailable(err) || apierrors.IsNotFound(err) {
				return cloudNativePGPrerequisiteError(err)
			}
			return fmt.Errorf("create PostgreSQL Cluster %q: %w", desired.GetName(), err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get PostgreSQL Cluster %q: %w", desired.GetName(), err)
	}
	if err := requirePostgresClusterOwnership(existing, cfg); err != nil {
		return err
	}

	if err := validatePostgresUpdate(existing, desired); err != nil {
		return fmt.Errorf("update PostgreSQL Cluster %q: %w", desired.GetName(), err)
	}
	updated, err := mergePostgresCluster(existing, desired)
	if err != nil {
		return fmt.Errorf("merge PostgreSQL Cluster %q: %w", desired.GetName(), err)
	}
	if _, err := c.databases.Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
		if postgresAPIUnavailable(err) {
			return cloudNativePGPrerequisiteError(err)
		}
		return fmt.Errorf("update PostgreSQL Cluster %q: %w", desired.GetName(), err)
	}
	return nil
}

func (c *Controller) ensurePostgresGeneratedResourcesAvailable(
	ctx context.Context,
	clusterName string,
	instances int,
) error {
	if c.services == nil || c.appSecrets == nil || c.serviceAccounts == nil || c.pvcs == nil ||
		c.pods == nil || c.pdbs == nil || c.jobs == nil || c.leases == nil || c.roles == nil ||
		c.roleBindings == nil || c.quorums == nil {
		return fmt.Errorf(
			"cannot verify generated resource ownership before creating PostgreSQL Cluster %q: Kubernetes generated-resource clients are required",
			clusterName,
		)
	}

	if _, err := c.quorums.Get(ctx, clusterName, metav1.GetOptions{}); err == nil {
		return postgresGeneratedResourceCollision("FailoverQuorum", clusterName, clusterName)
	} else if postgresAPIUnavailable(err) {
		return cloudNativePGPrerequisiteError(err)
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("check FailoverQuorum %q before creating PostgreSQL Cluster %q: %w", clusterName, clusterName, err)
	}

	for _, name := range []string{
		clusterName + "-any",
		clusterName + "-r",
		clusterName + "-ro",
		clusterName + "-rw",
	} {
		if _, err := c.services.Get(ctx, name, metav1.GetOptions{}); err == nil {
			return postgresGeneratedResourceCollision("Service", name, clusterName)
		} else if !apierrors.IsNotFound(err) {
			return fmt.Errorf("check Service %q before creating PostgreSQL Cluster %q: %w", name, clusterName, err)
		}
	}

	for _, name := range []string{
		clusterName + "-app",
		clusterName + "-ca",
		clusterName + "-server",
		clusterName + "-replication",
		clusterName + "-superuser",
		clusterName + "-pull",
	} {
		if _, err := c.appSecrets.Get(ctx, name, metav1.GetOptions{}); err == nil {
			return postgresGeneratedResourceCollision("Secret", name, clusterName)
		} else if !apierrors.IsNotFound(err) {
			return fmt.Errorf("check Secret %q before creating PostgreSQL Cluster %q: %w", name, clusterName, err)
		}
	}

	if _, err := c.serviceAccounts.Get(ctx, clusterName, metav1.GetOptions{}); err == nil {
		return postgresGeneratedResourceCollision("ServiceAccount", clusterName, clusterName)
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("check ServiceAccount %q before creating PostgreSQL Cluster %q: %w", clusterName, clusterName, err)
	}

	serials := make([]int, instances)
	for index := range serials {
		serials[index] = index + 1
	}
	if err := c.ensurePostgresInstanceResourcesAvailable(ctx, clusterName, serials, true); err != nil {
		return err
	}

	for _, name := range []string{clusterName, clusterName + "-primary"} {
		if _, err := c.pdbs.Get(ctx, name, metav1.GetOptions{}); err == nil {
			return postgresGeneratedResourceCollision("PodDisruptionBudget", name, clusterName)
		} else if !apierrors.IsNotFound(err) {
			return fmt.Errorf(
				"check PodDisruptionBudget %q before creating PostgreSQL Cluster %q: %w",
				name,
				clusterName,
				err,
			)
		}
	}
	if _, err := c.leases.Get(ctx, clusterName, metav1.GetOptions{}); err == nil {
		return postgresGeneratedResourceCollision("Lease", clusterName, clusterName)
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("check Lease %q before creating PostgreSQL Cluster %q: %w", clusterName, clusterName, err)
	}
	if _, err := c.roles.Get(ctx, clusterName, metav1.GetOptions{}); err == nil {
		return postgresGeneratedResourceCollision("Role", clusterName, clusterName)
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("check Role %q before creating PostgreSQL Cluster %q: %w", clusterName, clusterName, err)
	}
	if _, err := c.roleBindings.Get(ctx, clusterName, metav1.GetOptions{}); err == nil {
		return postgresGeneratedResourceCollision("RoleBinding", clusterName, clusterName)
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("check RoleBinding %q before creating PostgreSQL Cluster %q: %w", clusterName, clusterName, err)
	}
	return nil
}

func (c *Controller) ensurePostgresInstanceResourcesAvailable(
	ctx context.Context,
	clusterName string,
	serials []int,
	includesBootstrap bool,
) error {
	if c.pvcs == nil || c.pods == nil || c.jobs == nil {
		return fmt.Errorf(
			"cannot verify generated instance resource ownership before changing PostgreSQL Cluster %q: Kubernetes PersistentVolumeClaim, Pod, and Job clients are required",
			clusterName,
		)
	}
	for _, serial := range serials {
		instanceName := fmt.Sprintf("%s-%d", clusterName, serial)
		if _, err := c.pvcs.Get(ctx, instanceName, metav1.GetOptions{}); err == nil {
			return postgresGeneratedResourceCollision("PersistentVolumeClaim", instanceName, clusterName)
		} else if !apierrors.IsNotFound(err) {
			return fmt.Errorf("check PersistentVolumeClaim %q before changing PostgreSQL Cluster %q: %w", instanceName, clusterName, err)
		}
		if _, err := c.pods.Get(ctx, instanceName, metav1.GetOptions{}); err == nil {
			return postgresGeneratedResourceCollision("Pod", instanceName, clusterName)
		} else if !apierrors.IsNotFound(err) {
			return fmt.Errorf("check Pod %q before changing PostgreSQL Cluster %q: %w", instanceName, clusterName, err)
		}

		jobRole := "join"
		if includesBootstrap && serial == 1 {
			jobRole = "initdb"
		}
		jobName := instanceName + "-" + jobRole
		if _, err := c.jobs.Get(ctx, jobName, metav1.GetOptions{}); err == nil {
			return postgresGeneratedResourceCollision("Job", jobName, clusterName)
		} else if !apierrors.IsNotFound(err) {
			return fmt.Errorf("check Job %q before changing PostgreSQL Cluster %q: %w", jobName, clusterName, err)
		}
	}
	return nil
}

func postgresGeneratedResourceCollision(resourceKind string, resourceName string, clusterName string) error {
	return fmt.Errorf(
		"cannot create PostgreSQL Cluster %q: generated %s %q already exists",
		clusterName,
		resourceKind,
		resourceName,
	)
}

func requirePostgresClusterOwnership(cluster *unstructured.Unstructured, cfg appconfig.Config) error {
	labels := cluster.GetLabels()
	expected := map[string]string{
		managedByLabel:                 managedByDeployer,
		"deployer.io/database-for":     cfg.Name,
		"deployer.io/retention-policy": cfg.Database.Postgres.RetentionPolicy,
	}
	for key, value := range expected {
		if labels[key] == value {
			continue
		}
		return fmt.Errorf(
			"PostgreSQL Cluster %q ownership conflict: expected label %s=%q, found %q",
			cluster.GetName(),
			key,
			value,
			labels[key],
		)
	}
	return nil
}

func (c *Controller) ensurePostgresCapacity(ctx context.Context, cfg appconfig.Config) error {
	if c.nodes == nil {
		return fmt.Errorf("Kubernetes node client is not configured")
	}
	postgres := cfg.Database.Postgres
	if postgres == nil {
		return nil
	}

	nodes, err := c.nodes.List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list Kubernetes nodes for PostgreSQL placement: %w", err)
	}
	architecture := placementArchitecture(cfg.Placement.Arch)
	available := 0
	for _, node := range nodes.Items {
		if node.Spec.Unschedulable || !readyForScheduling(node) || hasBlockingTaint(node) {
			continue
		}
		if node.Labels["kubernetes.io/arch"] != architecture {
			continue
		}
		available++
	}
	if available < postgres.Instances {
		return fmt.Errorf(
			"PostgreSQL requires %d ready schedulable Kubernetes nodes for architecture %q, found %d",
			postgres.Instances,
			architecture,
			available,
		)
	}
	return nil
}

func hasBlockingTaint(node corev1.Node) bool {
	for _, taint := range node.Spec.Taints {
		if taint.Effect == corev1.TaintEffectNoSchedule || taint.Effect == corev1.TaintEffectNoExecute {
			return true
		}
	}
	return false
}

func postgresClusterForApp(cfg appconfig.Config, namespace string) *unstructured.Unstructured {
	cfg.Normalize()
	postgres := cfg.Database.Postgres
	clusterName := cfg.Name + postgresClusterSuffix
	storage := map[string]any{
		"size": postgres.Storage.Size,
	}
	if postgres.Storage.StorageClass != "" {
		storage["storageClass"] = postgres.Storage.StorageClass
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "postgresql.cnpg.io/v1",
		"kind":       "Cluster",
		"metadata": map[string]any{
			"name":      clusterName,
			"namespace": namespace,
			"labels": map[string]any{
				"app.kubernetes.io/name":       clusterName,
				"app.kubernetes.io/instance":   clusterName,
				"app.kubernetes.io/component":  "database",
				"app.kubernetes.io/managed-by": "deployer",
				"deployer.io/database-for":     cfg.Name,
				"deployer.io/retention-policy": postgres.RetentionPolicy,
			},
		},
		"spec": map[string]any{
			"instances": int64(postgres.Instances),
			"imageName": postgres.Image,
			"bootstrap": map[string]any{
				"initdb": map[string]any{
					"database":      postgres.Database,
					"owner":         postgres.Owner,
					"dataChecksums": true,
				},
			},
			"affinity": map[string]any{
				"enablePodAntiAffinity": true,
				"topologyKey":           "kubernetes.io/hostname",
				"podAntiAffinityType":   "required",
				"nodeSelector": map[string]any{
					"kubernetes.io/arch": placementArchitecture(cfg.Placement.Arch),
				},
			},
			"storage": storage,
			"postgresql": map[string]any{
				"parameters": map[string]any{
					"password_encryption": postgresSCRAMEncryption,
				},
				"pg_hba": []any{
					postgresRejectNonTLSHBA,
					postgresApplicationSCRAMHBA(postgres.Database, postgres.Owner),
				},
				"synchronous": map[string]any{
					"method":         "any",
					"number":         int64(postgres.Synchronous.Replicas),
					"dataDurability": postgres.Synchronous.DataDurability,
					"failoverQuorum": true,
				},
			},
		},
	}}
}

func validatePostgresUpdate(existing *unstructured.Unstructured, desired *unstructured.Unstructured) error {
	currentImage := nestedString(existing.Object, "spec", "imageName")
	requestedImage := nestedString(desired.Object, "spec", "imageName")
	if currentImage != requestedImage {
		return fmt.Errorf(
			"immutable PostgreSQL image cannot change from %q to %q through app deployment; use a dedicated database-maintenance runbook",
			currentImage,
			requestedImage,
		)
	}
	currentInstances := nestedInt64(existing.Object, "spec", "instances")
	requestedInstances := nestedInt64(desired.Object, "spec", "instances")
	if requestedInstances != currentInstances {
		return fmt.Errorf(
			"immutable PostgreSQL instance count cannot change from %d to %d through app deployment; use a dedicated database-maintenance runbook",
			currentInstances,
			requestedInstances,
		)
	}

	immutableFields := []struct {
		name   string
		fields []string
	}{
		{
			name:   "placement architecture",
			fields: []string{"spec", "affinity", "nodeSelector", "kubernetes.io/arch"},
		},
		{name: "bootstrap database", fields: []string{"spec", "bootstrap", "initdb", "database"}},
		{name: "bootstrap owner", fields: []string{"spec", "bootstrap", "initdb", "owner"}},
		{name: "storage class", fields: []string{"spec", "storage", "storageClass"}},
	}
	for _, field := range immutableFields {
		current := nestedString(existing.Object, field.fields...)
		requested := nestedString(desired.Object, field.fields...)
		if current != requested {
			return fmt.Errorf(
				"immutable %s cannot change from %q to %q",
				field.name,
				current,
				requested,
			)
		}
	}
	currentChecksums, currentChecksumsFound, err := unstructured.NestedBool(
		existing.Object,
		"spec",
		"bootstrap",
		"initdb",
		"dataChecksums",
	)
	if err != nil {
		return fmt.Errorf("read current bootstrap data checksums: %w", err)
	}
	requestedChecksums, requestedChecksumsFound, err := unstructured.NestedBool(
		desired.Object,
		"spec",
		"bootstrap",
		"initdb",
		"dataChecksums",
	)
	if err != nil {
		return fmt.Errorf("read requested bootstrap data checksums: %w", err)
	}
	checksumsChanged := !currentChecksumsFound || !requestedChecksumsFound || currentChecksums != requestedChecksums
	if checksumsChanged {
		return fmt.Errorf(
			"immutable bootstrap data checksums cannot change from %t to %t",
			currentChecksums,
			requestedChecksums,
		)
	}

	currentSize := nestedString(existing.Object, "spec", "storage", "size")
	requestedSize := nestedString(desired.Object, "spec", "storage", "size")
	currentQuantity, err := resource.ParseQuantity(currentSize)
	if err != nil {
		return fmt.Errorf("parse current PostgreSQL storage size %q: %w", currentSize, err)
	}
	requestedQuantity, err := resource.ParseQuantity(requestedSize)
	if err != nil {
		return fmt.Errorf("parse requested PostgreSQL storage size %q: %w", requestedSize, err)
	}
	if requestedQuantity.Cmp(currentQuantity) != 0 {
		return fmt.Errorf(
			"immutable PostgreSQL storage size cannot change from %q to %q through app deployment; use a dedicated database-maintenance runbook",
			currentSize,
			requestedSize,
		)
	}
	currentSynchronousReplicas := nestedInt64(existing.Object, "spec", "postgresql", "synchronous", "number")
	requestedSynchronousReplicas := nestedInt64(desired.Object, "spec", "postgresql", "synchronous", "number")
	if currentSynchronousReplicas != requestedSynchronousReplicas {
		return fmt.Errorf(
			"immutable PostgreSQL synchronous replica count cannot change from %d to %d through app deployment; use a dedicated database-maintenance runbook",
			currentSynchronousReplicas,
			requestedSynchronousReplicas,
		)
	}
	currentDataDurability := nestedString(existing.Object, "spec", "postgresql", "synchronous", "dataDurability")
	requestedDataDurability := nestedString(desired.Object, "spec", "postgresql", "synchronous", "dataDurability")
	if currentDataDurability != requestedDataDurability {
		return fmt.Errorf(
			"immutable PostgreSQL data durability cannot change from %q to %q through app deployment; use a dedicated database-maintenance runbook",
			currentDataDurability,
			requestedDataDurability,
		)
	}
	return nil
}

func mergePostgresCluster(
	existing *unstructured.Unstructured,
	desired *unstructured.Unstructured,
) (*unstructured.Unstructured, error) {
	updated := existing.DeepCopy()
	labels := cloneStringMap(updated.GetLabels())
	for key, value := range desired.GetLabels() {
		labels[key] = value
	}
	updated.SetLabels(labels)

	ownedFields := [][]string{
		{"spec", "instances"},
		{"spec", "imageName"},
		{"spec", "bootstrap", "initdb", "database"},
		{"spec", "bootstrap", "initdb", "owner"},
		{"spec", "bootstrap", "initdb", "dataChecksums"},
		{"spec", "affinity", "enablePodAntiAffinity"},
		{"spec", "affinity", "topologyKey"},
		{"spec", "affinity", "podAntiAffinityType"},
		{"spec", "affinity", "nodeSelector", "kubernetes.io/arch"},
		{"spec", "storage", "size"},
		{"spec", "storage", "storageClass"},
		{"spec", "postgresql", "parameters", "password_encryption"},
		{"spec", "postgresql", "synchronous"},
	}
	for _, fields := range ownedFields {
		if err := copyPostgresField(updated, desired, fields...); err != nil {
			return nil, err
		}
	}
	if err := mergePostgresHBA(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

func copyPostgresField(
	target *unstructured.Unstructured,
	source *unstructured.Unstructured,
	fields ...string,
) error {
	value, found, err := unstructured.NestedFieldNoCopy(source.Object, fields...)
	if err != nil {
		return fmt.Errorf("read desired field %q: %w", strings.Join(fields, "."), err)
	}
	if !found {
		return fmt.Errorf("desired field %q is missing", strings.Join(fields, "."))
	}
	if err := unstructured.SetNestedField(
		target.Object,
		runtime.DeepCopyJSONValue(value),
		fields...,
	); err != nil {
		return fmt.Errorf("set desired field %q: %w", strings.Join(fields, "."), err)
	}
	return nil
}

func mergePostgresHBA(cluster *unstructured.Unstructured) error {
	existing, found, err := unstructured.NestedSlice(cluster.Object, "spec", "postgresql", "pg_hba")
	if err != nil {
		return fmt.Errorf("read existing PostgreSQL pg_hba: %w", err)
	}
	if !found {
		existing = []any{}
	}
	database := nestedString(cluster.Object, "spec", "bootstrap", "initdb", "database")
	owner := nestedString(cluster.Object, "spec", "bootstrap", "initdb", "owner")
	applicationRule := postgresApplicationSCRAMHBA(database, owner)
	rules := make([]any, 0, len(existing)+2)
	rules = append(rules, postgresRejectNonTLSHBA, applicationRule)
	for _, rule := range existing {
		if text, ok := rule.(string); ok {
			if text == postgresRejectNonTLSHBA || text == applicationRule {
				continue
			}
		}
		rules = append(rules, runtime.DeepCopyJSONValue(rule))
	}
	if err := unstructured.SetNestedSlice(cluster.Object, rules, "spec", "postgresql", "pg_hba"); err != nil {
		return fmt.Errorf("set PostgreSQL pg_hba: %w", err)
	}
	return nil
}

func postgresApplicationSCRAMHBA(database string, owner string) string {
	return fmt.Sprintf("hostssl %s %s all %s", database, owner, postgresSCRAMEncryption)
}

func cloneStringMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloudNativePGPrerequisiteError(cause error) error {
	const message = "CloudNativePG prerequisite is unavailable: install the CloudNativePG operator and postgresql.cnpg.io/v1 Cluster CRD"
	if cause == nil {
		return errors.New(message)
	}
	return fmt.Errorf("%s: %w", message, cause)
}

func postgresAPIUnavailable(err error) bool {
	if err == nil || !apierrors.IsNotFound(err) {
		return false
	}
	var statusError *apierrors.StatusError
	if !errors.As(err, &statusError) {
		return strings.Contains(strings.ToLower(err.Error()), "requested resource")
	}
	details := statusError.ErrStatus.Details
	if details == nil || strings.TrimSpace(details.Name) == "" {
		return true
	}
	return strings.Contains(strings.ToLower(statusError.ErrStatus.Message), "requested resource")
}

func (c *Controller) DatabaseStatus(ctx context.Context, appName string) (domain.DatabaseStatus, error) {
	status := domain.DatabaseStatus{
		RunningNodes:               []string{},
		RunningInstances:           []string{},
		FailoverQuorumStandbyNames: []string{},
	}
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return status, nil
	}
	if c.databases == nil {
		return status, cloudNativePGPrerequisiteError(nil)
	}

	clusterName := appName + postgresClusterSuffix
	cluster, err := c.databases.Get(ctx, clusterName, metav1.GetOptions{})
	if postgresAPIUnavailable(err) {
		return status, cloudNativePGPrerequisiteError(err)
	}
	if apierrors.IsNotFound(err) {
		return status, nil
	}
	if err != nil {
		return status, fmt.Errorf("get PostgreSQL Cluster %q status: %w", clusterName, err)
	}

	status.Present = true
	labels := cluster.GetLabels()
	status.OwnedByDeployer = labels[managedByLabel] == managedByDeployer &&
		labels["deployer.io/database-for"] == appName &&
		labels["deployer.io/retention-policy"] == appconfig.PostgresRetentionPolicyRetain
	status.Image = nestedString(cluster.Object, "spec", "imageName")
	status.BootstrapDatabase = nestedString(cluster.Object, "spec", "bootstrap", "initdb", "database")
	status.BootstrapOwner = nestedString(cluster.Object, "spec", "bootstrap", "initdb", "owner")
	status.DataChecksumsEnabled = nestedBool(cluster.Object, "spec", "bootstrap", "initdb", "dataChecksums")
	status.StorageSize = nestedString(cluster.Object, "spec", "storage", "size")
	status.StorageClass = nestedString(cluster.Object, "spec", "storage", "storageClass")
	status.DesiredInstances = int32(nestedInt64(cluster.Object, "spec", "instances"))
	status.ReadyInstances = int32(nestedInt64(cluster.Object, "status", "readyInstances"))
	status.Phase = nestedString(cluster.Object, "status", "phase")
	status.Primary = nestedString(cluster.Object, "status", "currentPrimary")
	status.SynchronousMethod = nestedString(cluster.Object, "spec", "postgresql", "synchronous", "method")
	status.SynchronousReplicas = int32(nestedInt64(cluster.Object, "spec", "postgresql", "synchronous", "number"))
	status.DataDurability = nestedString(cluster.Object, "spec", "postgresql", "synchronous", "dataDurability")
	status.FailoverQuorumEnabled = nestedBool(
		cluster.Object,
		"spec",
		"postgresql",
		"synchronous",
		"failoverQuorum",
	)
	if nestedBool(cluster.Object, "spec", "affinity", "enablePodAntiAffinity") {
		status.AntiAffinityType = nestedString(cluster.Object, "spec", "affinity", "podAntiAffinityType")
	}
	status.TopologyKey = nestedString(cluster.Object, "spec", "affinity", "topologyKey")
	status.Architecture = nestedString(
		cluster.Object,
		"spec",
		"affinity",
		"nodeSelector",
		"kubernetes.io/arch",
	)
	status.PasswordEncryption = nestedString(
		cluster.Object,
		"spec",
		"postgresql",
		"parameters",
		"password_encryption",
	)
	postgresHBA := nestedStringSlice(cluster.Object, "spec", "postgresql", "pg_hba")
	status.RejectsNonTLS = len(postgresHBA) >= 1 && postgresHBA[0] == postgresRejectNonTLSHBA
	status.RequiresApplicationSCRAM = len(postgresHBA) >= 2 && postgresHBA[1] == postgresApplicationSCRAMHBA(
		nestedString(cluster.Object, "spec", "bootstrap", "initdb", "database"),
		nestedString(cluster.Object, "spec", "bootstrap", "initdb", "owner"),
	)
	if c.quorums == nil {
		return domain.DatabaseStatus{}, cloudNativePGPrerequisiteError(nil)
	}
	quorum, err := c.quorums.Get(ctx, clusterName, metav1.GetOptions{})
	if postgresAPIUnavailable(err) {
		return domain.DatabaseStatus{}, cloudNativePGPrerequisiteError(err)
	}
	if err == nil {
		status.FailoverQuorumPresent = true
		status.FailoverQuorumMethod = nestedString(quorum.Object, "status", "method")
		status.FailoverQuorumStandbyNumber = int32(nestedInt64(quorum.Object, "status", "standbyNumber"))
		status.FailoverQuorumPrimary = nestedString(quorum.Object, "status", "primary")
		status.FailoverQuorumStandbyNames = nestedStringSlice(quorum.Object, "status", "standbyNames")
	} else if !apierrors.IsNotFound(err) {
		return domain.DatabaseStatus{}, fmt.Errorf("get FailoverQuorum %q status: %w", clusterName, err)
	}
	if c.pods == nil {
		return status, nil
	}

	pods, err := c.pods.List(ctx, metav1.ListOptions{
		LabelSelector: "cnpg.io/cluster=" + clusterName + ",cnpg.io/podRole=instance",
	})
	if err != nil {
		return domain.DatabaseStatus{}, fmt.Errorf("list Pods for PostgreSQL Cluster %q status: %w", clusterName, err)
	}
	nodeSet := map[string]struct{}{}
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning || strings.TrimSpace(pod.Spec.NodeName) == "" {
			continue
		}
		nodeSet[pod.Spec.NodeName] = struct{}{}
		status.RunningInstances = append(status.RunningInstances, pod.Name)
	}
	status.RunningNodes = make([]string, 0, len(nodeSet))
	for nodeName := range nodeSet {
		status.RunningNodes = append(status.RunningNodes, nodeName)
	}
	sort.Strings(status.RunningNodes)
	sort.Strings(status.RunningInstances)
	return status, nil
}

func nestedInt64(object map[string]any, fields ...string) int64 {
	value, _, _ := unstructured.NestedInt64(object, fields...)
	return value
}

func nestedString(object map[string]any, fields ...string) string {
	value, _, _ := unstructured.NestedString(object, fields...)
	return value
}

func nestedBool(object map[string]any, fields ...string) bool {
	value, _, _ := unstructured.NestedBool(object, fields...)
	return value
}

func nestedStringSlice(object map[string]any, fields ...string) []string {
	value, found, _ := unstructured.NestedStringSlice(object, fields...)
	if !found {
		return []string{}
	}
	return value
}
