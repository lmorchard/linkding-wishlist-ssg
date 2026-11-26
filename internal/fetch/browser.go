package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lmorchard/linkding-wishlist-ssg/internal/extract"
)

// BrowserClient connects to a running Chrome instance over the Chrome DevTools
// Protocol (CDP) to fetch pages and evaluate DOM state.
type BrowserClient struct {
	BaseURL string // e.g. "http://myriad-docker:9222"
}

// NewBrowserClient creates a new BrowserClient pointing at the CDP HTTP endpoint.
func NewBrowserClient(baseURL string) *BrowserClient {
	return &BrowserClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
	}
}

type cdpConn struct {
	conn   *websocket.Conn
	nextID int64
}

func (c *cdpConn) call(ctx context.Context, method string, params map[string]interface{}) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	req := map[string]interface{}{
		"id":     id,
		"method": method,
		"params": params,
	}

	if err := c.conn.WriteJSON(req); err != nil {
		return nil, err
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		var resp struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := c.conn.ReadJSON(&resp); err != nil {
			return nil, err
		}
		if resp.ID == id {
			if resp.Error != nil {
				return nil, fmt.Errorf("cdp error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			return resp.Result, nil
		}
	}
}

// FetchAmazon opens a clean tab in the remote browser, navigates to targetURL,
// polls the DOM for price and availability signals, closes the tab, and returns
// the parsed extract.Result.
func (b *BrowserClient) FetchAmazon(ctx context.Context, targetURL string) (extract.Result, error) {
	// 1. Get browser websocket URL
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.BaseURL+"/json/version", nil)
	if err != nil {
		return extract.Result{}, fmt.Errorf("creating version request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return extract.Result{}, fmt.Errorf("getting browser version: %w", err)
	}
	defer resp.Body.Close()

	var ver struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ver); err != nil {
		return extract.Result{}, fmt.Errorf("decoding browser version: %w", err)
	}
	if ver.WebSocketDebuggerURL == "" {
		return extract.Result{}, fmt.Errorf("empty webSocketDebuggerUrl from browser")
	}

	// 2. Dial browser session
	bDialer := *websocket.DefaultDialer
	bWS, _, err := bDialer.DialContext(ctx, ver.WebSocketDebuggerURL, nil)
	if err != nil {
		return extract.Result{}, fmt.Errorf("dialing browser websocket: %w", err)
	}
	defer bWS.Close()
	bConn := &cdpConn{conn: bWS}

	// 3. Create target
	res, err := bConn.call(ctx, "Target.createTarget", map[string]interface{}{
		"url": "about:blank",
	})
	if err != nil {
		return extract.Result{}, fmt.Errorf("creating target tab: %w", err)
	}
	var created struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(res, &created); err != nil {
		return extract.Result{}, fmt.Errorf("unmarshaling targetId: %w", err)
	}
	targetID := created.TargetID

	// Ensure tab is always closed
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = bConn.call(closeCtx, "Target.closeTarget", map[string]interface{}{
			"targetId": targetID,
		})
	}()

	// 4. Dial target page websocket
	pageWSURL := b.pageWSURL(targetID)
	pWS, _, err := websocket.DefaultDialer.DialContext(ctx, pageWSURL, nil)
	if err != nil {
		return extract.Result{}, fmt.Errorf("dialing target page websocket: %w", err)
	}
	defer pWS.Close()
	pConn := &cdpConn{conn: pWS}

	// 5. Navigate to Amazon URL
	if _, err := pConn.call(ctx, "Page.enable", nil); err != nil {
		return extract.Result{}, fmt.Errorf("enabling Page domain: %w", err)
	}
	if _, err := pConn.call(ctx, "Page.navigate", map[string]interface{}{
		"url": targetURL,
	}); err != nil {
		return extract.Result{}, fmt.Errorf("navigating to %s: %w", targetURL, err)
	}

	// 6. Poll for DOM elements (price, stock, dog page, or captcha)
	evalScript := `(() => {
		const offscreen = document.querySelector(".a-price .a-offscreen")?.innerText?.trim();
		const whole = document.querySelector(".a-price .a-price-whole")?.innerText?.replace(/[^0-9]/g, "");
		const fraction = document.querySelector(".a-price .a-price-fraction")?.innerText?.replace(/[^0-9]/g, "");
		const symbol = document.querySelector(".a-price .a-price-symbol")?.innerText?.trim();
		const availText = document.querySelector("#availability")?.innerText?.trim();
		const title = document.querySelector("#productTitle")?.innerText?.trim();

		// Image extraction: main landing image or book front cover
		const imgEl = document.querySelector("#landingImage, #imgBlkFront, #main-image, #ebooks-img-canvas img");
		const imageUrl = imgEl?.getAttribute("src") || imgEl?.getAttribute("data-old-hires") || "";

		// Short bullet description or feature text
		const bullets = Array.from(document.querySelectorAll("#feature-bullets li span.a-list-item"))
			.map(el => el.innerText?.trim())
			.filter(t => t && !t.startsWith("Make sure this fits"))
			.slice(0, 3)
			.join(" • ");
		const description = bullets || document.querySelector("#productDescription")?.innerText?.trim() || "";

		// Target raw payload: capture key product center markup (or body snippet) for offline replay
		const rawContainer = document.querySelector("#centerCol, #dp")?.outerHTML || "";

		const isDogPage = document.title.includes("Page Not Found") || !!document.querySelector("img[alt*='dogs of Amazon']");
		const isCaptcha = document.title.includes("Robot Check") || !!document.querySelector("form[action*='validateCaptcha']");
		return {
			title,
			offscreen,
			whole,
			fraction,
			symbol,
			availText,
			imageUrl,
			description,
			rawHtml: rawContainer,
			isDogPage,
			isCaptcha
		};
	})()`

	var raw extract.AmazonRaw
	pollTicker := time.NewTicker(400 * time.Millisecond)
	defer pollTicker.Stop()

	// Max 15 seconds to settle on a loaded Amazon page
	pollDeadline := time.Now().Add(15 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return extract.Result{}, ctx.Err()
		case <-pollTicker.C:
		}

		evalRes, err := pConn.call(ctx, "Runtime.evaluate", map[string]interface{}{
			"expression":    evalScript,
			"returnByValue": true,
		})
		if err == nil {
			var evalOut struct {
				Result struct {
					Value extract.AmazonRaw `json:"value"`
				} `json:"result"`
			}
			if json.Unmarshal(evalRes, &evalOut) == nil {
				raw = evalOut.Result.Value
				if raw.Offscreen != "" || raw.Whole != "" || raw.AvailText != "" || raw.IsDogPage || raw.IsCaptcha {
					break
				}
			}
		}

		if time.Now().After(pollDeadline) {
			break
		}
	}

	return extract.Amazon(raw)
}

func (b *BrowserClient) pageWSURL(targetID string) string {
	u, err := url.Parse(b.BaseURL)
	if err != nil {
		return fmt.Sprintf("ws://%s/devtools/page/%s", b.BaseURL, targetID)
	}
	wsScheme := "ws"
	if u.Scheme == "https" {
		wsScheme = "wss"
	}
	return fmt.Sprintf("%s://%s/devtools/page/%s", wsScheme, u.Host, targetID)
}
