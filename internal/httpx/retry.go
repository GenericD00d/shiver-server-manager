// Package httpx retries HTTP requests that fail on the way, such as a TLS
// hiccup talking to GitHub.
package httpx

import (
	"context"
	"net/http"
	"time"
)

// Do sends the request made by newReq, retrying up to three times on a
// network error or a 5xx answer.
func Do(ctx context.Context, c *http.Client, newReq func() (*http.Request, error)) (*http.Response, error) {
	var resp *http.Response
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		var req *http.Request
		if req, err = newReq(); err != nil {
			return nil, err
		}
		resp, err = c.Do(req)
		if err == nil && resp.StatusCode < 500 {
			return resp, nil
		}
		if err == nil && attempt < 2 {
			resp.Body.Close()
		}
	}
	return resp, err
}
