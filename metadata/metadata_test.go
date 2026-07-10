package metadata

import (
	"slices"
	"testing"
)

func TestNewSetAppendGetValues(t *testing.T) {
	md := New()
	if md == nil {
		t.Fatal("New() returned nil")
	}

	md.Set("Trace-ID", "abc")
	md.Append("Trace-ID", "def")

	if got := md.Get("trace-id"); got != "abc" {
		t.Fatalf("Get(trace-id) = %q, want %q", got, "abc")
	}
	if got := md.Get("missing"); got != "" {
		t.Fatalf("Get(missing) = %q, want empty string", got)
	}
	if got := md.Values("TRACE-ID"); !slices.Equal(got, []string{"abc", "def"}) {
		t.Fatalf("Values(TRACE-ID) = %#v, want %#v", got, []string{"abc", "def"})
	}
}

func TestZeroValueMetadataIsWritable(t *testing.T) {
	var md MD
	md.Set("Authorization", "Bearer token")
	md.Append("Authorization", "tenant=one")

	if got := md.Values("authorization"); !slices.Equal(got, []string{"Bearer token", "tenant=one"}) {
		t.Fatalf("zero-value metadata values = %#v, want %#v", got, []string{"Bearer token", "tenant=one"})
	}
}

func TestValuesDoesNotShareUnderlyingSlice(t *testing.T) {
	md := New()
	md.Append("k", "v1")
	md.Append("k", "v2")

	values := md.Values("k")
	values[0] = "changed"

	if got := md.Values("k"); !slices.Equal(got, []string{"v1", "v2"}) {
		t.Fatalf("Values(k) after caller mutation = %#v, want %#v", got, []string{"v1", "v2"})
	}
}

func TestCopyDoesNotShareUnderlyingSlices(t *testing.T) {
	md := New()
	md.Append("k", "v1")
	md.Append("k", "v2")

	cp := md.Copy()
	cp["k"][0] = "changed"

	if got := md.Values("k"); !slices.Equal(got, []string{"v1", "v2"}) {
		t.Fatalf("original Values(k) after copy mutation = %#v, want %#v", got, []string{"v1", "v2"})
	}
}

func TestMergeAppendsValuesInArgumentOrder(t *testing.T) {
	first := New()
	first.Append("k", "a")
	first.Append("k", "b")
	first.Set("first-only", "1")

	second := New()
	second.Append("k", "c")
	second.Set("second-only", "2")

	merged := Merge(first, second)

	if got := merged.Values("k"); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("merged Values(k) = %#v, want %#v", got, []string{"a", "b", "c"})
	}
	if got := merged.Values("first-only"); !slices.Equal(got, []string{"1"}) {
		t.Fatalf("merged Values(first-only) = %#v, want %#v", got, []string{"1"})
	}
	if got := merged.Values("second-only"); !slices.Equal(got, []string{"2"}) {
		t.Fatalf("merged Values(second-only) = %#v, want %#v", got, []string{"2"})
	}

	first["k"][0] = "changed"
	if got := merged.Values("k"); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("merged Values(k) after source mutation = %#v, want %#v", got, []string{"a", "b", "c"})
	}
}

func TestMergeCanonicalizesKeys(t *testing.T) {
	first := New()
	first.Set("Trace-ID", "a")
	second := New()
	second.Append("trace-id", "b")

	merged := Merge(first, second)

	if got := merged.Values("TRACE-ID"); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("merged Values(TRACE-ID) = %#v, want %#v", got, []string{"a", "b"})
	}
}
