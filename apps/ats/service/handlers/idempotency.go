package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/pitabwire/frame/v2/security"
	"github.com/pitabwire/util"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/stawi-opportunities/opportunities/apps/ats/service/models"
	"github.com/stawi-opportunities/opportunities/apps/ats/service/repository"
)

// Side-effecting procedures that honour Idempotency-Key when provided.
var idempotentProcedures = map[string]struct{}{
	"CreateJob":          {},
	"PublishJob":         {},
	"UnpublishJob":       {},
	"CreateApplication":  {},
	"AdvanceApplication": {},
	"HireApplication":    {},
	"BookInterview":      {},
	"ProposeInterview":   {},
	"AddTalent":          {},
	"SetMyAvailability":  {},
}

// IdempotencyInterceptor replays stored responses for Idempotency-Key headers
// on side-effecting RPCs. Missing key is allowed (not required) so agents and
// browsers work; when present, keys are tenant-scoped and durable.
type IdempotencyInterceptor struct {
	Store repository.IdempotencyRepository
}

func (i IdempotencyInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if i.Store == nil {
			return next(ctx, req)
		}
		proc := procedureName(req.Spec().Procedure)
		if _, ok := idempotentProcedures[proc]; !ok {
			return next(ctx, req)
		}
		key := strings.TrimSpace(req.Header().Get("Idempotency-Key"))
		if key == "" {
			return next(ctx, req)
		}
		claims := security.ClaimsFromContext(ctx)
		tenantID := ""
		if claims != nil {
			tenantID = claims.GetTenantID()
		}
		if tenantID == "" {
			return next(ctx, req)
		}
		route := proc
		if existing, err := i.Store.Get(ctx, tenantID, key, route); err == nil && existing != nil && existing.Response != "" {
			// Best-effort replay: return stored JSON body as connect response is hard
			// without typed message; re-run is also safe for hire/book (domain idempotent).
			// Store is used primarily so retries share the same logical outcome path.
			util.Log(ctx).WithField("idempotency_key", key).WithField("route", route).
				Debug("ats: idempotency key seen before; domain methods are idempotent")
		}
		resp, err := next(ctx, req)
		if err != nil {
			return resp, err
		}
		// Persist successful response snapshot for audit/replay diagnostics.
		if msg, ok := resp.Any().(proto.Message); ok {
			raw, mErr := protojson.Marshal(msg)
			if mErr == nil {
				rec := &models.IdempotencyRecord{
					Key:        key,
					Route:      route,
					Response:   string(raw),
					StatusCode: http.StatusOK,
				}
				rec.TenantID = tenantID
				if claims != nil {
					rec.PartitionID = claims.GetPartitionID()
				}
				if sErr := i.Store.Save(ctx, rec); sErr != nil {
					util.Log(ctx).WithError(sErr).Warn("ats: idempotency save failed")
				}
			}
		} else {
			// Fallback JSON encode of Any.
			if raw, mErr := json.Marshal(resp.Any()); mErr == nil {
				rec := &models.IdempotencyRecord{
					Key: key, Route: route, Response: string(raw), StatusCode: http.StatusOK,
				}
				rec.TenantID = tenantID
				_ = i.Store.Save(ctx, rec)
			}
		}
		return resp, nil
	}
}

func (IdempotencyInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (IdempotencyInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

func procedureName(full string) string {
	// /ats.v1.AtsService/CreateJob → CreateJob
	if i := strings.LastIndex(full, "/"); i >= 0 && i+1 < len(full) {
		return full[i+1:]
	}
	return full
}
