/*
Copyright 2025 The Kubeflow authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package rhoai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubeflow/spark-operator/v2/api/v1beta2"
	"github.com/kubeflow/spark-operator/v2/pkg/common"
)

type prometheusQueryResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Value  []json.RawMessage `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

type prometheusSample struct {
	metric map[string]string
	value  float64
}

var _ = Describe("Prometheus Metrics", func() {
	Context("Metrics collection", func() {
		ctx := context.Background()
		path := filepath.Join("..", "..", "..", "examples", "openshift", "manifests", "spark-pi.yaml")

		var app *v1beta2.SparkApplication

		BeforeEach(func() {
			By("Parsing SparkApplication from file")
			file, err := os.Open(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(file).NotTo(BeNil())
			defer func() { Expect(file.Close()).NotTo(HaveOccurred()) }()

			decoder := yaml.NewYAMLOrJSONDecoder(file, 100)
			Expect(decoder).NotTo(BeNil())

			app = &v1beta2.SparkApplication{}
			Expect(decoder.Decode(app)).NotTo(HaveOccurred())
			app.Name = "spark-pi-metrics-test"
			app.Namespace = TestNamespace
			overrideSparkAppImage(app)
		})

		AfterEach(func() {
			if app == nil {
				return
			}
			if strings.EqualFold(os.Getenv("CLEANUP"), "false") {
				return
			}

			key := types.NamespacedName{Namespace: app.Namespace, Name: app.Name}
			if err := k8sClient.Get(ctx, key, app); err == nil {
				By("Deleting SparkApplication")
				Expect(k8sClient.Delete(ctx, app)).To(Succeed())
			}
		})

		It("Should serve Prometheus metrics including Spark application metrics", func() {
			metricNames := []string{
				common.MetricSparkApplicationSubmitCount,
				common.MetricSparkApplicationSuccessCount,
				common.MetricSparkApplicationStartLatencySecondsHistogram + "_count",
			}

			var prometheusURL string
			var stopPrometheusPortForward func()
			baselines := make(map[string]float64, len(metricNames))

			var controllerPod corev1.Pod
			var metricsScheme, metricsPort string
			if prometheusScrapeTestEnabled() {
				By("Connecting to the Prometheus test instance")
				prometheusPodName := findPrometheusPod(ctx)
				var err error
				prometheusURL, stopPrometheusPortForward, err = startPrometheusPortForward(prometheusPodName)
				Expect(err).NotTo(HaveOccurred())
				defer stopPrometheusPortForward()

				By("Checking that both TLS PodMonitor targets are up")
				jobs := []string{
					ReleaseNamespace + "/spark-operator-podmonitor",
					ReleaseNamespace + "/spark-operator-webhook-podmonitor",
				}
				for _, job := range jobs {
					query := fmt.Sprintf(`up{job=%q}`, job)
					Eventually(func(g Gomega) {
						samples, err := queryPrometheus(ctx, prometheusURL, query)
						g.Expect(err).NotTo(HaveOccurred())
						if len(samples) == 0 {
							allTargets, allTargetsErr := queryPrometheus(ctx, prometheusURL, "up")
							g.Expect(allTargetsErr).NotTo(HaveOccurred())
							g.Expect(samples).NotTo(BeEmpty(), "no Prometheus target matched %s; available targets: %v", query, allTargets)
						}
						for _, sample := range samples {
							g.Expect(sample.value).To(Equal(float64(1)), "Prometheus target %v is down", sample.metric)
						}
					}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
				}

				for _, metricName := range metricNames {
					samples, err := queryPrometheus(ctx, prometheusURL, "sum("+metricName+")")
					Expect(err).NotTo(HaveOccurred())
					if len(samples) == 1 {
						baselines[metricName] = samples[0].value
					}
				}
			} else {
				By("Finding the leader controller pod")
				pods := &corev1.PodList{}
				Expect(k8sClient.List(ctx, pods,
					client.InNamespace(ReleaseNamespace),
					client.MatchingLabels{
						"app.kubernetes.io/name":      "spark-operator",
						"app.kubernetes.io/component": "controller",
					},
				)).To(Succeed())
				Expect(pods.Items).NotTo(BeEmpty(), "controller pod not found")

				controllerPod = pods.Items[0]
				lease := &coordinationv1.Lease{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: ReleaseNamespace,
					Name:      "spark-operator-controller-lock",
				}, lease); err == nil && lease.Spec.HolderIdentity != nil {
					for _, pod := range pods.Items {
						if pod.Name == *lease.Spec.HolderIdentity {
							controllerPod = pod
							break
						}
					}
				}

				By("Detecting metrics scheme and port from controller deployment")
				deploy := &appsv1.Deployment{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: ReleaseNamespace,
					Name:      "spark-operator-controller",
				}, deploy)).To(Succeed())

				metricsScheme = "http"
				metricsPort = "8080"
				for _, arg := range deploy.Spec.Template.Spec.Containers[0].Args {
					if arg == "--secure-metrics" || arg == "--secure-metrics=true" {
						metricsScheme = "https"
					}
					if strings.HasPrefix(arg, "--metrics-bind-address=") {
						addr := strings.TrimPrefix(arg, "--metrics-bind-address=")
						if _, port, err := net.SplitHostPort(addr); err == nil && port != "" {
							metricsPort = port
						}
					}
				}

				By("Verifying the metrics endpoint serves Prometheus-formatted data")
				data, err := clientset.CoreV1().Pods(ReleaseNamespace).
					ProxyGet(metricsScheme, controllerPod.Name, metricsPort, "metrics", nil).
					DoRaw(ctx)
				Expect(err).NotTo(HaveOccurred(), "failed to proxy GET /metrics from controller pod")

				metricsOutput := string(data)
				Expect(metricsOutput).To(ContainSubstring("# HELP"))
				Expect(metricsOutput).To(ContainSubstring("# TYPE"))
				Expect(metricsOutput).To(ContainSubstring("go_goroutines"))
				Expect(metricsOutput).To(ContainSubstring("process_cpu_seconds_total"))
			}

			By("Cleaning up any stale driver pod from a previous run")
			staleDriver := &corev1.Pod{}
			staleDriver.Name = app.Name + "-driver"
			staleDriver.Namespace = app.Namespace
			_ = k8sClient.Delete(ctx, staleDriver)

			By("Creating SparkApplication to exercise the metrics pipeline")
			Expect(k8sClient.Create(ctx, app)).To(Succeed())

			By("Waiting for SparkApplication to complete")
			key := types.NamespacedName{Namespace: app.Namespace, Name: app.Name}
			Expect(waitForSparkApplicationCompleted(ctx, key)).NotTo(HaveOccurred())

			if prometheusScrapeTestEnabled() {
				By("Querying SparkApplication metrics through Prometheus")
				for _, metricName := range metricNames {
					query := "sum(" + metricName + ")"
					Eventually(func(g Gomega) {
						samples, err := queryPrometheus(ctx, prometheusURL, query)
						g.Expect(err).NotTo(HaveOccurred())
						g.Expect(samples).To(HaveLen(1), "Prometheus query %q did not return a value", query)
						if len(samples) != 1 {
							return
						}
						g.Expect(samples[0].value).To(BeNumerically(">", baselines[metricName]),
							"Prometheus query %q did not increase after the SparkApplication completed", query)
					}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
				}
			} else {
				By("Verifying Spark application metrics are present after app completion")
				Eventually(func() string {
					data, err := clientset.CoreV1().Pods(ReleaseNamespace).
						ProxyGet(metricsScheme, controllerPod.Name, metricsPort, "metrics", nil).
						DoRaw(ctx)
					if err != nil {
						return ""
					}
					return string(data)
				}).WithPolling(PollInterval).WithTimeout(WaitTimeout).Should(And(
					ContainSubstring("spark_application_count"),
					ContainSubstring("spark_application_submit_count"),
					ContainSubstring("spark_application_success_count"),
				))
			}
		})
	})
})

func findPrometheusPod(ctx context.Context) string {
	var prometheusPodName string
	Eventually(func(g Gomega) {
		pods, err := clientset.CoreV1().Pods(PrometheusNamespace).List(ctx, metav1.ListOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		for _, pod := range pods.Items {
			if strings.HasPrefix(pod.Name, prometheusStatefulSetName+"-") {
				prometheusPodName = pod.Name
				return
			}
		}
		g.Expect(prometheusPodName).NotTo(BeEmpty())
	}).WithTimeout(time.Minute).WithPolling(time.Second).Should(Succeed())
	return prometheusPodName
}

func startPrometheusPortForward(podName string) (string, func(), error) {
	transport, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return "", nil, fmt.Errorf("create port-forward transport: %w", err)
	}

	reqURL := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(PrometheusNamespace).
		Name(podName).
		SubResource("portforward").
		URL()
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, reqURL)

	stopChan := make(chan struct{})
	readyChan := make(chan struct{})
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { close(stopChan) }) }

	forwarder, err := portforward.New(dialer, []string{"0:9090"}, stopChan, readyChan, io.Discard, io.Discard)
	if err != nil {
		stop()
		return "", nil, fmt.Errorf("create Prometheus port forward: %w", err)
	}

	errChan := make(chan error, 1)
	go func() { errChan <- forwarder.ForwardPorts() }()

	select {
	case <-readyChan:
	case forwardErr := <-errChan:
		stop()
		if forwardErr == nil {
			forwardErr = fmt.Errorf("port forward stopped before becoming ready")
		}
		return "", nil, fmt.Errorf("start Prometheus port forward: %w", forwardErr)
	case <-time.After(time.Minute):
		stop()
		return "", nil, fmt.Errorf("timed out starting Prometheus port forward")
	}

	ports, err := forwarder.GetPorts()
	if err != nil {
		stop()
		return "", nil, fmt.Errorf("get Prometheus local port: %w", err)
	}
	if len(ports) != 1 {
		stop()
		return "", nil, fmt.Errorf("expected one Prometheus forwarded port, got %d", len(ports))
	}
	return fmt.Sprintf("http://127.0.0.1:%d", ports[0].Local), stop, nil
}

func queryPrometheus(ctx context.Context, baseURL, query string) ([]prometheusSample, error) {
	queryURL, err := url.Parse(baseURL + "/api/v1/query")
	if err != nil {
		return nil, fmt.Errorf("build Prometheus query URL: %w", err)
	}
	queryParams := queryURL.Query()
	queryParams.Set("query", query)
	queryURL.RawQuery = queryParams.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, queryURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Prometheus request: %w", err)
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("query Prometheus: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	rawResponse, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read Prometheus response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Prometheus returned HTTP %d: %s", response.StatusCode, string(rawResponse))
	}

	var queryResponse prometheusQueryResponse
	if err := json.Unmarshal(rawResponse, &queryResponse); err != nil {
		return nil, fmt.Errorf("decode Prometheus response: %w", err)
	}
	if queryResponse.Status != "success" {
		return nil, fmt.Errorf("Prometheus query failed: %s", queryResponse.Error)
	}

	samples := make([]prometheusSample, 0, len(queryResponse.Data.Result))
	for _, result := range queryResponse.Data.Result {
		if len(result.Value) != 2 {
			return nil, fmt.Errorf("unexpected Prometheus sample value: %s", string(rawResponse))
		}
		var value string
		if err := json.Unmarshal(result.Value[1], &value); err != nil {
			return nil, fmt.Errorf("decode Prometheus sample: %w", err)
		}
		parsedValue, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("parse Prometheus sample %q: %w", value, err)
		}
		samples = append(samples, prometheusSample{metric: result.Metric, value: parsedValue})
	}
	return samples, nil
}
