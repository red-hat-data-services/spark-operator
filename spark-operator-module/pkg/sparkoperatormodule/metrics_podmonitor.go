package sparkoperatormodule

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// applyMetricsPodMonitorServerNames makes TLS verification follow the
// effective applications namespace. The Kustomize overlays contain canonical
// server names for standalone use, but renderKustomize can override resource
// namespaces through APPLICATIONS_NAMESPACE after those patches are applied.
func applyMetricsPodMonitorServerNames(resources []unstructured.Unstructured, namespace string) error {
	for i := range resources {
		resource := &resources[i]
		serviceName, ok := metricsServiceNameForPodMonitor(resource.GetName())
		if resource.GetKind() != "PodMonitor" || !ok {
			continue
		}

		endpoints, found, err := unstructured.NestedSlice(resource.Object, "spec", "podMetricsEndpoints")
		if err != nil {
			return fmt.Errorf("read PodMonitor/%s endpoints: %w", resource.GetName(), err)
		}
		if !found || len(endpoints) == 0 {
			return fmt.Errorf("PodMonitor/%s has no metrics endpoints", resource.GetName())
		}

		endpoint, ok := endpoints[0].(map[string]any)
		if !ok {
			return fmt.Errorf("PodMonitor/%s endpoint has unexpected type %T", resource.GetName(), endpoints[0])
		}
		if err := unstructured.SetNestedField(
			endpoint,
			fmt.Sprintf("%s.%s.svc", serviceName, namespace),
			"tlsConfig", "serverName",
		); err != nil {
			return fmt.Errorf("set PodMonitor/%s serverName: %w", resource.GetName(), err)
		}
		endpoints[0] = endpoint
		if err := unstructured.SetNestedSlice(resource.Object, endpoints, "spec", "podMetricsEndpoints"); err != nil {
			return fmt.Errorf("update PodMonitor/%s endpoints: %w", resource.GetName(), err)
		}
	}

	return nil
}

func metricsServiceNameForPodMonitor(podMonitorName string) (string, bool) {
	switch podMonitorName {
	case "spark-operator-podmonitor":
		return "spark-operator-controller-metrics", true
	case "spark-operator-webhook-podmonitor":
		return "spark-operator-webhook-metrics", true
	default:
		return "", false
	}
}
