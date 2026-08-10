package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/golang-jwt/jwt/v5"
	"github.com/pitabwire/frame/v2/security"
	connectInterceptors "github.com/pitabwire/frame/v2/security/interceptors/connect"
	"github.com/pitabwire/util"

	"github.com/stawi-opportunities/opportunities/apps/ats/gen/ats/v1/atsv1connect"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/business"
)

// DevHeaderClaimsInterceptor injects tenancy claims from X-Profile-ID /
// X-Tenant-ID / X-Partition-ID when no JWT claims are present (local/dev).
type DevHeaderClaimsInterceptor struct{}

func (DevHeaderClaimsInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		ctx = injectDevClaims(ctx, req.Header())
		return next(ctx, req)
	}
}

func (DevHeaderClaimsInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (DevHeaderClaimsInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		ctx = injectDevClaims(ctx, conn.RequestHeader())
		return next(ctx, conn)
	}
}

func injectDevClaims(ctx context.Context, h http.Header) context.Context {
	if security.ClaimsFromContext(ctx) != nil {
		return ctx
	}
	pid := h.Get("X-Profile-ID")
	tid := h.Get("X-Tenant-ID")
	part := h.Get("X-Partition-ID")
	if pid == "" || tid == "" || part == "" {
		return ctx
	}
	c := &security.AuthenticationClaims{
		TenantID:    tid,
		PartitionID: part,
		ProfileID:   pid,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: pid,
		},
	}
	return c.ClaimsToContext(ctx)
}

// NewConnectMux mounts AtsService (Connect) + /healthz.
// allowDevHeaders=true skips JWT and accepts tenancy headers.
func NewConnectMux(
	ctx context.Context,
	biz *business.Service,
	authenticator security.Authenticator,
	allowDevHeaders bool,
) (http.Handler, error) {
	impl := NewConnectServer(biz)
	var interceptors []connect.Interceptor
	if allowDevHeaders {
		interceptors = append(interceptors, DevHeaderClaimsInterceptor{})
		util.Log(ctx).Warn("ats: Connect using dev tenancy headers (AUTH_REQUIRE_JWT=false)")
	} else {
		if authenticator == nil {
			return nil, errors.New("ats: authenticator required when AUTH_REQUIRE_JWT=true")
		}
		list, err := connectInterceptors.DefaultList(ctx, authenticator)
		if err != nil {
			return nil, err
		}
		interceptors = list
		util.Log(ctx).Info("ats: Connect using JWT + default Frame interceptors")
	}

	path, h := atsv1connect.NewAtsServiceHandler(impl, connect.WithInterceptors(interceptors...))
	mux := http.NewServeMux()
	// path is "/ats.v1.AtsService/" — handler routes full procedure paths.
	mux.Handle(path, h)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"service": "ats",
			"api":     "connect",
			"path":    path,
		})
	})
	return mux, nil
}

// CORSMiddleware wraps a handler with permissive CORS for the Vite SPA.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Connect-Protocol-Version, Connect-Timeout-Ms, X-Profile-ID, X-Tenant-ID, X-Partition-ID, Idempotency-Key")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Expose-Headers", "Connect-Content-Encoding, Connect-Accept-Encoding")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
