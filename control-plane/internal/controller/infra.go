package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

// cnpgDatabaseGVK is CloudNativePG's declarative managed-database resource
// (a database inside an existing Cluster).
var cnpgDatabaseGVK = schema.GroupVersionKind{
	Group: "postgresql.cnpg.io", Version: "v1", Kind: "Database",
}

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
	dsn := fmt.Sprintf("postgresql://app:%s@db-rw.%s.svc.cluster.local:5432/%s?sslmode=require", pw, ns, app)

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

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "openfga", Namespace: ns, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{{
						Name:    "migrate",
						Image:   "openfga/openfga:latest",
						Command: []string{"/openfga", "migrate"},
						Env:     []corev1.EnvVar{engine, dsnEnv},
					}},
					Containers: []corev1.Container{{
						Name:    "openfga",
						Image:   "openfga/openfga:latest",
						Command: []string{"/openfga", "run"},
						Env:     []corev1.EnvVar{engine, dsnEnv},
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

// setOwnerAndCreate stamps the tenant as owner (for GC on off-boarding)
// and creates the object.
func (r *TenantReconciler) setOwnerAndCreate(ctx context.Context, tenant *v1alpha1.Tenant, obj client.Object) error {
	if err := controllerutil.SetControllerReference(tenant, obj, r.Scheme()); err != nil {
		return err
	}
	return r.Create(ctx, obj)
}
