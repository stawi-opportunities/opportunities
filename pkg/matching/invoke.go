package matching

import (
	"context"
	"fmt"
	"time"
)

const (
	InvokeUserRefresh = "user_refresh"
	InvokeDigest      = "digest"
	InvokeOnboardSeed = "onboard_seed"
)

// InvokeInput is one explicit match generation request.
type InvokeInput struct {
	CandidateID    string
	Embedding      []float32
	Skills         []string
	Countries      []string
	Kinds          []string
	SalaryFloorUSD *int
	Since          time.Time
	MinScore       float64
	QueryText      string
	Reason         string // user_refresh | digest | onboard_seed
	// InvokeLimit when > 0 and Reason is user-facing, enforces daily invoke budget.
	InvokeLimit int
}

// InvokeDeps extends GapFill with optional invoke counting.
type InvokeDeps struct {
	GapFill GapFillDeps
	// Invokes optional; when nil, rate limit is skipped.
	Invokes InvokeCounter
	Now     func() time.Time
}

func reasonConsumesBudget(reason string) bool {
	switch reason {
	case InvokeUserRefresh, InvokeOnboardSeed:
		return true
	default:
		return false
	}
}

// MatchInvoke runs reverse-KNN matching without match-row caps.
// user_refresh and onboard_seed respect InvokeLimit when set; digest does not.
func MatchInvoke(ctx context.Context, in InvokeInput, deps InvokeDeps) (GapFillResult, error) {
	now := time.Now
	if deps.Now != nil {
		now = deps.Now
	}
	if in.MinScore <= 0 {
		in.MinScore = 0.70
	}
	if in.Since.IsZero() {
		in.Since = now().UTC().Add(-30 * 24 * time.Hour)
	}
	reason := in.Reason
	if reason == "" {
		reason = InvokeUserRefresh
	}

	if reasonConsumesBudget(reason) && in.InvokeLimit > 0 && deps.Invokes != nil {
		used, err := deps.Invokes.CountUserInvokesToday(ctx, in.CandidateID, now())
		if err != nil {
			return GapFillResult{}, fmt.Errorf("matching: invoke limit: %w", err)
		}
		if used >= in.InvokeLimit {
			return GapFillResult{
				Reason:     GapReasonRateLimited,
				WeeklyCap:  0,
				WeeklyUsed: 0,
			}, nil
		}
	}

	// Force unlimited match rows — quality floor only.
	return GapFill(ctx, GapFillInput{
		CandidateID:    in.CandidateID,
		Embedding:      in.Embedding,
		Skills:         in.Skills,
		Countries:      in.Countries,
		Kinds:          in.Kinds,
		SalaryFloorUSD: in.SalaryFloorUSD,
		Since:          in.Since,
		MinScore:       in.MinScore,
		DailyCap:       0,
		WeeklyCap:      0,
		QueryText:      in.QueryText,
		TriggeredBy:    reason,
	}, deps.GapFill)
}
