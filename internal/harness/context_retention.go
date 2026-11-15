package harness

import (
	"context"
)

// Retention counts the complete projected suffix, not just the small human
// messages that precede large answers. Only complete protocol groups survive.
func (service *Service) retainRecentUserText(ctx context.Context, run Run, base, through int64) (int64, error) {
	limits, binding, err := service.requestLimits()
	if err != nil {
		return through, err
	}
	window := contextWindow(service.Config, limits, binding, limits.MaxOutputTokens)
	byteCapacity := window.ByteLimit
	if window.Tokens == 0 && window.ByteLimitMode != "auto" {
		byteCapacity -= window.OutputReserve * 4
	}
	byteBudget, tokenBudget := max(0, byteCapacity)/5, window.InputBudget/5
	groups, err := service.contextSourceGroups(ctx, run, base, through)
	if err != nil {
		return through, err
	}
	if len(groups) == 0 || groups[len(groups)-1].records[len(groups[len(groups)-1].records)-1].ID != through {
		return through, nil // bounded source read did not reach the recent suffix
	}
	for i := len(groups) - 1; i >= 0; i-- {
		group := groups[i]
		body := string(mustJSON(group.messages))
		tokens := estimateContextTokens(body)
		if len(body) > byteBudget || window.Tokens > 0 && tokens > tokenBudget {
			break
		}
		byteBudget -= len(body)
		tokenBudget -= tokens
		through = group.records[0].ID - 1
	}
	return through, nil
}
