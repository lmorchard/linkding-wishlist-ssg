// Package fetch retrieves a page for extraction.
//
// Deliberately narrow: GET only, with an honest User-Agent and a hard timeout.
// Politeness teardown (rate limits, cooldowns) belongs to the store and the
// scrape command, not here — this package's only job is one request and one
// answer.
package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// UserAgent identifies us honestly. Sites that rate-limit or block automated
// clients are entitled to know who is calling, and a false browser string would
// only shift the problem rather than solve it.
const UserAgent = "linkding-wishlist-ssg/0.3 (+https://github.com/lmorchard/linkding-wishlist-ssg; personal wishlist price check)"

// MaxBody caps how much of a response we read. Product pages can be several
// megabytes and the JSON-LD block is near the top; there is no reason to hold
// the whole document in memory.
const MaxBody = 4 << 20

// Result is one fetch attempt.
type Result struct {
	Status      int
	Body        string
	Blocked     bool
	BlockReason string
	Err         error
}

// OK reports whether the response is worth handing to an extractor.
func (r Result) OK() bool { return r.Err == nil && !r.Blocked && r.Status == http.StatusOK }

// Client fetches pages over HTTP.
type Client struct {
	HTTP *http.Client
}

// New returns a Client with a sensible timeout. The timeout is generous because
// some small shops are slow, but bounded so one stall cannot eat a whole
// timebox.
func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Get retrieves rawURL.
//
// A non-200 status is not an error: it is an answer. Only transport failures
// and blocks are distinguished, because those demand different queue responses.
func (c *Client) Get(ctx context.Context, rawURL string) Result {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Result{Err: err}
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{Err: err}
	}
	defer resp.Body.Close()

	if blocked, reason := detectBlock(resp.StatusCode, resp.Header); blocked {
		return Result{Status: resp.StatusCode, Blocked: true, BlockReason: reason}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
	if err != nil {
		return Result{Status: resp.StatusCode, Err: fmt.Errorf("reading body: %w", err)}
	}
	return Result{Status: resp.StatusCode, Body: string(body)}
}

// detectBlock distinguishes "we were turned away" from "the server said no".
//
// This separation is the whole point: a captcha page returned as 200 is not a
// store without a price, and treating it as one would hide a wall behind a
// report full of working stores.
func detectBlock(status int, h http.Header) (bool, string) {
	switch {
	case status == http.StatusTooManyRequests:
		return true, "429 too many requests"
	case status == http.StatusForbidden:
		return true, "403 forbidden"
	case status == 503 && h.Get("Retry-After") != "":
		return true, "503 with Retry-After"
	}
	return false, ""
}
