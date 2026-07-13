package controller

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

// backupCredsSecret holds the S3 credentials the tenant's CNPG cluster uses to
// stream backups; CNPG requires it in the cluster's own namespace.
const backupCredsSecret = "backup-s3"

var scheduledBackupGVK = schema.GroupVersionKind{
	Group: "postgresql.cnpg.io", Version: "v1", Kind: "ScheduledBackup",
}

// backupsEnabled reports whether tenant Postgres is backed up to Object Storage
// (cloud). Empty bucket = dev, where there is nowhere (and no need) to stream.
func (r *TenantReconciler) backupsEnabled() bool { return r.BackupBucket != "" }

// barmanBackup is the CNPG `spec.backup` block for a tenant cluster — WAL + base
// backups to s3://<bucket>/tenants/<slug>, 7-day retention. nil when disabled.
func (r *TenantReconciler) barmanBackup(slug string) map[string]any {
	if !r.backupsEnabled() {
		return nil
	}
	return map[string]any{
		"retentionPolicy": "7d",
		"barmanObjectStore": map[string]any{
			"destinationPath": fmt.Sprintf("s3://%s/tenants/%s", r.BackupBucket, slug),
			"endpointURL":     r.BackupEndpoint,
			"s3Credentials": map[string]any{
				"accessKeyId":     map[string]any{"name": backupCredsSecret, "key": "ACCESS_KEY_ID"},
				"secretAccessKey": map[string]any{"name": backupCredsSecret, "key": "ACCESS_SECRET_KEY"},
			},
			"wal":  map[string]any{"compression": "gzip"},
			"data": map[string]any{"compression": "gzip"},
		},
	}
}

// ensureBackupCreds materialises the S3 credentials Secret in the tenant
// namespace (create-only) so the CNPG cluster can reach Object Storage. No-op
// when backups are disabled.
func (r *TenantReconciler) ensureBackupCreds(ctx context.Context, tenant *v1alpha1.Tenant, ns string) error {
	if !r.backupsEnabled() {
		return nil
	}
	sec := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: backupCredsSecret}, sec)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	sec = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: backupCredsSecret, Namespace: ns},
		StringData: map[string]string{
			"ACCESS_KEY_ID":     r.BackupS3KeyID,
			"ACCESS_SECRET_KEY": r.BackupS3Secret,
		},
	}
	if err := controllerutil.SetControllerReference(tenant, sec, r.Scheme()); err != nil {
		return err
	}
	return r.Create(ctx, sec)
}

// ensureScheduledBackup creates the daily base-backup schedule for the tenant's
// cluster (create-only). WAL archiving is continuous via the cluster's backup
// config; this adds the periodic full backup. No-op when disabled.
func (r *TenantReconciler) ensureScheduledBackup(ctx context.Context, tenant *v1alpha1.Tenant, ns string) error {
	if !r.backupsEnabled() {
		return nil
	}
	sb := &unstructured.Unstructured{}
	sb.SetGroupVersionKind(scheduledBackupGVK)
	err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: "db"}, sb)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	sb = &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "postgresql.cnpg.io/v1",
		"kind":       "ScheduledBackup",
		"metadata":   map[string]any{"name": "db", "namespace": ns},
		"spec": map[string]any{
			// CNPG cron is 6-field (seconds first): 03:00 daily.
			"schedule":             "0 0 3 * * *",
			"backupOwnerReference": "self",
			"cluster":              map[string]any{"name": "db"},
		},
	}}
	sb.SetGroupVersionKind(scheduledBackupGVK)
	if err := controllerutil.SetControllerReference(tenant, sb, r.Scheme()); err != nil {
		return err
	}
	return r.Create(ctx, sb)
}

// blobBackupsEnabled reports whether the per-tenant blob-backup CronJob is
// provisioned: it needs somewhere to stream (the bucket) AND an age recipient
// to escrow the DEK to — blobs restored without their DEK are undecryptable,
// so shipping one half is not a backup.
func (r *TenantReconciler) blobBackupsEnabled() bool {
	return r.backupsEnabled() && r.BackupAgeRecipient != ""
}

// ensureBlobBackup provisions the nightly CronJob that copies a blob-backed
// app's chunk store to Object Storage and escrows its DEK (interim until #21
// moves blobs into S3 natively — then durability is by construction and this
// job retires). Create-only, tenant-owned, like the rest of provisioning.
func (r *TenantReconciler) ensureBlobBackup(ctx context.Context, tenant *v1alpha1.Tenant, ns string, app CatalogApp) error {
	if !r.blobBackupsEnabled() {
		return nil
	}
	return r.createIfAbsent(ctx, tenant, r.blobBackupCronJob(tenant, ns, app))
}

// blobBackupCronJob builds the backup CronJob: the backup image (rclone+age)
// with the app's blob PVC mounted read-only and, when the app has a DEK, the
// DEK Secret mounted under the escrow dir. Runs at 03:30, after the 03:00
// CNPG base backup, so a same-night restore pairs a DB with a blob superset
// (chunks are content-addressed and copied without --delete, so blobs can
// only be a superset of what any earlier DB backup references).
//
// The pod mounts its inputs directly instead of reading the API — no
// ServiceAccount token, no RBAC. It carries no app label, so no tenant
// NetworkPolicy selects it and egress to Object Storage is open (the same
// posture as the tenant's CNPG pods, which stream WAL from this namespace
// already). Same non-root/read-only posture as the app pods; uid 65532
// matches the blob PVC's fsGroup.
func (r *TenantReconciler) blobBackupCronJob(tenant *v1alpha1.Tenant, ns string, app CatalogApp) *batchv1.CronJob {
	runAsNonRoot, noPrivEsc, readOnlyRoot, noToken := true, false, true, false
	uid := int64(65532)
	backoff := int32(2)

	env := []corev1.EnvVar{
		{Name: "BACKUP_BUCKET", Value: r.BackupBucket},
		{Name: "BACKUP_PREFIX", Value: "tenants/" + tenant.Spec.Slug},
		{Name: "BACKUP_ENDPOINT", Value: r.BackupEndpoint},
		{Name: "BACKUP_REGION", Value: r.BackupRegion},
		{Name: "AGE_RECIPIENT", Value: r.BackupAgeRecipient},
		{Name: "BLOB_DIR", Value: "/mnt/blob"},
		// rclone wants a HOME to (not) find its config file in; the root
		// filesystem is read-only, so point it at the tmp emptyDir.
		{Name: "HOME", Value: "/tmp"},
		{Name: "ACCESS_KEY_ID", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: backupCredsSecret}, Key: "ACCESS_KEY_ID"}}},
		{Name: "ACCESS_SECRET_KEY", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: backupCredsSecret}, Key: "ACCESS_SECRET_KEY"}}},
	}
	if r.BackupHeartbeat != "" {
		env = append(env, corev1.EnvVar{Name: "HEARTBEAT_URL", Value: r.BackupHeartbeat})
	}
	volumes := []corev1.Volume{
		{Name: "blob", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
			ClaimName: app.Name + "-blob", ReadOnly: true}}},
		{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	}
	mounts := []corev1.VolumeMount{
		{Name: "blob", MountPath: "/mnt/blob", ReadOnly: true},
		{Name: "tmp", MountPath: "/tmp"},
	}
	if app.NeedsDEK {
		env = append(env, corev1.EnvVar{Name: "ESCROW_DIR", Value: "/mnt/escrow"})
		volumes = append(volumes, corev1.Volume{Name: "dek", VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: app.Name + "-dek"}}})
		mounts = append(mounts, corev1.VolumeMount{Name: "dek", MountPath: "/mnt/escrow/" + app.Name + "-dek", ReadOnly: true})
	}

	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: app.Name + "-blob-backup", Namespace: ns},
		Spec: batchv1.CronJobSpec{
			// Standard 5-field cron (unlike CNPG's 6-field): 03:30 daily.
			Schedule:          "30 3 * * *",
			ConcurrencyPolicy: batchv1.ForbidConcurrent,
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					BackoffLimit: &backoff,
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy:                corev1.RestartPolicyOnFailure,
							AutomountServiceAccountToken: &noToken,
							SecurityContext: &corev1.PodSecurityContext{
								RunAsNonRoot:   &runAsNonRoot,
								RunAsUser:      &uid,
								RunAsGroup:     &uid,
								FSGroup:        &uid,
								SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
							},
							Containers: []corev1.Container{{
								Name:            "backup",
								Image:           r.backupImage(),
								ImagePullPolicy: corev1.PullIfNotPresent,
								Env:             env,
								VolumeMounts:    mounts,
								SecurityContext: &corev1.SecurityContext{
									AllowPrivilegeEscalation: &noPrivEsc,
									ReadOnlyRootFilesystem:   &readOnlyRoot,
									Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
								},
							}},
							Volumes: volumes,
						},
					},
				},
			},
		},
	}
}

// backupImage resolves the backup job image with imageFor's defaulting — the
// backup job is infrastructure, not a catalog app, so it has no CatalogApp.
func (r *TenantReconciler) backupImage() string {
	prefix, tag := r.ImagePrefix, r.ImageTag
	if prefix == "" {
		prefix = "peristera-"
	}
	if tag == "" {
		tag = "dev"
	}
	return prefix + "backup:" + tag
}
