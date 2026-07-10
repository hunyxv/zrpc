package metadata

// MD carries request or response metadata values.
type MD map[string][]string

// New returns an empty metadata map.
func New() MD {
	return MD{}
}

// Set replaces all values for key.
func (md MD) Set(key, value string) {
	md[key] = []string{value}
}

// Append adds value for key.
func (md MD) Append(key, value string) {
	md[key] = append(md[key], value)
}

// Get returns the first value for key.
func (md MD) Get(key string) string {
	values := md[key]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// Values returns all values for key.
func (md MD) Values(key string) []string {
	return cloneValues(md[key])
}

// Copy returns a deep copy of md.
func (md MD) Copy() MD {
	cp := make(MD, len(md))
	for key, values := range md {
		cp[key] = cloneValues(values)
	}
	return cp
}

// Merge combines metadata maps in order.
func Merge(mds ...MD) MD {
	merged := MD{}
	for _, md := range mds {
		for key, values := range md {
			merged[key] = append(merged[key], values...)
		}
	}
	return merged
}

func cloneValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cp := make([]string, len(values))
	copy(cp, values)
	return cp
}
