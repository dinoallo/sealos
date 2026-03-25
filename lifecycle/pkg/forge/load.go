package forge

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

func LoadClusterForge(path string) (*ClusterForge, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg ClusterForge
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode ClusterForge %s: %w", path, err)
	}
	if cfg.APIVersion == "" {
		cfg.APIVersion = DefaultAPIVersion
	}
	if cfg.Kind == "" {
		cfg.Kind = ClusterForgeKind
	}
	if cfg.Kind != ClusterForgeKind {
		return nil, fmt.Errorf("%s is not a %s document", path, ClusterForgeKind)
	}
	return &cfg, nil
}

func LoadForgefile(path string) (*Forgefile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Forgefile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode Forgefile %s: %w", path, err)
	}
	return &cfg, nil
}

func FindForgefile(root, component, version string) (string, error) {
	componentPath := filepath.FromSlash(component)
	candidates := []string{}
	if version != "" {
		candidates = append(candidates,
			filepath.Join(root, "apps", componentPath, version, "Forgefile"),
			filepath.Join(root, componentPath, version, "Forgefile"),
		)
	}
	candidates = append(candidates,
		filepath.Join(root, "apps", componentPath, "Forgefile"),
		filepath.Join(root, componentPath, "Forgefile"),
	)

	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("Forgefile not found for component %q version %q in overlay %q", component, version, root)
}
