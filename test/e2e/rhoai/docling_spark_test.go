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
	"fmt"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kubeflow/spark-operator/v2/api/v1beta2"
	"github.com/kubeflow/spark-operator/v2/pkg/util"
)

var _ = Describe("Docling Spark Application", func() {
	Context("Run docling-spark workload", func() {
		ctx := context.Background()

		var app *v1beta2.SparkApplication

		BeforeEach(func() {
			appYAML := os.Getenv("DOCLING_APP_YAML")
			if appYAML == "" {
				Skip("Docling test requires DOCLING_APP_YAML env var (9.5GB image + PVC setup needed)")
			}

			if _, err := os.Stat(appYAML); os.IsNotExist(err) {
				Skip(fmt.Sprintf("Docling app YAML not found: %s", appYAML))
			}

			By("Parsing SparkApplication from file")
			file, err := os.Open(appYAML)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = file.Close() }()

			app = &v1beta2.SparkApplication{}
			decoder := yaml.NewYAMLOrJSONDecoder(file, 4096)
			Expect(decoder.Decode(app)).NotTo(HaveOccurred())

			if app.Namespace == "" {
				app.Namespace = TestNamespace
			}
			overrideSparkAppImage(app)

			By("Ensuring PVCs exist for docling workload")
			ensureDoclingPVCs(ctx, app.Namespace)

			By("Creating SparkApplication")
			existing := &v1beta2.SparkApplication{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: app.Name}, existing); err == nil {
				Expect(k8sClient.Delete(ctx, existing)).To(Succeed())
				Eventually(func() bool {
					err := k8sClient.Get(ctx, types.NamespacedName{Namespace: app.Namespace, Name: app.Name}, &v1beta2.SparkApplication{})
					return apierrors.IsNotFound(err)
				}).WithTimeout(WaitTimeout).WithPolling(PollInterval).Should(BeTrue())
			}
			Expect(k8sClient.Create(ctx, app)).To(Succeed())
		})

		AfterEach(func() {
			if app == nil {
				return
			}
			if strings.EqualFold(os.Getenv("CLEANUP"), "false") && CurrentSpecReport().Failed() {
				return
			}

			key := types.NamespacedName{Namespace: app.Namespace, Name: app.Name}
			if err := k8sClient.Get(ctx, key, app); err == nil {
				By("Deleting SparkApplication")
				Expect(k8sClient.Delete(ctx, app)).To(Succeed())
			}
		})

		It("Should complete the docling workload successfully", func() {
			By("Waiting for SparkApplication to complete")
			key := types.NamespacedName{Namespace: app.Namespace, Name: app.Name}
			Expect(waitForSparkApplicationCompleted(ctx, key)).NotTo(HaveOccurred())

			By("Verifying driver pod completed")
			driverPodName := util.GetDriverPodName(app)
			driverPod := &corev1.Pod{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: app.Namespace, Name: driverPodName,
			}, driverPod)).NotTo(HaveOccurred())

			By("Verifying executor pods were created")
			executorPods := &corev1.PodList{}
			Expect(k8sClient.List(ctx, executorPods,
				client.InNamespace(app.Namespace),
				client.MatchingLabels{
					"spark-role":     "executor",
					"spark-app-name": app.Name,
				},
			)).To(Succeed())
			Expect(len(executorPods.Items)).To(BeNumerically(">=", 1),
				"at least one executor pod should have been created")
		})
	})
})

func ensureDoclingPVCs(ctx context.Context, namespace string) {
	pvcs := []string{"docling-input", "docling-output"}
	for _, name := range pvcs {
		pvc := &corev1.PersistentVolumeClaim{}
		err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, pvc)
		if err == nil {
			continue
		}
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "unexpected error checking PVC %s: %v", name, err)
		newPVC := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, newPVC)).To(Succeed())
	}
}
