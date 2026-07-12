package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

// enabledCallersOf must admit an optional caller (office) only when the tenant
// has enabled it, while always admitting the always-on caller (ergonomos).
func TestEnabledCallersOf(t *testing.T) {
	off := &v1alpha1.Tenant{Spec: v1alpha1.TenantSpec{Slug: "demo"}}
	if got := enabledCallersOf(off, "kamara"); !equalStringSet(got, []string{"ergonomos"}) {
		t.Errorf("office disabled: kamara callers = %v, want [ergonomos]", got)
	}
	on := &v1alpha1.Tenant{Spec: v1alpha1.TenantSpec{Slug: "demo", Apps: []string{"office"}}}
	if got := enabledCallersOf(on, "kamara"); !equalStringSet(got, []string{"ergonomos", "office"}) {
		t.Errorf("office enabled: kamara callers = %v, want [ergonomos office]", got)
	}
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func peer(app string) networkingv1.NetworkPolicyPeer {
	return networkingv1.NetworkPolicyPeer{PodSelector: nameSelector(app)}
}

func npKamara(ns string, callers ...string) *networkingv1.NetworkPolicy {
	from := []networkingv1.NetworkPolicyPeer{
		{NamespaceSelector: kubeSystemSelector(), PodSelector: nameSelector("traefik")},
	}
	for _, c := range callers {
		from = append(from, peer(c))
	}
	return &networkingv1.NetworkPolicy{
		ObjectMeta: policyMeta("np-kamara", ns),
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: *nameSelector("kamara"),
			Ingress:     []networkingv1.NetworkPolicyIngressRule{{From: from}},
		},
	}
}

func kamaraDeploy(ns string, env ...corev1.EnvVar) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "kamara", Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "kamara", Env: env}}},
			},
		},
	}
}

// Disabling office tears down its resources and removes it from Kamara's wiring.
func TestReconcileOptionalApps_Teardown(t *testing.T) {
	ns := "tenant-demo"
	tenant := &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{Name: "demo"}, Spec: v1alpha1.TenantSpec{Slug: "demo"}} // office NOT in Apps
	objs := []client.Object{
		tenant,
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "office", Namespace: ns}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "office", Namespace: ns}},
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "office", Namespace: ns}},
		&networkingv1.NetworkPolicy{ObjectMeta: policyMeta("np-office", ns)},
		npKamara(ns, "ergonomos", "office"),
		kamaraDeploy(ns,
			corev1.EnvVar{Name: "OIDC_ISSUER", Value: "x"},
			corev1.EnvVar{Name: "OFFICE_URL", Value: "http://office.demo.peristera.app"},
			corev1.EnvVar{Name: "WOPI_SRC_BASE", Value: "http://kamara." + ns + ".svc.cluster.local"},
		),
	}
	r := &TenantReconciler{
		Client:     fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).Build(),
		BaseDomain: "peristera.app",
	}
	if err := r.reconcileOptionalApps(context.Background(), tenant, ns); err != nil {
		t.Fatal(err)
	}

	// Office resources gone.
	for _, o := range []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "office", Namespace: ns}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "office", Namespace: ns}},
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "office", Namespace: ns}},
		&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "np-office", Namespace: ns}},
	} {
		if err := r.Get(context.Background(), client.ObjectKeyFromObject(o), o); !apierrors.IsNotFound(err) {
			t.Errorf("expected %T office resource deleted, got err=%v", o, err)
		}
	}

	// np-kamara no longer admits office.
	np := &networkingv1.NetworkPolicy{}
	if err := r.Get(context.Background(), client.ObjectKey{Name: "np-kamara", Namespace: ns}, np); err != nil {
		t.Fatal(err)
	}
	if !equalStringSet(callerNames(np.Spec.Ingress[0].From), []string{"ergonomos"}) {
		t.Errorf("np-kamara callers = %v, want [ergonomos]", callerNames(np.Spec.Ingress[0].From))
	}

	// Kamara env stripped of the office vars.
	dep := &appsv1.Deployment{}
	if err := r.Get(context.Background(), client.ObjectKey{Name: "kamara", Namespace: ns}, dep); err != nil {
		t.Fatal(err)
	}
	for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
		if e.Name == "OFFICE_URL" || e.Name == "WOPI_SRC_BASE" {
			t.Errorf("kamara env must not carry %s after office disabled", e.Name)
		}
	}
}

// Enabling office adds it to Kamara's wiring (np-kamara + env) without touching
// the teardown path. Office resource creation itself is ensureApps' job.
func TestReconcileOptionalApps_EnableRewiresKamara(t *testing.T) {
	ns := "tenant-demo"
	tenant := &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{Name: "demo"}, Spec: v1alpha1.TenantSpec{Slug: "demo", Apps: []string{"office"}}}
	objs := []client.Object{
		tenant,
		npKamara(ns, "ergonomos"), // office not yet admitted
		kamaraDeploy(ns, corev1.EnvVar{Name: "OIDC_ISSUER", Value: "x"}),
	}
	r := &TenantReconciler{
		Client:       fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).Build(),
		BaseDomain:   "peristera.app",
		URLScheme:    "https",
		ExternalPort: "443",
	}
	if err := r.reconcileOptionalApps(context.Background(), tenant, ns); err != nil {
		t.Fatal(err)
	}

	np := &networkingv1.NetworkPolicy{}
	if err := r.Get(context.Background(), client.ObjectKey{Name: "np-kamara", Namespace: ns}, np); err != nil {
		t.Fatal(err)
	}
	if !equalStringSet(callerNames(np.Spec.Ingress[0].From), []string{"ergonomos", "office"}) {
		t.Errorf("np-kamara callers = %v, want [ergonomos office]", callerNames(np.Spec.Ingress[0].From))
	}

	dep := &appsv1.Deployment{}
	if err := r.Get(context.Background(), client.ObjectKey{Name: "kamara", Namespace: ns}, dep); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, e := range dep.Spec.Template.Spec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	if env["OFFICE_URL"] != "https://office.demo.peristera.app" {
		t.Errorf("OFFICE_URL = %q, want https://office.demo.peristera.app", env["OFFICE_URL"])
	}
	if env["WOPI_SRC_BASE"] != "http://kamara."+ns+".svc.cluster.local" {
		t.Errorf("WOPI_SRC_BASE = %q", env["WOPI_SRC_BASE"])
	}
}

// In steady state a reconcile must not write anything — no pod churn every
// loop. The fake client bumps ResourceVersion on every Update, so a spurious
// write is observable. This guards the strip+re-append no-op and the
// aliasing-safety of reconcileKamaraEnv.
func TestReconcileOptionalApps_Idempotent(t *testing.T) {
	ns := "tenant-demo"
	tenant := &v1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{Name: "demo"}, Spec: v1alpha1.TenantSpec{Slug: "demo", Apps: []string{"office"}}}
	// Steady state: office enabled, np-kamara already admits it, kamara env
	// already carries the office vars in the order ensureApps appends them.
	objs := []client.Object{
		tenant,
		npKamara(ns, "ergonomos", "office"),
		kamaraDeploy(ns,
			corev1.EnvVar{Name: "OIDC_ISSUER", Value: "x"},
			corev1.EnvVar{Name: "OFFICE_URL", Value: "https://office.demo.peristera.app"},
			corev1.EnvVar{Name: "WOPI_SRC_BASE", Value: "http://kamara." + ns + ".svc.cluster.local"},
		),
	}
	r := &TenantReconciler{
		Client:       fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objs...).Build(),
		BaseDomain:   "peristera.app",
		URLScheme:    "https",
		ExternalPort: "443",
	}
	ctx := context.Background()
	rv := func(o client.Object, key client.ObjectKey) string {
		if err := r.Get(ctx, key, o); err != nil {
			t.Fatal(err)
		}
		return o.GetResourceVersion()
	}
	npKey := client.ObjectKey{Name: "np-kamara", Namespace: ns}
	depKey := client.ObjectKey{Name: "kamara", Namespace: ns}
	npBefore := rv(&networkingv1.NetworkPolicy{}, npKey)
	depBefore := rv(&appsv1.Deployment{}, depKey)

	if err := r.reconcileOptionalApps(ctx, tenant, ns); err != nil {
		t.Fatal(err)
	}

	if got := rv(&networkingv1.NetworkPolicy{}, npKey); got != npBefore {
		t.Errorf("np-kamara was rewritten in steady state (rv %s → %s)", npBefore, got)
	}
	if got := rv(&appsv1.Deployment{}, depKey); got != depBefore {
		t.Errorf("kamara Deployment was rolled in steady state (rv %s → %s)", depBefore, got)
	}
}
