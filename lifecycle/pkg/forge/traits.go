package forge

import (
	"fmt"
	"sort"
	"strings"
)

func NormalizeTraits(traits []string) ([]string, error) {
	if len(traits) == 0 {
		return nil, nil
	}

	seen := make(map[string]string, len(traits))
	for _, raw := range traits {
		trait := strings.TrimSpace(raw)
		if trait == "" {
			continue
		}
		if !strings.HasPrefix(trait, "+") && !strings.HasPrefix(trait, "-") {
			return nil, fmt.Errorf("trait %q must start with '+' or '-'", raw)
		}

		name := trait[1:]
		if name == "" {
			return nil, fmt.Errorf("trait %q is missing a name", raw)
		}

		if existing, ok := seen[name]; ok && existing != trait[:1] {
			return nil, fmt.Errorf("trait %q conflicts with %q", raw, existing+name)
		}
		seen[name] = trait[:1]
	}

	normalized := make([]string, 0, len(seen))
	for name, sign := range seen {
		normalized = append(normalized, sign+name)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func NormalizeTraitsExpression(traits []string) (string, []string, error) {
	normalized, err := NormalizeTraits(traits)
	if err != nil {
		return "", nil, err
	}
	return strings.Join(normalized, ","), normalized, nil
}

func matchesTraitFilter(selected []string, required []string) bool {
	if len(required) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(selected))
	for _, item := range selected {
		set[item] = struct{}{}
	}
	for _, item := range required {
		if _, ok := set[item]; !ok {
			return false
		}
	}
	return true
}
