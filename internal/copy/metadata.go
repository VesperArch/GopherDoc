package copy

// Metadata returns a deep copy of a metadata map. Nested maps, slices,
// and byte slices are recursively copied; scalar values pass through by
// value (Go's natural copy semantics). reflect is intentionally avoided.
func Metadata(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = deepCopyValue(v)
	}
	return dst
}

func deepCopyValue(val any) any {
	switch v := val.(type) {
	case map[string]any:
		cp := make(map[string]any, len(v))
		for k, inner := range v {
			cp[k] = deepCopyValue(inner)
		}
		return cp
	case []any:
		cp := make([]any, len(v))
		for i, inner := range v {
			cp[i] = deepCopyValue(inner)
		}
		return cp
	case []string:
		cp := make([]string, len(v))
		copy(cp, v)
		return cp
	case []byte:
		cp := make([]byte, len(v))
		copy(cp, v)
		return cp
	default:
		return v
	}
}
