package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

func TestBarmanBackup(t *testing.T) {
	// Dev: no bucket -> no backup block on the tenant cluster.
	if (&TenantReconciler{}).barmanBackup("demo") != nil {
		t.Error("dev (empty BackupBucket) must produce no backup block")
	}
	// Cloud: destination is per-tenant, creds reference the in-namespace secret.
	r := &TenantReconciler{BackupBucket: "peristera-backups", BackupEndpoint: "https://s3.fr-par.scw.cloud"}
	b := r.barmanBackup("demo")
	if b == nil {
		t.Fatal("cloud must produce a backup block")
	}
	store := b["barmanObjectStore"].(map[string]any)
	if store["destinationPath"] != "s3://peristera-backups/tenants/demo" {
		t.Errorf("destinationPath = %v", store["destinationPath"])
	}
	if store["endpointURL"] != "https://s3.fr-par.scw.cloud" {
		t.Errorf("endpointURL = %v", store["endpointURL"])
	}
	if b["retentionPolicy"] != "7d" {
		t.Errorf("retentionPolicy = %v", b["retentionPolicy"])
	}
}

func TestBackupsEnabled(t *testing.T) {
	if (&TenantReconciler{}).backupsEnabled() {
		t.Error("no bucket -> disabled")
	}
	if !(&TenantReconciler{BackupBucket: "b"}).backupsEnabled() {
		t.Error("bucket set -> enabled")
	}
	// Blob backups additionally need the age recipient: blobs restored
	// without an escrowed DEK are undecryptable, so half a backup is off.
	if (&TenantReconciler{BackupBucket: "b"}).blobBackupsEnabled() {
		t.Error("no age recipient -> blob backups disabled")
	}
	if !(&TenantReconciler{BackupBucket: "b", BackupAgeRecipient: "age1x"}).blobBackupsEnabled() {
		t.Error("bucket + recipient -> blob backups enabled")
	}
}

// ensureBackupCreds must converge an existing backup-s3 Secret to the
// controller's current credentials (#77): after a Scaleway key rotation the
// old create-only behavior left every tenant archiving WAL with the stale —
// possibly expired — key, silently.
func TestEnsureBackupCredsRotation(t *testing.T) {
	tn := &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{Name: "demo"}, Spec: v1alpha1.TenantSpec{Slug: "demo"}}
	stale := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: backupCredsSecret, Namespace: "tenant-demo"},
		Data: map[string][]byte{
			"ACCESS_KEY_ID":     []byte("OLDKEY"),
			"ACCESS_SECRET_KEY": []byte("OLDSECRET"),
		},
	}
	r := &TenantReconciler{
		Client:       fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(tn, stale).Build(),
		BackupBucket: "b", BackupS3KeyID: "NEWKEY", BackupS3Secret: "NEWSECRET",
	}
	ctx := context.Background()
	if err := r.ensureBackupCreds(ctx, tn, "tenant-demo"); err != nil {
		t.Fatalf("ensureBackupCreds: %v", err)
	}
	got := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: "tenant-demo", Name: backupCredsSecret}, got); err != nil {
		t.Fatal(err)
	}
	if string(got.Data["ACCESS_KEY_ID"]) != "NEWKEY" || string(got.Data["ACCESS_SECRET_KEY"]) != "NEWSECRET" {
		t.Errorf("secret not converged: %q/%q", got.Data["ACCESS_KEY_ID"], got.Data["ACCESS_SECRET_KEY"])
	}

	// Absent -> created with owner reference (the create path still works).
	r2 := &TenantReconciler{
		Client:       fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(tn).Build(),
		BackupBucket: "b", BackupS3KeyID: "K", BackupS3Secret: "S",
	}
	if err := r2.ensureBackupCreds(ctx, tn, "tenant-demo"); err != nil {
		t.Fatalf("create path: %v", err)
	}
	created := &corev1.Secret{}
	if err := r2.Get(ctx, client.ObjectKey{Namespace: "tenant-demo", Name: backupCredsSecret}, created); err != nil {
		t.Fatal(err)
	}
	if len(created.OwnerReferences) != 1 {
		t.Errorf("created secret must carry the tenant owner reference, got %v", created.OwnerReferences)
	}
}

// The blob-backup CronJob must copy the right PVC, escrow the right DEK to
// the configured recipient, authenticate from the in-namespace backup-s3
// Secret, and run with no ServiceAccount token and the app pods' non-root
// posture (uid 65532 = the blob PVC's fsGroup).
func TestBlobBackupCronJob(t *testing.T) {
	r := &TenantReconciler{
		BackupBucket: "peristera-backups", BackupEndpoint: "https://s3.fr-par.scw.cloud",
		BackupRegion: "fr-par", BackupAgeRecipient: "age1example",
		ImagePrefix: "ghcr.io/peristera-io/", ImageTag: "v1.2.3",
	}
	tn := &v1alpha1.Tenant{Spec: v1alpha1.TenantSpec{Slug: "demo"}}
	app := CatalogApp{Name: "kamara", NeedsBlob: true, NeedsDEK: true}
	cj := r.blobBackupCronJob(tn, "tenant-demo", app)

	if cj.Name != "kamara-blob-backup" || cj.Namespace != "tenant-demo" {
		t.Errorf("name/ns = %s/%s", cj.Name, cj.Namespace)
	}
	if cj.Spec.Schedule != "30 3 * * *" {
		t.Errorf("schedule = %q", cj.Spec.Schedule)
	}
	// Forbid + an unbounded hung Job would silently end all future backups.
	if d := cj.Spec.JobTemplate.Spec.ActiveDeadlineSeconds; d == nil || *d <= 0 {
		t.Error("backup job must carry an activeDeadlineSeconds")
	}
	pod := cj.Spec.JobTemplate.Spec.Template.Spec
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Error("backup pod must not mount a ServiceAccount token")
	}
	if pod.SecurityContext == nil || pod.SecurityContext.RunAsUser == nil || *pod.SecurityContext.RunAsUser != 65532 {
		t.Error("backup pod must run as uid 65532 (blob PVC fsGroup)")
	}
	c := pod.Containers[0]
	if c.Image != "ghcr.io/peristera-io/backup:v1.2.3" {
		t.Errorf("image = %q", c.Image)
	}
	env := map[string]string{}
	for _, e := range c.Env {
		env[e.Name] = e.Value
	}
	for k, want := range map[string]string{
		"BACKUP_BUCKET": "peristera-backups", "BACKUP_PREFIX": "tenants/demo",
		"BACKUP_REGION": "fr-par", "AGE_RECIPIENT": "age1example",
		"BLOB_DIR": "/mnt/blob", "ESCROW_DIR": "/mnt/escrow",
	} {
		if env[k] != want {
			t.Errorf("env %s = %q, want %q", k, env[k], want)
		}
	}
	if _, ok := env["HEARTBEAT_URL"]; ok {
		t.Error("no heartbeat configured -> no HEARTBEAT_URL env")
	}
	var blobRO, dekMounted bool
	for _, v := range pod.Volumes {
		if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == "kamara-blob" && v.PersistentVolumeClaim.ReadOnly {
			blobRO = true
		}
		if v.Secret != nil && v.Secret.SecretName == "kamara-dek" {
			dekMounted = true
		}
	}
	if !blobRO {
		t.Error("blob PVC must be mounted read-only")
	}
	if !dekMounted {
		t.Error("DEK Secret must be mounted for escrow")
	}

	// No DEK (a future blob-only app): no escrow half.
	cj = r.blobBackupCronJob(tn, "tenant-demo", CatalogApp{Name: "other", NeedsBlob: true})
	for _, e := range cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "ESCROW_DIR" {
			t.Error("app without DEK must not set ESCROW_DIR")
		}
	}

	// Heartbeat set -> passed through.
	r.BackupHeartbeat = "https://hc-ping.com/x"
	cj = r.blobBackupCronJob(tn, "tenant-demo", app)
	found := false
	for _, e := range cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "HEARTBEAT_URL" && e.Value == "https://hc-ping.com/x" {
			found = true
		}
	}
	if !found {
		t.Error("configured heartbeat must reach the job env")
	}
}
