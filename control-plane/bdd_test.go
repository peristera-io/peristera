// godog harness for the tenant-lifecycle feature (working agreement #2).
// Drives the real dev cluster (k3d + CNPG + Zitadel + a running
// controller), so it only runs when explicitly asked:
//
//	PERISTERA_E2E=1 go test -run TestFeatures -v -timeout 15m .
package controlplane_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

type world struct {
	k8s          client.Client
	issuers      map[string]string // slug → issuer seen in status
	former       map[string]string // slug → issuer before deletion
	updateErr    error
	pollInterval time.Duration
}

func (w *world) createTenant(slug, displayName string) error {
	t := &v1alpha1.Tenant{
		ObjectMeta: metav1.ObjectMeta{Name: slug},
		Spec:       v1alpha1.TenantSpec{Slug: slug, DisplayName: displayName},
	}
	err := w.k8s.Create(context.Background(), t)
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (w *world) waitPhase(slug, phase string, minutes int) error {
	deadline := time.Now().Add(time.Duration(minutes) * time.Minute)
	for time.Now().Before(deadline) {
		t := &v1alpha1.Tenant{}
		if err := w.k8s.Get(context.Background(), client.ObjectKey{Name: slug}, t); err == nil {
			if string(t.Status.Phase) == phase {
				return nil
			}
		}
		time.Sleep(w.pollInterval)
	}
	return fmt.Errorf("tenant %q did not reach phase %q within %dm", slug, phase, minutes)
}

func (w *world) tenantExists(slug string) error {
	if err := w.createTenant(slug, slug); err != nil {
		return err
	}
	return w.waitPhase(slug, string(v1alpha1.TenantReady), 3)
}

func (w *world) namespaceExists(name string) error {
	return w.k8s.Get(context.Background(), client.ObjectKey{Name: name}, &corev1.Namespace{})
}

func (w *world) statusReportsIAM(slug string) error {
	t := &v1alpha1.Tenant{}
	if err := w.k8s.Get(context.Background(), client.ObjectKey{Name: slug}, t); err != nil {
		return err
	}
	if t.Status.Issuer == "" || t.Status.ClientID == "" {
		return fmt.Errorf("tenant %q status incomplete: issuer=%q clientId=%q",
			slug, t.Status.Issuer, t.Status.ClientID)
	}
	w.issuers[slug] = t.Status.Issuer
	return nil
}

func discoveryIssuer(issuer string) (string, int, error) {
	resp, err := http.Get(issuer + "/.well-known/openid-configuration")
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, nil
	}
	var doc struct {
		Issuer string `json:"issuer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", resp.StatusCode, err
	}
	return doc.Issuer, resp.StatusCode, nil
}

func (w *world) discoveryMatches(slug string) error {
	want := w.issuers[slug]
	if want == "" {
		return fmt.Errorf("no issuer recorded for %q", slug)
	}
	// The instance can lag its projections for a few seconds after creation.
	var lastErr error
	for range 10 {
		got, code, err := discoveryIssuer(want)
		if err == nil && code == http.StatusOK && got == want {
			return nil
		}
		lastErr = fmt.Errorf("discovery on %s: code=%d issuer=%q err=%v", want, code, got, err)
		time.Sleep(3 * time.Second)
	}
	return lastErr
}

func (w *world) changeSlug(slug, newSlug string) error {
	t := &v1alpha1.Tenant{}
	if err := w.k8s.Get(context.Background(), client.ObjectKey{Name: slug}, t); err != nil {
		return err
	}
	t.Spec.Slug = newSlug
	w.updateErr = w.k8s.Update(context.Background(), t)
	return nil
}

func (w *world) rejectedWith(msg string) error {
	if w.updateErr == nil {
		return fmt.Errorf("update was accepted, expected rejection %q", msg)
	}
	if !strings.Contains(w.updateErr.Error(), msg) {
		return fmt.Errorf("rejection %q does not contain %q", w.updateErr, msg)
	}
	return nil
}

func (w *world) deleteTenant(slug string) error {
	t := &v1alpha1.Tenant{}
	if err := w.k8s.Get(context.Background(), client.ObjectKey{Name: slug}, t); err != nil {
		return err
	}
	w.former[slug] = t.Status.Issuer
	return w.k8s.Delete(context.Background(), t)
}

func (w *world) tenantGone(slug string, minutes int) error {
	return w.gone(minutes, func() error {
		return w.k8s.Get(context.Background(), client.ObjectKey{Name: slug}, &v1alpha1.Tenant{})
	})
}

func (w *world) namespaceGone(name string, minutes int) error {
	return w.gone(minutes, func() error {
		return w.k8s.Get(context.Background(), client.ObjectKey{Name: name}, &corev1.Namespace{})
	})
}

func (w *world) gone(minutes int, get func() error) error {
	deadline := time.Now().Add(time.Duration(minutes) * time.Minute)
	for time.Now().Before(deadline) {
		if apierrors.IsNotFound(get()) {
			return nil
		}
		time.Sleep(w.pollInterval)
	}
	return fmt.Errorf("still present after %dm", minutes)
}

func (w *world) formerIssuerDead(slug string) error {
	issuer := w.former[slug]
	if issuer == "" {
		return fmt.Errorf("no former issuer recorded for %q", slug)
	}
	var lastErr error
	for range 10 {
		_, code, err := discoveryIssuer(issuer)
		if err != nil || code == http.StatusNotFound {
			return nil // connection refused or 404 both mean "gone"
		}
		lastErr = fmt.Errorf("former issuer %s still answers %d", issuer, code)
		time.Sleep(3 * time.Second)
	}
	return lastErr
}

func (w *world) appURL(slug, app string) string {
	base := os.Getenv("TENANT_BASE_DOMAIN")
	if base == "" {
		base = "127.0.0.1.sslip.io"
	}
	port := os.Getenv("TENANT_EXTERNAL_PORT")
	if port == "" {
		port = "9080"
	}
	return fmt.Sprintf("http://%s.%s.%s:%s", app, slug, base, port)
}

func (w *world) appAnswers(app, slug string, minutes int) error {
	url := w.appURL(slug, app)
	deadline := time.Now().Add(time.Duration(minutes) * time.Minute)
	var last string
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			last = fmt.Sprintf("status %d", resp.StatusCode)
		} else {
			last = err.Error()
		}
		time.Sleep(w.pollInterval)
	}
	return fmt.Errorf("app %s of %s never answered on %s: %s", app, slug, url, last)
}

func (w *world) appLoginGoesToIssuer(app, slug string) error {
	t := &v1alpha1.Tenant{}
	if err := w.k8s.Get(context.Background(), client.ObjectKey{Name: slug}, t); err != nil {
		return err
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := noRedirect.Get(w.appURL(slug, app) + "/auth/login")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(loc, t.Status.Issuer+"/") {
		return fmt.Errorf("login redirect: status=%d location=%q issuer=%q",
			resp.StatusCode, loc, t.Status.Issuer)
	}
	return nil
}

func (w *world) initialAdminExists(ns string) error {
	sec := &corev1.Secret{}
	if err := w.k8s.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: "initial-admin"}, sec); err != nil {
		return err
	}
	if len(sec.Data["username"]) == 0 || len(sec.Data["password"]) == 0 {
		return fmt.Errorf("initial-admin secret incomplete: keys=%v", keys(sec.Data))
	}
	return nil
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestFeatures(t *testing.T) {
	if os.Getenv("PERISTERA_E2E") == "" {
		t.Skip("set PERISTERA_E2E=1 to run against the dev cluster")
	}
	cfg, err := ctrl.GetConfig()
	if err != nil {
		t.Fatalf("kubeconfig: %v", err)
	}
	k8s, err := client.New(cfg, client.Options{})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if err := v1alpha1.AddToScheme(k8s.Scheme()); err != nil {
		t.Fatalf("scheme: %v", err)
	}

	w := &world{k8s: k8s, issuers: map[string]string{}, former: map[string]string{}, pollInterval: 3 * time.Second}

	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^I create a tenant "([^"]*)" with display name "([^"]*)"$`, w.createTenant)
			sc.Step(`^the tenant "([^"]*)" reaches phase "([^"]*)" within (\d+) minutes$`, w.waitPhase)
			sc.Step(`^the namespace "([^"]*)" exists$`, w.namespaceExists)
			sc.Step(`^the tenant "([^"]*)" status reports an issuer and a client ID$`, w.statusReportsIAM)
			sc.Step(`^OIDC discovery on the issuer of tenant "([^"]*)" answers with the same issuer$`, w.discoveryMatches)
			sc.Step(`^a tenant "([^"]*)" exists$`, w.tenantExists)
			sc.Step(`^I try to change the slug of tenant "([^"]*)" to "([^"]*)"$`, w.changeSlug)
			sc.Step(`^the change is rejected with message "([^"]*)"$`, w.rejectedWith)
			sc.Step(`^I delete the tenant "([^"]*)"$`, w.deleteTenant)
			sc.Step(`^the tenant "([^"]*)" is gone within (\d+) minutes$`, w.tenantGone)
			sc.Step(`^the namespace "([^"]*)" is gone within (\d+) minutes$`, w.namespaceGone)
			sc.Step(`^OIDC discovery on the former issuer of tenant "([^"]*)" stops answering$`, w.formerIssuerDead)
			sc.Step(`^the app "([^"]*)" of tenant "([^"]*)" answers on its own domain within (\d+) minutes$`, w.appAnswers)
			sc.Step(`^the app "([^"]*)" of tenant "([^"]*)" sends logins to the tenant's issuer$`, w.appLoginGoesToIssuer)
			sc.Step(`^the namespace "([^"]*)" holds initial admin credentials$`, w.initialAdminExists)
		},
		Options: &godog.Options{
			Format: "pretty", Paths: []string{"features"}, Strict: true, TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("feature suite failed")
	}
}
