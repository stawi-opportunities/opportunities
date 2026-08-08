// Package profilecontacts stores CV-discovered contact details in the
// platform Profile service as standalone contacts (CreateContact) and
// resolves them by id via GetContacts.
//
// Two lanes (do not mix):
//
//  1. Profile-attached contacts (identity) — GetById(profile).contacts[]
//     Used by checkout and notify only.
//
//  2. Standalone CV contacts — CreateContact + GetContacts(ids).
//     Product stores contact_ids (e.g. cv_contact_ids) and resolves
//     ContactObjects when needed. Never attach these to the person profile
//     just to read them back.
package profilecontacts

import (
	"context"
	"fmt"
	"strings"

	"buf.build/gen/go/antinvestor/profile/connectrpc/go/profile/v1/profilev1connect"
	profilev1 "buf.build/gen/go/antinvestor/profile/protocolbuffers/go/profile/v1"
	"connectrpc.com/connect"
	"github.com/pitabwire/util"
	"google.golang.org/protobuf/types/known/structpb"
)

// MaxContactsPerSync caps how many distinct CV details we CreateContact per call.
const MaxContactsPerSync = 10

// MaxResolveBatch is the max ids sent in one GetContacts call (proto max 100).
const MaxResolveBatch = 100

// Ref is a platform contact (id + optional detail for display).
type Ref struct {
	ID     string `json:"id"`
	Detail string `json:"detail,omitempty"`
	// Type is ProfileService ContactType name when known (EMAIL, MSISDN).
	Type string `json:"type,omitempty"`
}

// Directory creates/reuses standalone contacts and resolves them by id.
type Directory interface {
	// EnsureDetails creates (or reuses) standalone contacts for each detail.
	// knownIDs are previously stored contact_ids always retained.
	EnsureDetails(ctx context.Context, details []string, knownIDs []string) (refs []Ref, err error)
	// Resolve loads ContactObjects for the given ids (one or many).
	// Works regardless of profile attachment. Missing ids returned separately.
	Resolve(ctx context.Context, ids []string) (found []Ref, missing []string, err error)
}

// ProfileClient is the subset of ProfileServiceClient we need (testable).
type ProfileClient interface {
	CreateContact(context.Context, *connect.Request[profilev1.CreateContactRequest]) (*connect.Response[profilev1.CreateContactResponse], error)
	GetContacts(context.Context, *connect.Request[profilev1.GetContactsRequest]) (*connect.Response[profilev1.GetContactsResponse], error)
}

// Service backs Directory with ProfileService RPCs.
type Service struct {
	Client ProfileClient
	Source string
}

// New returns a Directory when client is non-nil; otherwise Nil.
func New(client profilev1connect.ProfileServiceClient) Directory {
	if client == nil {
		return Nil{}
	}
	return &Service{Client: client, Source: "opportunities_cv"}
}

// Nil is a no-op Directory when ProfileService is not configured.
type Nil struct{}

func (Nil) EnsureDetails(context.Context, []string, []string) ([]Ref, error) {
	return nil, nil
}
func (Nil) Resolve(context.Context, []string) ([]Ref, []string, error) {
	return nil, nil, nil
}

// EnsureDetails stores each detail via CreateContact (standalone).
func (s *Service) EnsureDetails(
	ctx context.Context,
	details []string,
	knownIDs []string,
) (refs []Ref, err error) {
	if s == nil || s.Client == nil {
		return nil, nil
	}

	seenID := make(map[string]struct{}, len(knownIDs)+len(details))
	out := make([]Ref, 0, len(knownIDs)+len(details))
	for _, id := range knownIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seenID[id]; ok {
			continue
		}
		seenID[id] = struct{}{}
		out = append(out, Ref{ID: id})
	}

	source := s.Source
	if source == "" {
		source = "opportunities_cv"
	}
	extras, _ := structpb.NewStruct(map[string]any{
		"source":  source,
		"product": "opportunities",
		"lane":    "cv_standalone",
	})

	log := util.Log(ctx)
	var firstErr error
	created := 0
	seenDetail := make(map[string]struct{}, len(details))

	for _, raw := range details {
		if created >= MaxContactsPerSync {
			break
		}
		detail := strings.TrimSpace(raw)
		if detail == "" {
			continue
		}
		key := detailKey(detail)
		if _, ok := seenDetail[key]; ok {
			continue
		}
		seenDetail[key] = struct{}{}

		req := &profilev1.CreateContactRequest{
			Contact: detail,
			Extras:  extras,
		}
		resp, cErr := s.Client.CreateContact(ctx, connect.NewRequest(req))
		if cErr != nil {
			log.WithError(cErr).WithField("contact", detail).
				Warn("profilecontacts: CreateContact failed")
			if firstErr == nil {
				firstErr = cErr
			}
			continue
		}
		if resp == nil || resp.Msg == nil || resp.Msg.GetData() == nil {
			continue
		}
		c := resp.Msg.GetData()
		id := strings.TrimSpace(c.GetId())
		if id == "" {
			continue
		}
		created++
		if _, ok := seenID[id]; ok {
			for i := range out {
				if out[i].ID == id && out[i].Detail == "" {
					out[i].Detail = firstNonEmpty(c.GetDetail(), detail)
					out[i].Type = c.GetType().String()
				}
			}
			continue
		}
		seenID[id] = struct{}{}
		out = append(out, Ref{
			ID:     id,
			Detail: firstNonEmpty(c.GetDetail(), detail),
			Type:   c.GetType().String(),
		})
		log.WithField("contact_id", id).Info("profilecontacts: standalone CV contact stored")
	}

	if firstErr != nil && len(out) == 0 {
		return nil, fmt.Errorf("profilecontacts: EnsureDetails: %w", firstErr)
	}
	return out, firstErr
}

// Resolve loads ContactObjects for the given ids via GetContacts (1..N).
func (s *Service) Resolve(
	ctx context.Context,
	ids []string,
) (found []Ref, missing []string, err error) {
	if s == nil || s.Client == nil {
		return nil, nil, nil
	}
	clean := cleanIDs(ids)
	if len(clean) == 0 {
		return nil, nil, nil
	}

	// Chunk to MaxResolveBatch.
	for i := 0; i < len(clean); i += MaxResolveBatch {
		end := i + MaxResolveBatch
		if end > len(clean) {
			end = len(clean)
		}
		chunk := clean[i:end]
		resp, rErr := s.Client.GetContacts(ctx, connect.NewRequest(&profilev1.GetContactsRequest{
			Ids: chunk,
		}))
		if rErr != nil {
			return found, missing, fmt.Errorf("profilecontacts: GetContacts: %w", rErr)
		}
		if resp != nil && resp.Msg != nil {
			for _, c := range resp.Msg.GetData() {
				if c == nil || strings.TrimSpace(c.GetId()) == "" {
					continue
				}
				found = append(found, Ref{
					ID:     c.GetId(),
					Detail: c.GetDetail(),
					Type:   c.GetType().String(),
				})
			}
			missing = append(missing, resp.Msg.GetMissingIds()...)
		}
	}
	return found, missing, nil
}

func detailKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return strings.TrimSpace(b)
}

// CollectDetails merges extract/heuristic contact strings into one opaque list.
func CollectDetails(parts ...[]string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, group := range parts {
		for _, raw := range group {
			for _, p := range splitMulti(raw) {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				k := detailKey(p)
				if _, ok := seen[k]; ok {
					continue
				}
				seen[k] = struct{}{}
				out = append(out, p)
			}
		}
	}
	return out
}

// IDs extracts contact_id strings from refs.
func IDs(refs []Ref) []string {
	return cleanIDs(func() []string {
		out := make([]string, len(refs))
		for i, r := range refs {
			out[i] = r.ID
		}
		return out
	}())
}

func cleanIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func splitMulti(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if !strings.ContainsAny(s, "·|;\n/") {
		return []string{s}
	}
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '·' || r == '|' || r == ';' || r == '\n' || r == '/'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
