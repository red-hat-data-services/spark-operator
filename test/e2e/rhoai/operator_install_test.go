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
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Operator Installation Validation", func() {
	ctx := context.Background()

	Context("Verify operator pods are healthy", func() {
		It("Should have running controller pod and webhook pod if present", func() {
			By("Listing operator pods")
			pods := &corev1.PodList{}
			Expect(k8sClient.List(ctx, pods,
				client.InNamespace(ReleaseNamespace),
				client.MatchingLabels{"app.kubernetes.io/name": "spark-operator"},
			)).To(Succeed())
			Expect(pods.Items).NotTo(BeEmpty(), "no operator pods found")

			By("Verifying controller pod exists")
			var controllerPod *corev1.Pod
			var webhookPod *corev1.Pod
			for i := range pods.Items {
				labels := pods.Items[i].Labels
				if labels["app.kubernetes.io/component"] == "controller" {
					controllerPod = &pods.Items[i]
				}
				if labels["app.kubernetes.io/component"] == "webhook" {
					webhookPod = &pods.Items[i]
				}
			}
			Expect(controllerPod).NotTo(BeNil(), "controller pod not found")
			Expect(controllerPod.Status.Phase).To(Equal(corev1.PodRunning))

			if webhookPod != nil {
				Expect(webhookPod.Status.Phase).To(Equal(corev1.PodRunning))
			}
		})
	})

	Context("Verify fsGroup is not 185 (OpenShift compatibility)", func() {
		It("Should not have fsGroup=185 on any operator pod", func() {
			By("Listing operator pods")
			pods := &corev1.PodList{}
			Expect(k8sClient.List(ctx, pods,
				client.InNamespace(ReleaseNamespace),
				client.MatchingLabels{"app.kubernetes.io/name": "spark-operator"},
			)).To(Succeed())
			Expect(pods.Items).NotTo(BeEmpty())

			for _, pod := range pods.Items {
				if pod.Spec.SecurityContext != nil && pod.Spec.SecurityContext.FSGroup != nil {
					Expect(*pod.Spec.SecurityContext.FSGroup).NotTo(Equal(int64(185)),
						fmt.Sprintf("pod %s has fsGroup=185 (not allowed for OpenShift)", pod.Name))
				}
			}
		})
	})

	Context("Verify container runs with non-root UID", func() {
		It("Should run controller container as non-root", func() {
			By("Finding controller pod")
			pods := &corev1.PodList{}
			Expect(k8sClient.List(ctx, pods,
				client.InNamespace(ReleaseNamespace),
				client.MatchingLabels{
					"app.kubernetes.io/name":      "spark-operator",
					"app.kubernetes.io/component": "controller",
				},
			)).To(Succeed())
			Expect(pods.Items).NotTo(BeEmpty(), "controller pod not found")

			controllerPod := pods.Items[0]

			By("Executing 'id' in the controller container")
			output, err := runCommand("kubectl", "exec", "-n", ReleaseNamespace, controllerPod.Name, "--", "id")
			Expect(err).NotTo(HaveOccurred(), "failed to exec 'id' in controller pod: %s", output)

			By("Verifying UID is not 0 (root)")
			Expect(output).NotTo(ContainSubstring("uid=0("),
				"container is running as root (uid=0)")

			uidStr := extractUID(output)
			Expect(uidStr).NotTo(Equal("0"), "container UID should not be 0")
		})
	})
})

var _ = Describe("ScheduledSparkApplication RBAC Preflight", func() {
	ctx := context.Background()

	controllerSA := os.Getenv("CONTROLLER_SA")
	if controllerSA == "" {
		controllerSA = "spark-operator-controller"
	}

	Context("Verify controller ServiceAccount has required RBAC", func() {
		It("Should have permissions for ScheduledSparkApplication lifecycle", func() {
			By("Finding ClusterRoles bound to the controller SA")
			clusterRoleBindings := &rbacv1.ClusterRoleBindingList{}
			Expect(k8sClient.List(ctx, clusterRoleBindings)).To(Succeed())

			var boundRoleNames []string
			for _, crb := range clusterRoleBindings.Items {
				for _, subject := range crb.Subjects {
					if subject.Kind == "ServiceAccount" &&
						subject.Name == controllerSA &&
						subject.Namespace == ReleaseNamespace {
						boundRoleNames = append(boundRoleNames, crb.RoleRef.Name)
					}
				}
			}
			Expect(boundRoleNames).NotTo(BeEmpty(),
				"no ClusterRoleBindings found for SA %s/%s", ReleaseNamespace, controllerSA)

			By("Verifying required permissions exist in bound ClusterRoles")
			type requiredRule struct {
				apiGroup string
				resource string
				verbs    []string
			}
			required := []requiredRule{
				{"sparkoperator.k8s.io", "scheduledsparkapplications", []string{"get", "list", "watch"}},
				{"sparkoperator.k8s.io", "scheduledsparkapplications/status", []string{"update"}},
				{"sparkoperator.k8s.io", "scheduledsparkapplications/finalizers", []string{"update"}},
				{"sparkoperator.k8s.io", "sparkapplications", []string{"create", "get", "list", "watch", "delete"}},
			}

			for _, roleName := range boundRoleNames {
				role := &rbacv1.ClusterRole{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: roleName}, role); err != nil {
					continue
				}
				for i := range required {
					if hasPermission(role.Rules, required[i].apiGroup, required[i].resource, required[i].verbs) {
						required[i].verbs = nil
					}
				}
			}

			for _, r := range required {
				Expect(r.verbs).To(BeNil(),
					"missing RBAC: %s %s %v for SA %s", r.apiGroup, r.resource, r.verbs, controllerSA)
			}
		})
	})
})

func hasPermission(rules []rbacv1.PolicyRule, apiGroup, resource string, verbs []string) bool {
	for _, rule := range rules {
		groupMatch := containsOrWildcard(rule.APIGroups, apiGroup)
		resourceMatch := containsOrWildcard(rule.Resources, resource)
		if !groupMatch || !resourceMatch {
			continue
		}
		allVerbsFound := true
		for _, verb := range verbs {
			if !containsOrWildcard(rule.Verbs, verb) {
				allVerbsFound = false
				break
			}
		}
		if allVerbsFound {
			return true
		}
	}
	return false
}

func containsOrWildcard(slice []string, item string) bool {
	for _, s := range slice {
		if s == "*" || s == item {
			return true
		}
	}
	return false
}

func extractUID(idOutput string) string {
	parts := strings.SplitN(idOutput, "=", 2)
	if len(parts) < 2 {
		return ""
	}
	uidPart := parts[1]
	parenIdx := strings.Index(uidPart, "(")
	if parenIdx > 0 {
		return uidPart[:parenIdx]
	}
	spaceIdx := strings.Index(uidPart, " ")
	if spaceIdx > 0 {
		return uidPart[:spaceIdx]
	}
	return uidPart
}
