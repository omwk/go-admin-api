package ingestion

// Drift is a structural change without any source values.
type Drift struct{ Kind, Path, OldType, NewType, Severity string }

func CompareStructure(baseline, observed Structure, required []string) []Drift {
	changes := []Drift{}
	for path, types := range observed {
		old, exists := baseline[path]
		if !exists {
			changes = append(changes, Drift{"field_added", path, "", types, "info"})
		} else if old != types {
			changes = append(changes, Drift{"type_changed", path, old, types, driftSeverity(path, required)})
		}
	}
	for path, types := range baseline {
		if _, exists := observed[path]; exists {
			continue
		}
		changes = append(changes, Drift{"field_removed", path, types, "", driftSeverity(path, required)})
	}
	return changes
}

func driftSeverity(path string, required []string) string {
	for _, candidate := range required {
		if candidate == path {
			return "critical"
		}
	}
	return "warning"
}
