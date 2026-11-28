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
				if raw.IsDogPage || raw.IsCaptcha {
					break
				}
				hasPriceOrStock := raw.Offscreen != "" || raw.Whole != "" || raw.AvailText != ""
				// Break once we have price/stock AND image, or if price/stock settled and we've polled for at least 3 seconds
				if hasPriceOrStock && raw.ImageURL != "" {
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

// WishlistCard represents an item parsed from an Amazon Wishlist page.
type WishlistCard struct {
	Shape          string `json:"shape"` // "amazon" or "external"
	ASIN           string `json:"asin,omitempty"`
	URL            string `json:"url"`
	URLSource      string `json:"url_source"` // "original", "reconstructed", "unavailable"
	Title          string `json:"title"`
	Price          string `json:"price,omitempty"`
	Added          string `json:"added,omitempty"`
	WishlistItemID string `json:"wishlist_item_id"`
	ExternalID     string `json:"external_id,omitempty"`
}

// FetchAmazonWishlistPage loads an Amazon wishlist page, optionally scrolls once or twice to populate lazy cards,
// and extracts all visible cards using the battle-tested selector script from extract.py.
func (b *BrowserClient) FetchAmazonWishlistPage(ctx context.Context, listURL string, scrollRounds int) ([]WishlistCard, error) {
	// 1. Get browser websocket URL
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.BaseURL+"/json/version", nil)
	if err != nil {
		return nil, fmt.Errorf("creating version request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("getting browser version: %w", err)
	}
	defer resp.Body.Close()

	var ver struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ver); err != nil {
		return nil, fmt.Errorf("decoding browser version: %w", err)
	}
	if ver.WebSocketDebuggerURL == "" {
		return nil, fmt.Errorf("empty webSocketDebuggerUrl from browser")
	}

	// 2. Dial browser session
	bDialer := *websocket.DefaultDialer
	bWS, _, err := bDialer.DialContext(ctx, ver.WebSocketDebuggerURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dialing browser websocket: %w", err)
	}
	defer bWS.Close()
	bConn := &cdpConn{conn: bWS}

	// 3. Create target
	res, err := bConn.call(ctx, "Target.createTarget", map[string]interface{}{
		"url": "about:blank",
	})
	if err != nil {
		return nil, fmt.Errorf("creating target tab: %w", err)
	}
	var created struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(res, &created); err != nil {
		return nil, fmt.Errorf("unmarshaling targetId: %w", err)
	}
	targetID := created.TargetID

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
		return nil, fmt.Errorf("dialing target page websocket: %w", err)
	}
	defer pWS.Close()
	pConn := &cdpConn{conn: pWS}

	// 5. Navigate to Wishlist URL
	if _, err := pConn.call(ctx, "Page.enable", nil); err != nil {
		return nil, fmt.Errorf("enabling Page domain: %w", err)
	}
	if _, err := pConn.call(ctx, "Page.navigate", map[string]interface{}{
		"url": listURL,
	}); err != nil {
		return nil, fmt.Errorf("navigating to %s: %w", listURL, err)
	}

	// 6. Wait for wishlist cards container to appear
	waitScript := `(() => {
		const cards = document.querySelectorAll('li[data-itemid]');
		const isDog = document.title.includes("Page Not Found") || !!document.querySelector("img[alt*='dogs of Amazon']");
		const isCaptcha = document.title.includes("Robot Check") || !!document.querySelector("form[action*='validateCaptcha']");
		return { count: cards.length, isDog, isCaptcha };
	})()`

	pollTicker := time.NewTicker(500 * time.Millisecond)
	defer pollTicker.Stop()
	deadline := time.Now().Add(25 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pollTicker.C:
		}

		evalRes, err := pConn.call(ctx, "Runtime.evaluate", map[string]interface{}{
			"expression":    waitScript,
			"returnByValue": true,
		})
		if err == nil {
			var check struct {
				Result struct {
					Value struct {
						Count     int  `json:"count"`
						IsDog     bool `json:"isDog"`
						IsCaptcha bool `json:"isCaptcha"`
					} `json:"value"`
				} `json:"result"`
			}
			if json.Unmarshal(evalRes, &check) == nil {
				val := check.Result.Value
				if val.IsDog {
					return nil, fmt.Errorf("amazon returned dog page (list not found)")
				}
				if val.IsCaptcha {
					return nil, fmt.Errorf("amazon returned robot captcha check")
				}
				if val.Count > 0 {
					break
				}
			}
		}

		if time.Now().After(deadline) {
			break
		}
	}

	// Optional gentle scroll rounds if requested
	for r := 0; r < scrollRounds; r++ {
		_, _ = pConn.call(ctx, "Runtime.evaluate", map[string]interface{}{
			"expression": "window.scrollTo(0, document.body.scrollHeight)",
		})
		time.Sleep(1500 * time.Millisecond)
	}

	// 7. Extract cards using exact logic
	extractScript := `(() => {
		const cards = Array.from(document.querySelectorAll('li[data-itemid]'));
		const AMZ = /\/dp\/([A-Z0-9]{10})/;
		const out = [];

		const reconstruct = (title, text) => {
			const fromMatch = text.match(/\s+From\s+([\w.-]+\.[a-z]{2,})/i);
			if (!fromMatch) return null;
			const host = fromMatch[1].toLowerCase();
			const slug = (title || '')
				.toLowerCase()
				.replace(/[^a-z0-9]+/g, '-')
				.replace(/^-+|-+$/g, '')
				.slice(0, 60);
			if (!slug) return null;
			return 'https://' + host + '/#wishlist-' + slug;
		};

		for (const li of cards) {
			const dateEl = li.querySelector('[id^="itemAddedDate_"]');
			const added = dateEl ? dateEl.innerText.trim().replace(/^Item added\s+/, '') : "";

			const priceAttr = li.getAttribute('data-price');
			const priceEl = li.querySelector('.a-price .a-offscreen');

			const titleLink = li.querySelector('h2 a') || li.querySelector('a[id^="itemName_"]');
			const href = titleLink ? titleLink.getAttribute('href') : null;
			const asinMatch = href ? href.match(AMZ) : null;

			if (asinMatch) {
				let amzPrice = li.getAttribute('data-price');
				if (amzPrice && !/^[0-9]/.test(amzPrice)) amzPrice = "";
				if (!amzPrice && priceEl) amzPrice = priceEl.innerText.trim();
				out.push({
					shape: 'amazon',
					asin: asinMatch[1],
					url: 'https://www.amazon.com/dp/' + asinMatch[1],
					url_source: 'original',
					title: titleLink ? titleLink.innerText.trim() : "",
					price: amzPrice || "",
					added: added,
					wishlist_item_id: li.getAttribute('data-itemid') || ""
				});
				continue;
			}

			// External card
			let url = null;
			for (const a of li.querySelectorAll('a[href]')) {
				const h = a.getAttribute('href');
				if (!h) continue;
				if (/^javascript:/i.test(h)) continue;
				if (h === '#' || h === '') continue;
				if (/^\/hz\/wishlist\//.test(h)) continue;
				if (/\/gp\/registry/.test(h)) continue;
				if (h.startsWith('/') && !/^\/\//.test(h)) continue;
				url = h.startsWith('//') ? 'https:' + h : h;
				break;
			}

			const text = (li.innerText || '').replace(/\s+/g, ' ').trim();
			const ACTION_CHROME = /\s*(Add a note|Move|Delete|Shop products|Add to (?:List|Cart)|Edit comment)\s*/gi;
			let titleText = text.replace(ACTION_CHROME, ' ').replace(/\s+/g, ' ').trim();

			if (!url) {
				const m = titleText.match(/https?:\/\/[^\s"]+/);
				if (m) url = m[0];
			}

			let title = "";
			if (titleText) {
				const fromIdx = titleText.search(/\s+From\s+[\w.-]+\.[a-z]{2,}/i);
				if (fromIdx > 0) {
					title = titleText.slice(0, fromIdx).trim();
				} else if (url && titleText.startsWith(url)) {
					title = "";
				} else {
					title = titleText;
				}
				if (title.length > 400) title = title.slice(0, 400);
				const ta = document.createElement('textarea');
				ta.innerHTML = title;
				title = ta.value.replace(/\s+/g, ' ').trim();
			}

			let price = priceAttr;
			if (price && !/^[0-9]/.test(price)) price = "";
			if (!price && priceEl) {
				const p = priceEl.innerText.trim();
				if (/^[0-9$£€]/.test(p)) price = p;
			}

			const externalIdEl = li.querySelector('a[href*="itemExternalId"]');
			const externalId = externalIdEl
				? (externalIdEl.getAttribute('href').match(/itemExternalId=([^&]+)/) || [])[1] || ""
				: "";

			let urlSource = 'original';
			if (!url) {
				const rebuilt = reconstruct(title, titleText);
				if (rebuilt) {
					url = rebuilt;
					urlSource = 'reconstructed';
				} else {
					urlSource = 'unavailable';
				}
			}

			out.push({
				shape: 'external',
				asin: '',
				url: url || "",
				url_source: urlSource,
				title: title,
				price: price || "",
				added: added,
				wishlist_item_id: li.getAttribute('data-itemid') || "",
				external_id: externalId ? decodeURIComponent(externalId) : ""
			});
		}
		return out;
	})()`

	extractRes, err := pConn.call(ctx, "Runtime.evaluate", map[string]interface{}{
		"expression":    extractScript,
		"returnByValue": true,
	})
	if err != nil {
		return nil, fmt.Errorf("evaluating extract script: %w", err)
	}

	var evalOut struct {
		Result struct {
			Value []WishlistCard `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(extractRes, &evalOut); err != nil {
		return nil, fmt.Errorf("unmarshaling wishlist cards: %w", err)
	}

	return evalOut.Result.Value, nil
}
