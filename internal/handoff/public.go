package handoff

import "context"

func (c *Client) PublicSettings(ctx context.Context) (map[string]any, error) {
	var result map[string]any
	err := c.request(ctx, "GET", "/api/public-settings", nil, &result)
	return result, err
}
func (c *Client) PublicPassword(ctx context.Context, password []byte) error {
	return c.request(ctx, "POST", "/api/public-password", map[string]string{"password": string(password)}, nil)
}
func (c *Client) PublicRecovery(ctx context.Context, clear bool) (map[string]any, error) {
	var result map[string]any
	method := "GET"
	if clear {
		method = "POST"
	}
	err := c.request(ctx, method, "/api/public-recovery", map[string]any{}, &result)
	return result, err
}
