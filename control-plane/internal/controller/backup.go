package controller

import (
	"context"
	"fmt"

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
