package controller

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"

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
}

var catalog = []CatalogApp{
	{Name: "stub", Image: "peristera-stub:dev", Port: 5556},
	{Name: "ergonomos", Image: "peristera-ergonomos:dev", Port: 5570,
		NeedsDatabase: true, NeedsOpenFGA: true},
}

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

	for _, app := range catalog {
		host := fmt.Sprintf("%s.%s", app.Name, r.tenantDomain(tenant))
		publicURL := fmt.Sprintf("http://%s:%s", host, r.ExternalPort)
		labels := map[string]string{
			"app.kubernetes.io/name":       app.Name,
			"app.kubernetes.io/managed-by": "peristera-control-plane",
		}

		env := []corev1.EnvVar{
			{Name: "OIDC_ISSUER", Value: tenant.Status.Issuer},
			{Name: "OIDC_CLIENT_ID", Value: tenant.Status.ClientID},
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
		}

		deploy := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: ns, Labels: labels},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: labels},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: labels},
					Spec: corev1.PodSpec{Containers: []corev1.Container{{
						Name:            app.Name,
						Image:           app.Image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Ports:           []corev1.ContainerPort{{ContainerPort: app.Port}},
						Env:             env,
					}}},
				},
			},
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
			ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: ns, Labels: labels},
			Spec: networkingv1.IngressSpec{
				IngressClassName: &ingressClass,
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
	return nil
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

// ensureInitialAdmin creates the tenant's first human user and hands the
// generated credentials over as a Secret in the tenant namespace — the
// MSP's handover artifact. Skipped entirely once the Secret exists.
func (r *TenantReconciler) ensureInitialAdmin(ctx context.Context, tenant *v1alpha1.Tenant, ns string) error {
	sec := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: "initial-admin"}, sec)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	password, err := generatePassword()
	if err != nil {
		return err
	}
	issuer := tenant.Status.Issuer
	orgID, err := r.IAM.FirstOrgID(ctx, issuer)
	if err != nil {
		return err
	}
	email := fmt.Sprintf("admin@%s", r.tenantDomain(tenant))
	if err := r.IAM.EnsureHumanUser(ctx, issuer, orgID, "admin", email, password); err != nil {
		return err
	}
	sec = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "initial-admin", Namespace: ns},
		StringData: map[string]string{"username": "admin", "password": password},
	}
	if err := controllerutil.SetControllerReference(tenant, sec, r.Scheme()); err != nil {
		return err
	}
	return r.Create(ctx, sec)
}

// generatePassword returns a random password that satisfies Zitadel's
// default complexity policy (upper, lower, digit, symbol).
func generatePassword() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "Aa1!" + base64.RawURLEncoding.EncodeToString(raw), nil
}
