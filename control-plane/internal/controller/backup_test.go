package controller

import "testing"

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
}
