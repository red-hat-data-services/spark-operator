package sparkoperatormodule

import (
	"context"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCheckDeploymentsReady_Missing(t *testing.T) {
	g := NewWithT(t)

	cli := fake.NewClientBuilder().Build()
	err := checkDeploymentsReady(context.Background(), cli, "opendatahub", []string{"spark-operator-controller"})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("spark-operator-controller"))
}

func TestCheckDeploymentsReady_Available(t *testing.T) {
	g := NewWithT(t)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spark-operator-controller",
			Namespace: "opendatahub",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "spark-operator-controller"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "spark-operator-controller"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "test"}}},
			},
		},
	}
	dep.Status.AvailableReplicas = 1
	dep.Status.UpdatedReplicas = 1

	cli := fake.NewClientBuilder().WithObjects(dep).WithStatusSubresource(dep).Build()
	err := checkDeploymentsReady(context.Background(), cli, "opendatahub", []string{"spark-operator-controller"})
	g.Expect(err).NotTo(HaveOccurred())
}

func TestCheckDeploymentsReady_ExistsButUnavailable(t *testing.T) {
	g := NewWithT(t)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "spark-operator-controller",
			Namespace:  "opendatahub",
			Generation: 1,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "spark-operator-controller"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "spark-operator-controller"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "test"}}},
			},
		},
	}
	dep.Status.AvailableReplicas = 0
	dep.Status.UpdatedReplicas = 0
	dep.Status.ObservedGeneration = 1

	cli := fake.NewClientBuilder().WithObjects(dep).WithStatusSubresource(dep).Build()
	err := checkDeploymentsReady(context.Background(), cli, "opendatahub", []string{"spark-operator-controller"})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("spark-operator-controller"))
}

func TestRenderKustomizeMetricsServerNamesUseEffectiveNamespace(t *testing.T) {
	t.Parallel()

	const namespace = "custom-applications"
	overlays := []string{"odh", "rhoai"}
	expected := map[string]string{
		"spark-operator-podmonitor":         "spark-operator-controller-metrics." + namespace + ".svc",
		"spark-operator-webhook-podmonitor": "spark-operator-webhook-metrics." + namespace + ".svc",
	}

	for _, overlay := range overlays {
		t.Run(overlay, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join("..", "..", "..", "config", "overlays", overlay)
			resources, err := renderKustomize(path, namespace)
			if err != nil {
				t.Fatalf("renderKustomize(%s): %v", overlay, err)
			}
			if err := applyMetricsPodMonitorServerNames(resources, namespace); err != nil {
				t.Fatalf("applyMetricsPodMonitorServerNames(%s): %v", overlay, err)
			}

			for name, wantServerName := range expected {
				pm := findUnstructured(resources, "PodMonitor", name)
				if pm == nil {
					t.Fatalf("PodMonitor/%s not found in %s overlay", name, overlay)
				}
				if pm.GetNamespace() != namespace {
					t.Errorf("PodMonitor/%s namespace = %q, want %q", name, pm.GetNamespace(), namespace)
				}

				endpoints, found, err := unstructured.NestedSlice(pm.Object, "spec", "podMetricsEndpoints")
				if err != nil {
					t.Fatalf("read PodMonitor/%s endpoints: %v", name, err)
				}
				if !found || len(endpoints) == 0 {
					t.Fatalf("PodMonitor/%s endpoints not found", name)
				}
				endpoint, ok := endpoints[0].(map[string]any)
				if !ok {
					t.Fatalf("PodMonitor/%s endpoint has unexpected type %T", name, endpoints[0])
				}
				serverName, found, err := unstructured.NestedString(endpoint, "tlsConfig", "serverName")
				if err != nil {
					t.Fatalf("read PodMonitor/%s serverName: %v", name, err)
				}
				if !found {
					t.Fatalf("PodMonitor/%s serverName not found", name)
				}
				if serverName != wantServerName {
					t.Errorf("PodMonitor/%s serverName = %q, want %q", name, serverName, wantServerName)
				}
			}
		})
	}
}

func findUnstructured(resources []unstructured.Unstructured, kind, name string) *unstructured.Unstructured {
	for i := range resources {
		if resources[i].GetKind() == kind && resources[i].GetName() == name {
			return &resources[i]
		}
	}
	return nil
}
