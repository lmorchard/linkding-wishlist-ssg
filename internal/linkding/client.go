// Package linkding is a client for the Linkding REST API.
package linkding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Bookmark is the subset of the Linkding bookmark model this tool uses.
type Bookmark struct {
	ID           int64     `json:"id"`
	URL          string    `json:"url"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	Notes        string    `json:"notes"`
	IsArchived   bool      `json:"is_archived"`
	TagNames     []string  `json:"tag_names"`
	DateAdded    time.Time `json:"date_added"`
	DateCreated  time.Time `json:"date_created"`
	DateModified time.Time `json:"date_modified"`
}

// CreateBookmarkRequest defines the payload for creating a new bookmark via POST /api/bookmarks/.
type CreateBookmarkRequest struct {
	URL         string   `json:"url"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Notes       string   `json:"notes,omitempty"`
	TagNames    []string `json:"tag_names,omitempty"`
	IsArchived  bool     `json:"is_archived,omitempty"`
	Shared      bool     `json:"shared,omitempty"`
}

// CheckResponse represents the response from GET /api/bookmarks/check/?url=...
type CheckResponse struct {
	Bookmark *Bookmark `json:"bookmark"`
}

// UnmarshalJSON keeps date_added and date_created consistent, because
// Linkding versions differ in which one they populate.
func (b *Bookmark) UnmarshalJSON(data []byte) error {
	type alias Bookmark
	aux := &struct {
		*alias
	}{
		alias: (*alias)(b),
	}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	if b.DateCreated.IsZero() && !b.DateAdded.IsZero() {
		b.DateCreated = b.DateAdded
	} else if b.DateAdded.IsZero() && !b.DateCreated.IsZero() {
		b.DateAdded = b.DateCreated
	}
	return nil
}

// BookmarkListResponse is the paginated envelope from GET /api/bookmarks/.
type BookmarkListResponse struct {
	Count    int        `json:"count"`
	Next     *string    `json:"next"`
	Previous *string    `json:"previous"`
	Results  []Bookmark `json:"results"`
}

// Client talks to the Linkding REST API.
type Client struct {
	baseURL       *url.URL
	token         string
	httpClient    *http.Client
	retryBackoffs []time.Duration
}

// ClientOptions configures a Client.
type ClientOptions struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
	// RetryBackoffs overrides the delays between retries. A nil slice uses
	// the production defaults; an empty slice disables retries.
	RetryBackoffs []time.Duration
}

var defaultRetryBackoffs = []time.Duration{500 * time.Millisecond, 1500 * time.Millisecond, 3000 * time.Millisecond}

// NewClient validates the base URL and builds a Client.
func NewClient(opts ClientOptions) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(opts.BaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("invalid LINKDING_URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("LINKDING_URL must include scheme and host (for example https://links.example.com)")
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	return &Client{
		baseURL:       parsed,
		token:         opts.Token,
		httpClient:    httpClient,
		retryBackoffs: opts.RetryBackoffs,
	}, nil
}

// ListAll fetches every page of bookmarks matching query. When archived is
// true it reads the archived endpoint. The tag filter must be part of query;
// this client never fetches and filters client-side.
func (c *Client) ListAll(ctx context.Context, query string, archived bool, pageSize int, progress func(fetched, total int)) ([]Bookmark, error) {
	if pageSize <= 0 {
		pageSize = 100
	}

	var all []Bookmark
	offset := 0
	for {
		resp, err := c.listPage(ctx, query, archived, pageSize, offset)
		if err != nil {
			return nil, fmt.Errorf("fetching bookmarks at offset %d: %w", offset, err)
		}

		all = append(all, resp.Results...)
		if progress != nil {
			progress(len(all), resp.Count)
		}

		if resp.Next == nil || *resp.Next == "" || len(resp.Results) == 0 {
			break
		}
		offset += len(resp.Results)
	}

	return all, nil
}

func (c *Client) listPage(ctx context.Context, query string, archived bool, limit, offset int) (*BookmarkListResponse, error) {
	reqURL := c.baseURL.JoinPath("api", "bookmarks")
	reqURL.Path += "/"
	reqURL.RawQuery = ""
	reqURL.Fragment = ""
	if archived {
		reqURL.Path += "archived/"
	}

	params := reqURL.Query()
	params.Set("limit", strconv.Itoa(limit))
	if offset > 0 {
		params.Set("offset", strconv.Itoa(offset))
	}
	if query != "" {
		params.Set("q", query)
	}
	reqURL.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "linkding-wishlist-ssg/0.1")

	var resp *http.Response
	var reqErr error
	backoffs := c.retryBackoffs
	if backoffs == nil {
		backoffs = defaultRetryBackoffs
	}

	for attempt := 0; attempt <= len(backoffs); attempt++ {
		resp, reqErr = c.httpClient.Do(req)
		if reqErr == nil && resp.StatusCode < 500 {
			break
		}
		if attempt < len(backoffs) {
			if reqErr == nil && resp != nil {
				resp.Body.Close()
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoffs[attempt]):
			}
		}
	}
	if reqErr != nil {
		return nil, fmt.Errorf("requesting %s: %w", reqURL.Redacted(), reqErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("Linkding API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var list BookmarkListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decoding Linkding response: %w", err)
	}
	return &list, nil
}

// CheckBookmark queries Linkding's /api/bookmarks/check/?url=... endpoint to see
// if a URL already exists as a bookmark. Returns the existing Bookmark or nil.
func (c *Client) CheckBookmark(ctx context.Context, targetURL string) (*Bookmark, error) {
	reqURL := c.baseURL.JoinPath("api", "bookmarks", "check")
	reqURL.Path += "/"
	params := reqURL.Query()
	params.Set("url", targetURL)
	reqURL.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating check request: %w", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "linkding-wishlist-ssg/0.1")

	resp, err := c.doWithRetry(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("checking bookmark %s: %w", targetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("Linkding check API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var check CheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&check); err != nil {
		return nil, fmt.Errorf("decoding Linkding check response: %w", err)
	}
	return check.Bookmark, nil
}

// CreateBookmark creates a new bookmark via POST /api/bookmarks/.
func (c *Client) CreateBookmark(ctx context.Context, req CreateBookmarkRequest) (*Bookmark, error) {
	reqURL := c.baseURL.JoinPath("api", "bookmarks")
	reqURL.Path += "/"

	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling create bookmark request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating POST request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Token "+c.token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", "linkding-wishlist-ssg/0.1")

	resp, err := c.doWithRetry(ctx, httpReq)
	if err != nil {
		return nil, fmt.Errorf("posting bookmark to Linkding: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("Linkding create API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var created Bookmark
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return nil, fmt.Errorf("decoding created bookmark response: %w", err)
	}
	return &created, nil
}

func (c *Client) doWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var reqErr error
	backoffs := c.retryBackoffs
	if backoffs == nil {
		backoffs = defaultRetryBackoffs
	}

	for attempt := 0; attempt <= len(backoffs); attempt++ {
		resp, reqErr = c.httpClient.Do(req)
		if reqErr == nil && resp.StatusCode < 500 {
			return resp, nil
		}
		if attempt < len(backoffs) {
			if reqErr == nil && resp != nil {
				resp.Body.Close()
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoffs[attempt]):
			}
		}
	}
	return nil, reqErr
}
