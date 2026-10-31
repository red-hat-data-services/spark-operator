package sparkoperatormodule

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/opendatahub-io/odh-platform-utilities/api/common"

	platformv1alpha1 "github.com/opendatahub-io/spark-operator-module/pkg/apis/v1alpha1"
)

type componentMetadata struct {
	Releases []common.ComponentRelease `yaml:"releases"`
}

var fallbackReleases = []common.ComponentRelease{
	{Name: "Spark Operator", Version: "unknown", RepoURL: "https://github.com/opendatahub-io/spark-operator"},
}

func loadComponentReleases(manifestsPath string, componentDirs []string) ([]common.ComponentRelease, error) {
	seen := make(map[string]bool)
	var all []common.ComponentRelease

	for _, dir := range componentDirs {
		path := filepath.Join(manifestsPath, dir, "config", "component_metadata.yaml")

		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}

		var meta componentMetadata
		if err := yaml.Unmarshal(data, &meta); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}

		for _, r := range meta.Releases {
			if r.Name == "" || seen[r.Name] {
				continue
			}
			if r.Version == "" {
				r.Version = "unknown"
			}
			seen[r.Name] = true
			all = append(all, r)
		}
	}

	if len(all) == 0 {
		return fallbackReleases, nil
	}

	return all, nil
}

func (r *SparkOperatorModuleReconciler) getPlatformVersion(ctx context.Context) (string, error) {
	key := types.NamespacedName{
		Name:      platformConfigMapName,
		Namespace: r.getApplicationsNamespace(ctx),
	}
	config := &corev1.ConfigMap{}
	if err := r.Get(ctx, key, config); err != nil {
		return "", fmt.Errorf("reading platform config ConfigMap %s: %w", key, err)
	}

	version := config.Data[platformVersionKey]
	if strings.TrimSpace(version) == "" {
		return "", fmt.Errorf("platform config ConfigMap %s has an empty %q", key, platformVersionKey)
	}
	return version, nil
}

func setPlatformRelease(sparkOperator *platformv1alpha1.SparkOperator, version string) {
	for i := range sparkOperator.Status.Releases {
		if sparkOperator.Status.Releases[i].Name == platformReleaseName {
			sparkOperator.Status.Releases[i].Version = version
			return
		}
	}

	sparkOperator.Status.Releases = append(sparkOperator.Status.Releases, common.ComponentRelease{
		Name:    platformReleaseName,
		Version: version,
	})
}
