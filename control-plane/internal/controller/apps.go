package controller

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

// CatalogApp is one entry of the tenant app catalog. The catalog stays a
// hardcoded Go slice (ADR-0013; catalog-as-data deferred) but an entry now
// declares its infrastructure needs, which the reconciler satisfies.
type CatalogApp struct {
	Name  string
	Image string
	Port  int32
	// NeedsDatabase provisions a dedicated database for the app inside the
	// tenant's CNPG cluster (database-per-app, ADR-0013/R30) and injects
	// its DSN as DATABASE_DSN.
	NeedsDatabase bool
	// NeedsOpenFGA gives the app the tenant's per-namespace OpenFGA
	// (ADR-0010) and injects OPENFGA_API_URL.
	NeedsOpenFGA bool
	// NeedsBlob provisions a per-tenant PersistentVolumeClaim for the app's
	// content-addressed blob store and mounts it at KAMARA_BLOB_DIR (the
	// first stateful-beyond-Postgres catalog app — Kamara, ADR-0013/SPEC §4).
	NeedsBlob bool
	// NeedsDEK generates a per-tenant data-encryption key as a Secret and
	// mounts it at KAMARA_DEK_FILE — the seed of the per-tenant key
	// hierarchy for at-rest chunk encryption (Kamara SPEC §6, ADR-0009 §6).
	NeedsDEK bool
	// Calls names the sibling catalog apps this app may make
	// server-to-server calls to. It is the single source of truth for the
	// service-topology graph (ADR-0016): the reconciler generates the
	// per-tenant NetworkPolicy (and, later, the Zitadel audience grants)
	// from it. Platform-uniform — the same graph in every tenant.
	//
	// For an External engine (below), Calls still feeds the network graph
	// (e.g. office calls kamara over WOPI, so kamara's NetworkPolicy admits
	// office) but does NOT provision an S2S identity — the engine authenticates
	// with WOPI access tokens, not RFC-8693 token exchange.
	Calls []string
	// Optional makes the app opt-in per tenant (ADR-0013 optional dimension,
	// ADR-0018): it is provisioned only when the tenant's Spec.Apps names it.
	// Always-on apps (Optional=false) are provisioned for every tenant.
	Optional bool
	// External marks a third-party engine (Collabora), not a Peristera Go app.
	// It has no OIDC client / database / OpenFGA / S2S contract; it is
	// provisioned by ensureOffice with its own image, capabilities, and WOPI
	// configuration. The standard app-provisioning path is skipped for it.
	External bool
}

// The images of our own apps are resolved from the reconciler's ImagePrefix +
// name + ImageTag (M7 s0), so the same catalog works in dev (peristera-<app>:dev,
// k3d-imported) and on the cloud (ghcr.io/peristera-io/<app>:<tag>, pulled).
// External engines (office) carry a literal upstream Image.
var catalog = []CatalogApp{
	{Name: "stub", Port: 5556},
	{Name: "ergonomos", Port: 5570,
		NeedsDatabase: true, NeedsOpenFGA: true, Calls: []string{"kamara"}},
	{Name: "kamara", Port: 5580,
		NeedsDatabase: true, NeedsOpenFGA: true, NeedsBlob: true, NeedsDEK: true},
	// The office engine (Collabora Online / CODE): opt-in per tenant, a
	// third-party engine reached over WOPI (ADR-0018). Calls kamara so the
	// network graph admits the editor→WOPI-host path; provisioned by
	// ensureOffice, not the standard path.
	{Name: "office", Image: "collabora/code:latest", Port: 9980,
		Optional: true, External: true, Calls: []string{"kamara"}},
}

// imageFor resolves a catalog app's container image. An app with a literal
// Image (external engines) uses it verbatim; ours is
// `<ImagePrefix><name>:<ImageTag>` — dev "peristera-kamara:dev",
// cloud "ghcr.io/peristera-io/kamara:<tag>".
func (r *TenantReconciler) imageFor(app CatalogApp) string {
	if app.Image != "" {
		return app.Image
	}
	prefix, tag := r.ImagePrefix, r.ImageTag
	if prefix == "" {
		prefix = "peristera-"
	}
	if tag == "" {
		tag = "dev"
	}
	return prefix + app.Name + ":" + tag
}

// tenantEnables reports whether the tenant has opted into the named optional
// app (ADR-0018). Always-on apps ignore this.
func tenantEnables(tenant *v1alpha1.Tenant, name string) bool {
	for _, a := range tenant.Spec.Apps {
		if a == name {
			return true
		}
	}
	return false
}

// callersOf returns the catalog apps that declare target in their Calls —
// the apps allowed to make server-to-server calls to it (ADR-0016).
func callersOf(target string) []string {
	var out []string
	for _, a := range catalog {
		for _, c := range a.Calls {
			if c == target {
				out = append(out, a.Name)
			}
		}
	}
	return out
}

// Blob and DEK mount points inside the app pod; the app reads these paths
// from KAMARA_BLOB_DIR / KAMARA_DEK_FILE.
const (
	blobMountPath = "/mnt/kamara-blob"
	dekMountPath  = "/mnt/kamara-dek"
	dekFileName   = "dek"
	// S2S client-key mount (ADR-0017): the app-key JSON for lib/svcauth.
	s2sKeyMountPath = "/mnt/s2s"
	s2sKeyFileName  = "key.json"
)

// anyAppNeedsOpenFGA reports whether the catalog requires the per-tenant
// OpenFGA to be provisioned.
func anyAppNeedsOpenFGA() bool {
	for _, a := range catalog {
		if a.NeedsOpenFGA {
			return true
		}
	}
	return false
}

// ensureApps deploys every catalog app into the tenant namespace with the
// catalog env contract (OIDC_ISSUER, OIDC_CLIENT_ID, PUBLIC_URL,
// LISTEN_ADDR). Create-only for M2: drift correction and upgrades are the
// 2027 control-plane alpha.
func (r *TenantReconciler) ensureApps(ctx context.Context, tenant *v1alpha1.Tenant, ns string) error {
	// Per-tenant OpenFGA is shared by every app that needs it; provision it
	// once before the apps that depend on it (ADR-0010/0013).
	if anyAppNeedsOpenFGA() {
		if err := r.ensureOpenFGA(ctx, tenant, ns); err != nil {
			return err
		}
	}

	// Each app is its own OIDC client (own redirect URIs), so register one
	// per app rather than sharing the tenant's primary client.
	orgID, err := r.IAM.FirstOrgID(ctx, tenant.Status.Issuer)
	if err != nil {
		return err
	}

	// Service-to-service auth (ADR-0017): the tenant's app project id scopes
	// exchange audiences for any app that calls another. The plain exchange
	// (subject re-scoping; azp=service is the actor) needs no instance-wide
	// impersonation setting — that is only for explicit actor_token
	// delegation, which we do not use, so we deliberately do NOT enable it
	// (least privilege; see ADR-0017 and the s2 review).
	projectID, err := r.IAM.ProjectID(ctx, tenant.Status.Issuer, orgID)
	if err != nil {
		return err
	}

	for _, app := range catalog {
		// Optional apps (ADR-0018) are provisioned only when the tenant opts in.
		if app.Optional && !tenantEnables(tenant, app.Name) {
			continue
		}
		// External engines (Collabora) don't use the Peristera OIDC/DB/OpenFGA
		// contract; provision them on their own path and skip the rest.
		if app.External {
			if err := r.ensureOffice(ctx, tenant, ns, app); err != nil {
				return err
			}
			continue
		}

		host := fmt.Sprintf("%s.%s", app.Name, r.tenantDomain(tenant))
		publicURL := r.publicURL(host)
		labels := map[string]string{
			"app.kubernetes.io/name":       app.Name,
			"app.kubernetes.io/managed-by": "peristera-control-plane",
		}

		clientID, err := r.IAM.EnsureWebApp(ctx, tenant.Status.Issuer, orgID, app.Name,
			[]string{publicURL + "/auth/callback"}, []string{publicURL + "/"})
		if err != nil {
			return fmt.Errorf("ensuring OIDC client for %s: %w", app.Name, err)
		}

		env := []corev1.EnvVar{
			{Name: "OIDC_ISSUER", Value: tenant.Status.Issuer},
			{Name: "OIDC_CLIENT_ID", Value: clientID},
			{Name: "PUBLIC_URL", Value: publicURL},
			{Name: "LISTEN_ADDR", Value: fmt.Sprintf(":%d", app.Port)},
		}
		if app.NeedsDatabase {
			if err := r.ensureAppDatabase(ctx, tenant, ns, app.Name); err != nil {
				return err
			}
			env = append(env, corev1.EnvVar{
				Name: "DATABASE_DSN",
				ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: app.Name + "-db-dsn"},
					Key:                  "dsn",
				}},
			})
		}
		if app.NeedsOpenFGA {
			env = append(env, corev1.EnvVar{
				Name:  "OPENFGA_API_URL",
				Value: fmt.Sprintf("http://openfga.%s.svc.cluster.local:8080", ns),
			})
			// The per-tenant OpenFGA preshared key (ADR-0016); lib/authz
			// sends it as a bearer token.
			env = append(env, corev1.EnvVar{
				Name: "OPENFGA_API_TOKEN",
				ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: openFGAKeySecret},
					Key:                  openFGAKeyField,
				}},
			})
		}

		// Stateful extras (Kamara): a per-tenant blob PVC and a per-tenant
		// DEK Secret, mounted into the pod. Provisioned before the
		// Deployment references them (the pod stays Pending until they bind).
		var volumes []corev1.Volume
		var mounts []corev1.VolumeMount
		if app.NeedsBlob {
			if err := r.ensureBlob(ctx, tenant, ns, app.Name); err != nil {
				return err
			}
			env = append(env, corev1.EnvVar{Name: "KAMARA_BLOB_DIR", Value: blobMountPath})
			volumes = append(volumes, corev1.Volume{
				Name: "blob",
				VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: app.Name + "-blob",
				}},
			})
			mounts = append(mounts, corev1.VolumeMount{Name: "blob", MountPath: blobMountPath})
		}
		if app.NeedsDEK {
			if err := r.ensureDEK(ctx, tenant, ns, app.Name); err != nil {
				return err
			}
			env = append(env, corev1.EnvVar{Name: "KAMARA_DEK_FILE", Value: dekMountPath + "/" + dekFileName})
			volumes = append(volumes, corev1.Volume{
				Name: "dek",
				VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
					SecretName: app.Name + "-dek",
					Items:      []corev1.KeyToPath{{Key: dekFileName, Path: dekFileName}},
				}},
			})
			mounts = append(mounts, corev1.VolumeMount{Name: "dek", MountPath: dekMountPath, ReadOnly: true})
		}

		// S2S caller identity (ADR-0017): an app that calls another gets its
		// own confidential OIDC "S2S client" + key (mounted for lib/svcauth),
		// plus the project id so lib/oidcrp can request the project-audience
		// scope on the user's token and svcauth can exchange it.
		if len(app.Calls) > 0 {
			if err := r.ensureS2SIdentity(ctx, tenant, ns, orgID, app.Name); err != nil {
				return err
			}
			env = append(env,
				corev1.EnvVar{Name: "OIDC_PROJECT_ID", Value: projectID},
				corev1.EnvVar{Name: "SVCAUTH_KEY_FILE", Value: s2sKeyMountPath + "/" + s2sKeyFileName},
			)
			// The in-cluster URL of each callee this app may call
			// (e.g. KAMARA_URL) — reachable per the ADR-0016 NetworkPolicy.
			for _, callee := range app.Calls {
				env = append(env, corev1.EnvVar{
					Name:  strings.ToUpper(callee) + "_URL",
					Value: fmt.Sprintf("http://%s.%s.svc.cluster.local", callee, ns),
				})
			}
			volumes = append(volumes, corev1.Volume{
				Name: "s2s-key",
				VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
					SecretName: app.Name + "-s2s-key",
					Items:      []corev1.KeyToPath{{Key: s2sKeyFileName, Path: s2sKeyFileName}},
				}},
			})
			mounts = append(mounts, corev1.VolumeMount{Name: "s2s-key", MountPath: s2sKeyMountPath, ReadOnly: true})
		}

		// Office editing (ADR-0018): Kamara embeds the tenant's Collabora when
		// the office app is enabled. It needs the engine's public URL (to fetch
		// WOPI discovery and build the editor iframe) and its own in-cluster
		// base (the WOPISrc the engine fetches back, intra-namespace). Injected
		// only into kamara, only when office is on.
		if app.Name == "kamara" && tenantEnables(tenant, "office") {
			officeURL := r.publicURL("office." + r.tenantDomain(tenant))
			env = append(env,
				corev1.EnvVar{Name: "OFFICE_URL", Value: officeURL},
				corev1.EnvVar{Name: "WOPI_SRC_BASE", Value: fmt.Sprintf("http://kamara.%s.svc.cluster.local", ns)},
			)
		}

		// A blob-backed app owns a ReadWriteOnce PVC (#30): the default rolling
		// update would start the new pod while the old still holds the volume,
		// so it either wedges on multi-attach or (same node) two writers race
		// the chunk store. Recreate (stop-then-start) + a single replica keeps
		// exactly one writer. Stateless apps keep the default rolling update.
		replicas := int32(1)
		deploySpec := appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:            app.Name,
						Image:           r.imageFor(app),
						ImagePullPolicy: corev1.PullIfNotPresent,
						Ports:           []corev1.ContainerPort{{ContainerPort: app.Port}},
						Env:             env,
						VolumeMounts:    mounts,
					}},
					Volumes: volumes,
				},
			},
		}
		if app.NeedsBlob {
			deploySpec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
		}
		deploy := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: ns, Labels: labels},
			Spec:       deploySpec,
		}
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: ns, Labels: labels},
			Spec: corev1.ServiceSpec{
				Selector: labels,
				Ports: []corev1.ServicePort{{
					Port: 80, TargetPort: intstr.FromInt32(app.Port),
				}},
			},
		}
		pathType := networkingv1.PathTypePrefix
		ingressClass := "traefik"
		ing := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: ns, Labels: labels, Annotations: r.ingressAnnotations()},
			Spec: networkingv1.IngressSpec{
				IngressClassName: &ingressClass,
				TLS:              r.ingressTLS(host, app.Name+"-tls"),
				Rules: []networkingv1.IngressRule{{
					Host: host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{{
								Path: "/", PathType: &pathType,
								Backend: networkingv1.IngressBackend{
									Service: &networkingv1.IngressServiceBackend{
										Name: app.Name,
										Port: networkingv1.ServiceBackendPort{Number: 80},
									},
								},
							}},
						},
					},
				}},
			},
		}

		for _, obj := range []client.Object{deploy, svc, ing} {
			if err := r.createIfAbsent(ctx, tenant, obj); err != nil {
				return err
			}
		}
	}

	// Enforce the service-topology allowlist once the apps exist (ADR-0016).
	return r.ensureNetworkPolicies(ctx, tenant, ns)
}

func (r *TenantReconciler) createIfAbsent(ctx context.Context, tenant *v1alpha1.Tenant, obj client.Object) error {
	err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	if err := controllerutil.SetControllerReference(tenant, obj, r.Scheme()); err != nil {
		return err
	}
	return r.Create(ctx, obj)
}
