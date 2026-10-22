package sparkoperatormodule

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/opendatahub-io/odh-platform-utilities/api/common"

	platformv1alpha1 "github.com/opendatahub-io/spark-operator-module/pkg/apis/v1alpha1"
)

func TestGetPlatformVersion(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		data    map[string]string
		want    string
		wantErr bool
	}{
		{name: "reads version", data: map[string]string{platformVersionKey: "2.20.0"}, want: "2.20.0"},
		{name: "rejects missing key", data: map[string]string{}, wantErr: true},
		{name: "rejects blank version", data: map[string]string{platformVersionKey: "  "}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			config := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: platformConfigMapName, Namespace: "apps"},
				Data:       tt.data,
			}
			r := &SparkOperatorModuleReconciler{
				Client:                fake.NewClientBuilder().WithScheme(scheme).WithObjects(config).Build(),
				applicationsNamespace: "apps",
			}

			got, err := r.getPlatformVersion(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("getPlatformVersion() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("getPlatformVersion() = %q, want %q", got, tt.want)
			}
		})
	}

	r := &SparkOperatorModuleReconciler{
		Client:                fake.NewClientBuilder().WithScheme(scheme).Build(),
		applicationsNamespace: "apps",
	}
	if _, err := r.getPlatformVersion(context.Background()); err == nil {
		t.Fatal("getPlatformVersion() expected an error for a missing ConfigMap")
	}
}

func TestSetPlatformRelease(t *testing.T) {
	t.Parallel()

	instance := &platformv1alpha1.SparkOperator{
		Status: platformv1alpha1.SparkOperatorStatus{
			ComponentReleaseStatus: common.ComponentReleaseStatus{Releases: []common.ComponentRelease{
				{Name: "Spark Operator", Version: "v2.4.0"},
				{Name: platformReleaseName, Version: "2.19.0"},
			}},
		},
	}

	setPlatformRelease(instance, "2.20.0")
	if len(instance.Status.Releases) != 2 {
		t.Fatalf("setPlatformRelease() produced %d releases, want 2", len(instance.Status.Releases))
	}
	if got := instance.Status.Releases[1].Version; got != "2.20.0" {
		t.Fatalf("setPlatformRelease() version = %q, want %q", got, "2.20.0")
	}
	if got := instance.Status.Releases[0].Version; got != "v2.4.0" {
		t.Fatalf("setPlatformRelease() changed component release to %q", got)
	}
}

func TestSetReleaseStatusMergesWithPlatformRelease(t *testing.T) {
	t.Parallel()

	// Status left with only the platform entry, as happens when the first
	// healthy reconcile failed to load component metadata.
	instance := &platformv1alpha1.SparkOperator{
		Status: platformv1alpha1.SparkOperatorStatus{
			ComponentReleaseStatus: common.ComponentReleaseStatus{Releases: []common.ComponentRelease{
				{Name: platformReleaseName, Version: "2.20.0"},
			}},
		},
	}

	manifestsPath := t.TempDir()
	metadata := `releases:
  - name: Spark Operator
    version: v2.4.0
  - name: Spark
    version: 4.0.1
`
	if err := os.MkdirAll(filepath.Join(manifestsPath, SparkOperatorComponentName, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(manifestsPath, SparkOperatorComponentName, "config", "component_metadata.yaml"),
		[]byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &SparkOperatorModuleReconciler{ManifestsTemplatePath: manifestsPath}

	r.setReleaseStatus(instance)

	if len(instance.Status.Releases) != 3 {
		t.Fatalf("setReleaseStatus() produced %d releases, want 3", len(instance.Status.Releases))
	}
	if got := instance.Status.Releases[0].Version; got != "2.20.0" {
		t.Fatalf("setReleaseStatus() changed platform release to %q", got)
	}
	for _, want := range []struct{ name, version string }{
		{"Spark Operator", "v2.4.0"},
		{"Spark", "4.0.1"},
	} {
		rel := findReleaseByName(instance, want.name)
		if rel == nil {
			t.Fatalf("setReleaseStatus() did not populate release %q", want.name)
		}
		if rel.Version != want.version {
			t.Fatalf("setReleaseStatus() release %q version = %q, want %q", want.name, rel.Version, want.version)
		}
	}
}

func findReleaseByName(cr *platformv1alpha1.SparkOperator, name string) *common.ComponentRelease {
	for i := range cr.Status.Releases {
		if cr.Status.Releases[i].Name == name {
			return &cr.Status.Releases[i]
		}
	}
	return nil
}
