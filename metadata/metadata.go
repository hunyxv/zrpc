package metadata

import "strings"

// MD carries request or response metadata values.
type MD map[string][]string

// New returns an empty metadata map.
func New() MD {
	return MD{}
}

// Set replaces all values for key.
func (md *MD) Set(key, value string) {
	m := md.ensure()
	if m == nil {
		return
	}
	m[canonicalKey(key)] = []string{value}
}

// Append adds value for key.
func (md *MD) Append(key, value string) {
	m := md.ensure()
	if m == nil {
		return
	}
	key = canonicalKey(key)
	m[key] = append(m[key], value)
}

// Get returns the first value for key.
func (md MD) Get(key string) string {
	values := md.lookup(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// Values returns all values for key.
func (md MD) Values(key string) []string {
	return cloneValues(md.lookup(key))
}

// Copy returns a deep copy of md.
func (md MD) Copy() MD {
	cp := make(MD, len(md))
	for key, values := range md {
		cp[canonicalKey(key)] = append(cp[canonicalKey(key)], values...)
	}
	return cp
}

// Merge combines metadata maps in order.
func Merge(mds ...MD) MD {
	merged := MD{}
	for _, md := range mds {
		for key, values := range md {
			key = canonicalKey(key)
			merged[key] = append(merged[key], values...)
		}
	}
	return merged
}

func (md *MD) ensure() MD {
	if md == nil {
		return nil
	}
	if *md == nil {
		*md = New()
	}
	return *md
}

func (md MD) lookup(key string) []string {
	if len(md) == 0 {
		return nil
	}
	key = canonicalKey(key)
	if values, ok := md[key]; ok {
		return values
	}
	for existing, values := range md {
		if canonicalKey(existing) == key {
			return values
		}
	}
	return nil
}

func canonicalKey(key string) string {
	return strings.ToLower(key)
}

func cloneValues(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	cp := make([]string, len(values))
	copy(cp, values)
	return cp
}
