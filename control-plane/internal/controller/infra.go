package controller

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

// blobVolumeSize is the per-tenant blob PVC request. Dev sizing; a
// per-plan size and a logical quota are a SaaS-era concern (issue #27).
const blobVolumeSize = "10Gi"

// dekKeySize is the data-encryption-key length (XChaCha20-Poly1305 key,
// crypto.KeySize). Kept here to avoid the control plane importing Kamara.
const dekKeySize = 32

// cnpgDatabaseGVK is CloudNativePG's declarative managed-database resource
// (a database inside an existing Cluster).
var cnpgDatabaseGVK = schema.GroupVersionKind{
	Group: "postgresql.cnpg.io", Version: "v1", Kind: "Database",
}

// openFGAImage is pinned (not :latest) so the tested migrate/server
// versions can't silently diverge on a pod reschedule.
const openFGAImage = "openfga/openfga:v1.8.0"

// NOTE: app provisioning here is create-only (createIfAbsent). Not just the
// app Deployment — the DSN secret, the OpenFGA Deployment, and their env
// are all frozen at first create. Changing an image, an env value, or a
// rotated DB password requires recreating the resource (M3 scope; drift
// correction is the 2027 control-plane alpha).

// ensureAppDatabase provisions a dedicated database for an app inside the
// tenant's CNPG cluster (database-per-app, ADR-0013/R30) and writes a
// secret carrying its DSN, assembled from the cluster's app credentials.
// The apps share the cluster's "app" role but get isolated databases —
// per-app roles are a later hardening.
func (r *TenantReconciler) ensureAppDatabase(ctx context.Context, tenant *v1alpha1.Tenant, ns, app string) error {
	db := &unstructured.Unstructured{}
	db.SetGroupVersionKind(cnpgDatabaseGVK)
	name := app + "-db"
	err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, db)
	if apierrors.IsNotFound(err) {
		db = &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "postgresql.cnpg.io/v1",
			"kind":       "Database",
			"metadata":   map[string]any{"name": name, "namespace": ns},
			"spec": map[string]any{
				"cluster": map[string]any{"name": "db"},
				"name":    app, // the database name
				"owner":   "app",
			},
		}}
		db.SetGroupVersionKind(cnpgDatabaseGVK)
		if err := r.setOwnerAndCreate(ctx, tenant, db); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return r.ensureDSNSecret(ctx, tenant, ns, app)
}

// ensureDSNSecret assembles the app's DSN from the tenant cluster's app
// credentials (secret db-app) pointed at the app's own database, and
// stores it as <app>-db-dsn.
func (r *TenantReconciler) ensureDSNSecret(ctx context.Context, tenant *v1alpha1.Tenant, ns, app string) error {
	name := app + "-db-dsn"
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &corev1.Secret{}); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}

	creds := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: "db-app"}, creds); err != nil {
		return fmt.Errorf("reading cluster app credentials: %w", err)
	}
	pw := string(creds.Data["password"])
	// Build via net/url so the password is percent-encoded — CNPG's default
	// password alphabet is URL-safe today, but an externally-set/rotated
	// password containing @ / : ? # would otherwise corrupt the DSN.
	u := url.URL{
		Scheme:   "postgresql",
		User:     url.UserPassword("app", pw),
		Host:     fmt.Sprintf("db-rw.%s.svc.cluster.local:5432", ns),
		Path:     "/" + app,
		RawQuery: "sslmode=require",
	}
	dsn := u.String()

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		StringData: map[string]string{"dsn": dsn},
	}
	return r.setOwnerAndCreate(ctx, tenant, sec)
}

// ensureOpenFGA deploys the tenant's per-namespace OpenFGA (ADR-0010),
// backed by its own database in the tenant's CNPG cluster. An init
// container runs `openfga migrate` before the server starts.
func (r *TenantReconciler) ensureOpenFGA(ctx context.Context, tenant *v1alpha1.Tenant, ns string) error {
	if err := r.ensureAppDatabase(ctx, tenant, ns, "openfga"); err != nil {
		return err
	}
	if err := r.ensureOpenFGAKey(ctx, tenant, ns); err != nil {
		return err
	}
	labels := map[string]string{
		"app.kubernetes.io/name":       "openfga",
		"app.kubernetes.io/managed-by": "peristera-control-plane",
	}
	dsnEnv := corev1.EnvVar{
		Name: "OPENFGA_DATASTORE_URI",
		ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "openfga-db-dsn"},
			Key:                  "dsn",
		}},
	}
	engine := corev1.EnvVar{Name: "OPENFGA_DATASTORE_ENGINE", Value: "postgres"}
	// Preshared-key authn (ADR-0016, folds #18): OpenFGA rejects API calls
	// without the tenant key. The NetworkPolicy already restricts *who* can
	// reach the port (same-ns apps); this stops a reachable-but-unauthorized
	// caller from reading/writing tuples. Health probes stay unauthenticated
	// in OpenFGA, so the readiness probe below still works. Server-only —
	// `openfga migrate` talks to the DB, not the API.
	authnMethod := corev1.EnvVar{Name: "OPENFGA_AUTHN_METHOD", Value: "preshared"}
	authnKeys := corev1.EnvVar{
		Name: "OPENFGA_AUTHN_PRESHARED_KEYS",
		ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: openFGAKeySecret},
			Key:                  openFGAKeyField,
		}},
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "openfga", Namespace: ns, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{{
						Name:    "migrate",
						Image:   openFGAImage,
						Command: []string{"/openfga", "migrate"},
						Env:     []corev1.EnvVar{engine, dsnEnv},
					}},
					Containers: []corev1.Container{{
						Name:    "openfga",
						Image:   openFGAImage,
						Command: []string{"/openfga", "run"},
						Env:     []corev1.EnvVar{engine, dsnEnv, authnMethod, authnKeys},
						Ports: []corev1.ContainerPort{
							{Name: "http", ContainerPort: 8080},
							{Name: "grpc", ContainerPort: 8081},
						},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/healthz", Port: intstr.FromInt32(8080),
								},
							},
						},
					}},
				},
			},
		},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "openfga", Namespace: ns, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(8080)},
				{Name: "grpc", Port: 8081, TargetPort: intstr.FromInt32(8081)},
			},
		},
	}
	for _, obj := range []client.Object{deploy, svc} {
		if err := r.createIfAbsent(ctx, tenant, obj); err != nil {
			return err
		}
	}
	return nil
}

// ensureBlob provisions a per-tenant PersistentVolumeClaim for an app's
// content-addressed blob store (Kamara). Create-only, like the rest of app
// provisioning; the tenant owns it so off-boarding garbage-collects it.
func (r *TenantReconciler) ensureBlob(ctx context.Context, tenant *v1alpha1.Tenant, ns, app string) error {
	name := app + "-blob"
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &corev1.PersistentVolumeClaim{}); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(blobVolumeSize)},
			},
		},
	}
	return r.setOwnerAndCreate(ctx, tenant, pvc)
}

// openFGAKeySecret / openFGAKeyField name the per-tenant OpenFGA
// preshared-key Secret. OpenFGA reads it as OPENFGA_AUTHN_PRESHARED_KEYS;
// apps that talk to OpenFGA get the same value as OPENFGA_API_TOKEN.
const (
	openFGAKeySecret = "openfga-authn-key"
	openFGAKeyField  = "token"
)

// ensureOpenFGAKey generates the per-tenant OpenFGA preshared key as a
// Secret (ADR-0016). Generated once, never rotated here (rotation is a later
// hardening); the tenant owns it so off-boarding garbage-collects it.
func (r *TenantReconciler) ensureOpenFGAKey(ctx context.Context, tenant *v1alpha1.Tenant, ns string) error {
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: openFGAKeySecret}, &corev1.Secret{}); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("generating OpenFGA key: %w", err)
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: openFGAKeySecret, Namespace: ns},
		StringData: map[string]string{openFGAKeyField: base64.RawURLEncoding.EncodeToString(key)},
	}
	return r.setOwnerAndCreate(ctx, tenant, sec)
}

// ensureS2SIdentity provisions an app's service-to-service caller identity
// (ADR-0017): a confidential OIDC "S2S client" in the tenant's project (the
// token-exchange grant + private_key_jwt) and a JSON app key, stored as a
// Secret that lib/svcauth reads to sign client assertions. Create-only: the
// key is generated once (Zitadel returns the private key only at creation),
// so we skip if the Secret already exists.
func (r *TenantReconciler) ensureS2SIdentity(ctx context.Context, tenant *v1alpha1.Tenant, ns, orgID, app string) error {
	name := app + "-s2s-key"
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &corev1.Secret{}); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	_, appID, err := r.IAM.EnsureS2SClient(ctx, tenant.Status.Issuer, orgID, app+"-s2s")
	if err != nil {
		return fmt.Errorf("ensuring S2S client for %s: %w", app, err)
	}
	keyJSON, err := r.IAM.AddAppKey(ctx, tenant.Status.Issuer, orgID, appID)
	if err != nil {
		return fmt.Errorf("adding S2S key for %s: %w", app, err)
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Data:       map[string][]byte{s2sKeyFileName: keyJSON},
	}
	return r.setOwnerAndCreate(ctx, tenant, sec)
}

// ensureDEK generates a per-tenant data-encryption key as a Secret (Kamara,
// ADR-0009 §6). The key is stored base64-encoded so the mounted file is
// text — no binary/trailing-newline ambiguity when the app reads it. This
// is the seed of the per-tenant key hierarchy; deleting the Secret is
// whole-tenant crypto-shredding. Generated once, never rotated here (key
// rotation is a later hardening).
func (r *TenantReconciler) ensureDEK(ctx context.Context, tenant *v1alpha1.Tenant, ns, app string) error {
	name := app + "-dek"
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &corev1.Secret{}); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	key := make([]byte, dekKeySize)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("generating DEK: %w", err)
	}
	// Stored base64 in StringData so the mounted file is a single base64
	// string (44 chars, no binary/newline ambiguity). Kamara's decodeKey
	// (cmd/kamara/main.go) trims and base64-decodes it — keep the two in
	// sync: the mounted file must be one base64 layer, not raw bytes.
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		StringData: map[string]string{dekFileName: base64.StdEncoding.EncodeToString(key)},
	}
	return r.setOwnerAndCreate(ctx, tenant, sec)
}

// setOwnerAndCreate stamps the tenant as owner (for GC on off-boarding)
// and creates the object.
func (r *TenantReconciler) setOwnerAndCreate(ctx context.Context, tenant *v1alpha1.Tenant, obj client.Object) error {
	if err := controllerutil.SetControllerReference(tenant, obj, r.Scheme()); err != nil {
		return err
	}
	return r.Create(ctx, obj)
}
