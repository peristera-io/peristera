package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

// ensureOffice provisions the Collabora Online (CODE) engine into the tenant
// namespace (ADR-0018). It is a third-party engine, not a Peristera app: no
// OIDC client, database, OpenFGA, or S2S identity. Editing is authorized by
// per-session WOPI access tokens Kamara mints (s2), so the only trust the
// engine needs is (a) the WOPI host allow-list, pinned to the tenant's own
// in-cluster Kamara, and (b) frame-ancestors, pinned to Kamara's public host
// so its /edit page may embed the editor iframe. Create-only, like the rest
// of provisioning.
//
// Dev-only notes (hardening tracked for the SaaS era): runs plain HTTP behind
// Traefik (no TLS termination flag); admin console uses a fixed dev password;
// and the pod carries the Linux capabilities coolwsd needs to build its
// per-document chroot jails.
func (r *TenantReconciler) ensureOffice(ctx context.Context, tenant *v1alpha1.Tenant, ns string, app CatalogApp) error {
	labels := map[string]string{
		"app.kubernetes.io/name":       app.Name,
		"app.kubernetes.io/managed-by": "peristera-control-plane",
	}
	host := fmt.Sprintf("%s.%s", app.Name, r.tenantDomain(tenant))

	// The WOPI host is the tenant's own Kamara, reached in-cluster so document
	// traffic never leaves the namespace (intra-tenant isolation, R68). The
	// browser embeds the editor from Kamara's public origin, so that origin
	// must be an allowed frame ancestor.
	kamaraInCluster := fmt.Sprintf("http://kamara.%s.svc.cluster.local", ns)
	kamaraOrigin := r.publicURL("kamara." + r.tenantDomain(tenant))

	env := []corev1.EnvVar{
		// WOPI host allow-list: only this tenant's in-cluster Kamara.
		{Name: "aliasgroup1", Value: kamaraInCluster},
		// Plain HTTP behind Traefik; allow embedding only from Kamara's origin.
		{Name: "extra_params", Value: fmt.Sprintf(
			"--o:ssl.enable=false --o:ssl.termination=false --o:net.frame_ancestors=%s", kamaraOrigin)},
		{Name: "DONT_GEN_SSL_CERT", Value: "1"},
		{Name: "username", Value: "admin"},
		{Name: "password", Value: "admin"},
	}

	replicas := int32(1)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: ns, Labels: labels},
		Spec: appsv1.DeploymentSpec{
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
						SecurityContext: &corev1.SecurityContext{
							// coolwsd builds a chroot jail per open document.
							Capabilities: &corev1.Capabilities{Add: []corev1.Capability{
								"MKNOD", "SYS_CHROOT", "FOWNER", "CHOWN", "SYS_ADMIN",
							}},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("250m"),
								corev1.ResourceMemory: resource.MustParse("640Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("2"),
								corev1.ResourceMemory: resource.MustParse("2Gi"),
							},
						},
					}},
				},
			},
		},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: ns, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports:    []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt32(app.Port)}},
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
	return nil
}
