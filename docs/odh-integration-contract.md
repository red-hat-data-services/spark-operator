# ODH / RHOAI Integration Contract

This document defines the contract between the **opendatahub-operator**
(via `spark-operator-module`) and the **Kubeflow Spark Operator** Kustomize
manifests.  It is the reference for both teams: changes to either side that
violate this contract must be coordinated.

See also: [Kustomize Installation Guide](kustomize-installation.md) for
standalone (non-ODH) deployment.

## Overview

The opendatahub-operator does **not** install the Spark Operator directly.
Instead it runs a component controller called `spark-operator-module`
(source: `spark-operator-module/` in this repo) that:

1. Reads a `SparkOperator` CR (`components.platform.opendatahub.io/v1alpha1`).
2. Selects the correct Kustomize overlay based on the platform type.
3. Injects the operand image via `params.env`.
4. Runs `kustomize build` and applies the rendered resources to the cluster.

## Environment Variables

The opendatahub-operator injects the following environment variables into the
`spark-operator-module` container.  The module does not probe OLM or the
cluster to derive these values.

| Env Var | Required | Default | Description | Example Values |
|---|---|---|---|---|
| `ODH_MODULE_OPERATOR_PLATFORM_TYPE` | No | `OpenDataHub` | Platform type. Determines which overlay is selected. | `OpenDataHub`, `ManagedRhoai`, `SelfManagedRhoai`, `XKS` |
| `ODH_PLATFORM_TYPE` | No | — | Legacy alias for `ODH_MODULE_OPERATOR_PLATFORM_TYPE`. Read only when the primary var is unset. | Same as above |
| `APPLICATIONS_NAMESPACE` | No | `opendatahub` | Namespace where KSO resources are deployed. | `opendatahub`, `redhat-ods-applications` |
| `RELATED_IMAGE_ODH_SPARK_OPERATOR_IMAGE` | Yes | — | The container image for both controller and webhook. | `quay.io/opendatahub/spark-operator:v2.5.1` |

> **Resolution order for platform type:** The module reads
> `ODH_MODULE_OPERATOR_PLATFORM_TYPE` first; if unset, it falls back to
> the legacy `ODH_PLATFORM_TYPE`; if both are unset, it defaults to
> `OpenDataHub`. See `spark-operator-module/pkg/sparkoperatormodule/platform.go`.

### Platform → Overlay Mapping

| `ODH_MODULE_OPERATOR_PLATFORM_TYPE` | Overlay Selected |
|---|---|
| `OpenDataHub` (or unset / unknown) | `config/overlays/odh/` |
| `ManagedRhoai` / `ManagedRHOAI` | `config/overlays/rhoai/` |
| `SelfManagedRhoai` / `SelfManagedRHOAI` | `config/overlays/rhoai/` |
| `XKS` | `config/overlays/odh/` |

The mapping is implemented in
`spark-operator-module/pkg/sparkoperatormodule/platform.go`
(`overlayForPlatform` and `parseInjectedPlatform`).

## Kustomize Overlay Structure

Each overlay inherits from `config/default/` and customises namespace, images,
labels, and network policies.

```
config/
├── default/                    Base overlay (spark-operator namespace)
│   ├── kustomization.yaml      Composes crd + rbac + manager + webhook
│   └── params.env              Default image variables
├── overlays/
│   ├── odh/                    OpenDataHub overlay
│   │   ├── kustomization.yaml  Namespace: opendatahub
│   │   └── params.env          ODH-specific images
│   └── rhoai/                  Red Hat OpenShift AI overlay
│       ├── kustomization.yaml  Namespace: redhat-ods-applications
│       └── params.env          RHOAI-specific images
```

### What each overlay adds over default

| Feature | default | odh | rhoai |
|---|---|---|---|
| Namespace | `spark-operator` | `opendatahub` | `redhat-ods-applications` |
| Namespace resource | Yes (creates it) | **No** (deleted by patch — ODH operator manages it) | **No** (deleted by patch — ODH operator manages it) |
| NetworkPolicy | No | Yes (4 policies) | Yes (4 policies) |
| PodMonitor | No | Yes (with `opendatahub.io/monitoring` label) | Yes (with `opendatahub.io/monitoring` label) |
| Spark RBAC (`config/spark-rbac`) | Not included | Included | Included |
| `app.kubernetes.io/part-of` label | Not set | `opendatahub` | `rhoai` |

## Image Injection (params.env)

Each overlay has a `params.env` file that declares image variables.
`kustomize` generates a `ConfigMap` from it and injects the values into
Deployment container image fields via `replacements`.

### Required keys in params.env

All `params.env` files **must** declare the same set of keys:

| Key | Description |
|---|---|
| `RELATED_IMAGE_ODH_SPARK_OPERATOR_IMAGE` | Source-of-truth image reference (used by ODH operator's `ApplyParams`) |
| `SPARK_OPERATOR_CONTROLLER_IMAGE` | Injected into `spark-operator-controller` Deployment |
| `SPARK_OPERATOR_WEBHOOK_IMAGE` | Injected into `spark-operator-webhook` Deployment |
| `OPERATOR_VERSION` | Operator version string |
| `OPERATOR_NAMESPACE` | Target namespace (must match the overlay's namespace) |

### How ApplyParams works

The ODH operator's `ApplyParams` function:
1. Reads `RELATED_IMAGE_ODH_SPARK_OPERATOR_IMAGE` from the module container env.
2. Writes it into `params.env` as both `SPARK_OPERATOR_CONTROLLER_IMAGE` and
   `SPARK_OPERATOR_WEBHOOK_IMAGE`.
3. Runs `kustomize build` on the selected overlay.
4. Applies the rendered manifests to the cluster.

This means the image in `params.env` is the **default/development** value.
In production, the ODH operator always overrides it with the operand image
from the module's `RELATED_IMAGE_*` env var.

## Required Resources (Deployment Contract)

After `kustomize build`, every overlay **must** produce the following
resources.  The `spark-operator-module` reconciler and RHOAI e2e tests depend
on their existence and naming.

### Core resources

| Kind | Name | Scope |
|---|---|---|
| `CustomResourceDefinition` | `sparkapplications.sparkoperator.k8s.io` | Cluster |
| `CustomResourceDefinition` | `scheduledsparkapplications.sparkoperator.k8s.io` | Cluster |
| `CustomResourceDefinition` | `sparkconnects.sparkoperator.k8s.io` | Cluster |
| `Deployment` | `spark-operator-controller` | Namespaced |
| `Deployment` | `spark-operator-webhook` | Namespaced |
| `Service` | `spark-operator-webhook-svc` | Namespaced |
| `ServiceAccount` | `spark-operator-controller` | Namespaced |
| `ServiceAccount` | `spark-operator-webhook` | Namespaced |
| `ClusterRole` | `spark-operator-controller` | Cluster |
| `ClusterRole` | `spark-operator-webhook` | Cluster |
| `ClusterRoleBinding` | `spark-operator-controller` | Cluster |
| `ClusterRoleBinding` | `spark-operator-webhook` | Cluster |
| `Role` | `spark-operator-controller` | Namespaced |
| `Role` | `spark-operator-webhook` | Namespaced |
| `RoleBinding` | `spark-operator-controller` | Namespaced |
| `RoleBinding` | `spark-operator-webhook` | Namespaced |
| `MutatingWebhookConfiguration` | `mutating-webhook-configuration` | Cluster |
| `ValidatingWebhookConfiguration` | `validating-webhook-configuration` | Cluster |

### Deployment container contract

| Deployment | Container Name | Image Source |
|---|---|---|
| `spark-operator-controller` | `controller` | `SPARK_OPERATOR_CONTROLLER_IMAGE` from `params.env` |
| `spark-operator-webhook` | `webhook` | `SPARK_OPERATOR_WEBHOOK_IMAGE` from `params.env` |

### Required pod labels

Both Deployments must set these labels on their pod templates:

| Label | Value | Used By |
|---|---|---|
| `app.kubernetes.io/name` | `spark-operator` | Pod discovery in e2e tests and monitoring |
| `app.kubernetes.io/component` | `controller` or `webhook` | Distinguishing pods in tests and `kubectl` |

### Webhook service reference

Both `MutatingWebhookConfiguration` and `ValidatingWebhookConfiguration`
must reference the webhook `Service`:

- **Service name:** `spark-operator-webhook-svc`
- **Service namespace:** must match the overlay's target namespace

## Contract Tests

Automated contract tests in `test/kustomize/overlay_build_test.go` validate
all of the above on every PR.  Run them locally:

```bash
make kustomize-lint
```

The `requiredOverlayResources` list in that file enumerates all 18 resources
from the table above.  The `TestParamsEnvConsistency` test additionally
verifies that all `params.env` files declare the same set of keys.

## Versioning and Breaking Changes

When making changes that affect this contract:

1. **Adding a new resource:** Add it to `requiredOverlayResources` in
   `test/kustomize/overlay_build_test.go` so the contract test catches
   future regressions.
2. **Renaming a resource:** Update both the Kustomize manifests and the
   contract test.  Coordinate with the `spark-operator-module` team if
   the module references the resource by name.
3. **Adding a new params.env key:** Add it to all three `params.env` files
   (default, odh, rhoai).  The `TestParamsEnvConsistency` test will fail
   if any file is missing a key.
4. **Changing the overlay structure:** Update this document and the contract
   test.  Coordinate with the ODH operator team.
