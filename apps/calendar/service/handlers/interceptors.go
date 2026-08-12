package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/antinvestor/common/v2/permissions"
	"github.com/golang-jwt/jwt/v5"
	"github.com/pitabwire/frame/v2/security"
	"github.com/pitabwire/frame/v2/security/authorizer"
	connectInterceptors "github.com/pitabwire/frame/v2/security/interceptors/connect"
	"github.com/pitabwire/util"
	"google.golang.org/protobuf/reflect/protoreflect"

	calendarv1 "github.com/stawi-opportunities/opportunities/apps/calendar/gen/calendar/v1"
	"github.com/stawi-opportunities/opportunities/apps/calendar/gen/calendar/v1/calendarv1connect"
	"github.com/stawi-opportunities/opportunities/apps/calendar/service/business"
)

func ServiceDescriptor() protoreflect.ServiceDescriptor {
	return calendarv1.File_calendar_v1_calendar_proto.Services().ByName("CalendarService")
}

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
		TenantID: tid, PartitionID: part, ProfileID: pid,
		RegisteredClaims: jwt.RegisteredClaims{Subject: pid},
	}
	return c.ClaimsToContext(ctx)
}

type ConnectOptions struct {
	Authenticator      security.Authenticator
	Authorizer         security.Authorizer
	AllowDevHeaders    bool
	EnforcePermissions bool
}

func NewConnectMux(ctx context.Context, biz *business.Service, opts ConnectOptions) (http.Handler, error) {
	impl := NewConnectServer(biz)
	interceptors, err := buildInterceptors(ctx, opts)
	if err != nil {
		return nil, err
	}
	path, h := calendarv1connect.NewCalendarServiceHandler(impl, connect.WithInterceptors(interceptors...))
	mux := http.NewServeMux()
	mux.Handle(path, h)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok", "service": "calendar", "api": "connect",
			"path": path, "namespace": "service_calendar",
			"permissions_enforce": opts.EnforcePermissions,
		})
	})
	return mux, nil
}

func buildInterceptors(ctx context.Context, opts ConnectOptions) ([]connect.Interceptor, error) {
	log := util.Log(ctx)
	if opts.AllowDevHeaders {
		log.Warn("calendar: Connect using dev tenancy headers")
		return []connect.Interceptor{DevHeaderClaimsInterceptor{}}, nil
	}
	if opts.Authenticator == nil {
		return nil, errors.New("calendar: authenticator required when AUTH_REQUIRE_JWT=true")
	}
	var more []connect.Interceptor
	if opts.EnforcePermissions {
		if opts.Authorizer == nil {
			return nil, errors.New("calendar: authorizer required when EnforcePermissions=true")
		}
		sd := ServiceDescriptor()
		ns := permissions.ForService(sd).Namespace
		if ns == "" {
			ns = "service_calendar"
		}
		functionChecker := authorizer.NewFunctionChecker(opts.Authorizer, ns)
		procMap := permissions.BuildProcedureMap(sd)
		more = append(more, connectInterceptors.NewFunctionAccessInterceptor(functionChecker, procMap))
	}
	return connectInterceptors.DefaultList(ctx, opts.Authenticator, more...)
}

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
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
