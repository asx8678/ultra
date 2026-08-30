package fabric

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var typeScriptIdentifierPattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// Declarations returns a deterministic conservative guest declaration set.
// Schema-to-TypeScript specialization is intentionally a compiler-layer
// concern; the host registry still performs authoritative schema validation.
func (r *Registry) Declarations(view *CapabilityView) (string, error) {
	catalog, err := r.Catalog(view)
	if err != nil {
		return "", err
	}
	providers := make(map[string][]string)
	for _, action := range catalog {
		if !typeScriptIdentifierPattern.MatchString(action.Provider) {
			continue
		}
		if strings.Contains(action.Descriptor.Name, ".") {
			continue
		}
		providers[action.Provider] = append(providers[action.Provider], action.Descriptor.Name)
	}
	providerNames := make([]string, 0, len(providers))
	for provider := range providers {
		providerNames = append(providerNames, provider)
	}
	slices.Sort(providerNames)
	var declarations strings.Builder
	for _, provider := range providerNames {
		declarations.WriteString("declare const ")
		declarations.WriteString(provider)
		declarations.WriteString(": {\n")
		actions := providers[provider]
		slices.Sort(actions)
		for _, action := range actions {
			declarations.WriteString("  ")
			if typeScriptIdentifierPattern.MatchString(action) {
				declarations.WriteString(action)
			} else {
				declarations.WriteString(fmt.Sprintf("%q", action))
			}
			declarations.WriteString("(args: Record<string, unknown>): Promise<unknown>;\n")
		}
		declarations.WriteString("};\n")
	}
	return declarations.String(), nil
}
