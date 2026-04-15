package schemaorg

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"stawi.jobs/internal/connectors/httpx"
	"stawi.jobs/internal/domain"
)

type Connector struct{ client *httpx.Client }

func New(client *httpx.Client) *Connector    { return &Connector{client: client} }
func (c *Connector) Type() domain.SourceType { return domain.SourceSchemaOrg }

var scriptJSONLD = regexp.MustCompile(`(?is)<script[^>]+application/ld\+json[^>]*>(.*?)</script>`)

func (c *Connector) Crawl(ctx context.Context, src domain.Source) ([]domain.ExternalJob, []byte, int, error) {
	body, status, err := c.client.Get(ctx, src.BaseURL, nil)
	if err != nil {
		return nil, body, status, err
	}
	matches := scriptJSONLD.FindAllSubmatch(body, -1)
	out := make([]domain.ExternalJob, 0)
	for _, m := range matches {
		var generic any
		if err := json.Unmarshal(m[1], &generic); err != nil {
			continue
		}
		jobs := flattenJobPostings(generic)
		out = append(out, jobs...)
	}
	if len(out) == 0 {
		return nil, body, status, fmt.Errorf("no schema.org job postings found")
	}
	return out, body, status, nil
}

func flattenJobPostings(v any) []domain.ExternalJob {
	out := make([]domain.ExternalJob, 0)
	switch t := v.(type) {
	case map[string]any:
		typ, _ := t["@type"].(string)
		if strings.EqualFold(typ, "JobPosting") {
			out = append(out, mapToExternal(t))
		}
		for _, child := range t {
			out = append(out, flattenJobPostings(child)...)
		}
	case []any:
		for _, item := range t {
			out = append(out, flattenJobPostings(item)...)
		}
	}
	return out
}

func mapToExternal(m map[string]any) domain.ExternalJob {
	title, _ := m["title"].(string)
	desc, _ := m["description"].(string)
	id, _ := m["identifier"].(string)
	if id == "" {
		if identMap, ok := m["identifier"].(map[string]any); ok {
			if val, ok := identMap["value"].(string); ok {
				id = val
			}
		}
	}
	company := ""
	if org, ok := m["hiringOrganization"].(map[string]any); ok {
		company, _ = org["name"].(string)
	}
	location := ""
	if jl, ok := m["jobLocation"].(map[string]any); ok {
		if addr, ok := jl["address"].(map[string]any); ok {
			location, _ = addr["addressLocality"].(string)
		}
	}
	apply, _ := m["url"].(string)
	return domain.ExternalJob{ExternalID: id, SourceURL: apply, ApplyURL: apply, Title: title, Company: company, LocationText: location, Description: desc}
}
