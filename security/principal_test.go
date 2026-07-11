package security

import (
	"context"
	"testing"
)

func TestPrincipalContext(t *testing.T) {
	p := &Principal{Subject: "user-1", Claims: map[string]string{"tenant": "t1"}}
	ctx := ContextWithPrincipal(context.Background(), p)

	got, ok := PrincipalFromContext(ctx)
	if !ok {
		t.Fatal("principal missing")
	}
	if got.Subject != "user-1" || got.Claims["tenant"] != "t1" {
		t.Fatalf("principal = %#v", got)
	}
}

func TestPrincipalContextCopiesClaims(t *testing.T) {
	claims := map[string]string{"tenant": "t1"}
	ctx := ContextWithPrincipal(context.Background(), &Principal{Subject: "user-1", Claims: claims})
	claims["tenant"] = "changed"

	got, ok := PrincipalFromContext(ctx)
	if !ok {
		t.Fatal("principal missing")
	}
	if got.Claims["tenant"] != "t1" {
		t.Fatalf("stored claim = %q, want t1", got.Claims["tenant"])
	}

	got.Claims["tenant"] = "mutated"
	again, ok := PrincipalFromContext(ctx)
	if !ok {
		t.Fatal("principal missing on second lookup")
	}
	if again.Claims["tenant"] != "t1" {
		t.Fatalf("returned claim mutated stored principal: %q", again.Claims["tenant"])
	}
}

func TestPrincipalFromContextMissing(t *testing.T) {
	if got, ok := PrincipalFromContext(context.Background()); ok || got != nil {
		t.Fatalf("PrincipalFromContext() = %#v, %v; want nil, false", got, ok)
	}
}
