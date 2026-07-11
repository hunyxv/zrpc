package security

import "context"

type Principal struct {
	Subject string
	Claims  map[string]string
}

type principalKey struct{}

func ContextWithPrincipal(ctx context.Context, principal *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, clonePrincipal(principal))
}

func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(*Principal)
	if !ok || principal == nil {
		return principal, ok
	}
	return clonePrincipal(principal), true
}

func clonePrincipal(principal *Principal) *Principal {
	if principal == nil {
		return nil
	}
	cp := &Principal{
		Subject: principal.Subject,
	}
	if principal.Claims != nil {
		cp.Claims = make(map[string]string, len(principal.Claims))
		for key, value := range principal.Claims {
			cp.Claims[key] = value
		}
	}
	return cp
}
