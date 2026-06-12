package recipe

import (
	"encoding/json"
	"fmt"
)

// stateDTO is the stable, exported wire shape of a PageState so it can be
// persisted (in a crawl_runs cursor) and restored across process restarts. The
// PageState fields stay unexported — this DTO is the only serialization contract.
type stateDTO struct {
	URL    string `json:"url,omitempty"`
	Page   int    `json:"page,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// MarshalState serializes a PageState to opaque JSON for durable resume. The
// crawl handler stores the result in crawl_runs.cursor after each slice.
func MarshalState(st PageState) (json.RawMessage, error) {
	b, err := json.Marshal(stateDTO{URL: st.url, Page: st.page, Cursor: st.cursor})
	if err != nil {
		return nil, fmt.Errorf("marshal page state: %w", err)
	}
	return b, nil
}

// UnmarshalState restores a PageState previously produced by MarshalState. An
// empty input yields the zero PageState (start from the beginning).
func UnmarshalState(data json.RawMessage) (PageState, error) {
	if len(data) == 0 {
		return PageState{}, nil
	}
	var d stateDTO
	if err := json.Unmarshal(data, &d); err != nil {
		return PageState{}, fmt.Errorf("unmarshal page state: %w", err)
	}
	return PageState{url: d.URL, page: d.Page, cursor: d.Cursor}, nil
}

// StateProgress exposes the observable position of a PageState — the page index
// (tenant index for multi-tenant api recipes) and the listing URL, if any — for
// checkpoint metadata. It deliberately does not leak the cursor token.
func StateProgress(st PageState) (page int, url string) {
	return st.page, st.url
}
