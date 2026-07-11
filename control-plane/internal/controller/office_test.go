package controller

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

func officeEnv(d *appsv1.Deployment) map[string]string {
	m := map[string]string{}
	for _, e := range d.Spec.Template.Spec.Containers[0].Env {
		m[e.Name] = e.Value
	}
	return m
}

// The office Deployment must be built hardened (R95, #48): no admin credentials,
// admin console disabled, RuntimeDefault seccomp, and the jailing capabilities
// retained. ssl.termination is the one setting that flips with tlsEnabled().
func TestOfficeDeploymentHardening(t *testing.T) {
	app := CatalogApp{Name: "office", Port: 9980}
	labels := map[string]string{"app.kubernetes.io/name": "office"}
	tn := &v1alpha1.Tenant{Spec: v1alpha1.TenantSpec{Slug: "demo"}}

	cloud := &TenantReconciler{BaseDomain: "peristera.app", TLSIssuer: "letsencrypt-prod", URLScheme: "https", ExternalPort: "443"}
	dep := cloud.officeDeployment(tn, "peristera-tenant-demo", app, labels)
	env := officeEnv(dep)

	// The WOPI allow-list pins the tenant's own in-cluster Kamara — the
	// intra-tenant isolation control (R68), the most load-bearing env var here.
	if got := env["aliasgroup1"]; got != "http://kamara.peristera-tenant-demo.svc.cluster.local" {
		t.Errorf("WOPI allow-list must pin the in-cluster Kamara, got %q", got)
	}

	// No admin credentials leak into the pod.
	if _, ok := env["username"]; ok {
		t.Error("office pod must not carry a username env var")
	}
	if _, ok := env["password"]; ok {
		t.Error("office pod must not carry a password env var")
	}
	// Admin console disabled.
	if !strings.Contains(env["extra_params"], "--o:admin_console.enable=false") {
		t.Errorf("admin console not disabled: %q", env["extra_params"])
	}
	// Cloud is behind TLS-terminating Traefik.
	if !strings.Contains(env["extra_params"], "--o:ssl.termination=true") {
		t.Errorf("cloud must set ssl.termination=true: %q", env["extra_params"])
	}
	// Frame-ancestors pinned to the tenant's public Kamara origin.
	if !strings.Contains(env["extra_params"], "--o:net.frame_ancestors=https://kamara.demo.peristera.app") {
		t.Errorf("frame_ancestors not pinned to kamara origin: %q", env["extra_params"])
	}

	// RuntimeDefault seccomp at the pod level.
	sc := dep.Spec.Template.Spec.SecurityContext
	if sc == nil || sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("office pod must run under RuntimeDefault seccomp, got %+v", sc)
	}

	// Jailing capabilities retained in full (dropping any disables chroot jails).
	caps := dep.Spec.Template.Spec.Containers[0].SecurityContext.Capabilities.Add
	for _, want := range []corev1.Capability{"MKNOD", "SYS_CHROOT", "FOWNER", "CHOWN", "SYS_ADMIN"} {
		if !hasCap(caps, want) {
			t.Errorf("office container must retain jailing capability %s, got %v", want, caps)
		}
	}

	// Dev flips only ssl.termination.
	dev := &TenantReconciler{BaseDomain: "peristera.app"}
	devEnv := officeEnv(dev.officeDeployment(tn, "peristera-tenant-demo", app, labels))
	if !strings.Contains(devEnv["extra_params"], "--o:ssl.termination=false") {
		t.Errorf("dev must set ssl.termination=false: %q", devEnv["extra_params"])
	}
	if !strings.Contains(devEnv["extra_params"], "--o:admin_console.enable=false") {
		t.Errorf("dev must also disable the admin console: %q", devEnv["extra_params"])
	}
}

func hasCap(caps []corev1.Capability, want corev1.Capability) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}
