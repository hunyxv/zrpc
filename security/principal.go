package security

import "context"

// Principal 表示认证后写入 context 的调用方身份。
type Principal struct {
	// Subject 是主体标识，例如用户 ID、服务名或客户端 ID。
	Subject string
	// Claims 保存认证系统附加的键值声明。
	Claims map[string]string
}

type principalKey struct{}

// ContextWithPrincipal 返回携带调用方身份副本的 context。
func ContextWithPrincipal(ctx context.Context, principal *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, clonePrincipal(principal))
}

// PrincipalFromContext 从 context 中读取调用方身份副本。
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
