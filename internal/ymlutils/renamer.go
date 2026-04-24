package ymlutils

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// AddSuffixToNameInManifests walks dir and appends suffix to metadata.name and spec.group
// in every .yml file. Delegates per-file work to addSuffixToNameInContent.
func AddSuffixToNameInManifests(manifestsDir, suffix string) error {
	return fs.WalkDir(os.DirFS(manifestsDir), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".yml") {
			return nil
		}
		fullPath := filepath.Join(manifestsDir, path)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return err
		}
		updated, err := addSuffixToNameInContent(data, suffix)
		if err != nil {
			return err
		}
		return os.WriteFile(fullPath, updated, 0644)
	})
}

func addSuffixToNameInContent(data []byte, suffix string) ([]byte, error) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(data, &obj); err != nil {
		return nil, err
	}

	if metadata, ok := obj["metadata"].(map[string]interface{}); ok {
		if name, ok := metadata["name"].(string); ok {
			metadata["name"] = name + suffix
		}
	}

	if spec, ok := obj["spec"].(map[string]interface{}); ok {
		if group, ok := spec["group"].(string); ok {
			spec["group"] = group + suffix
		}
	}

	out, err := yaml.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateChartVersion sets the version field in Chart.yaml at chartPath/Chart.yaml.
func UpdateChartVersion(chartPath, newVersion string) error {
	filename := filepath.Join(chartPath, "Chart.yaml")
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	updated, err := updateChartVersionInContent(data, newVersion)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, updated, 0644)
}

func updateChartVersionInContent(data []byte, newVersion string) ([]byte, error) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	obj["version"] = newVersion
	return yaml.Marshal(obj)
}
