package fetch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{}

func TestBrowserClientMock(t *testing.T) {
	// Mock HTTP/WS server emulating CDP
	var targetID = "target-123"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			w.Header().Set("Content-Type", "application/json")
			wsURL := "ws://" + r.Host + "/devtools/browser/test-browser"
			_ = json.NewEncoder(w).Encode(map[string]string{
				"Browser":              "Chrome/Mock",
				"webSocketDebuggerUrl": wsURL,
			})
			return
		}

		if strings.HasPrefix(r.URL.Path, "/devtools/browser/") {
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			for {
				var msg struct {
					ID     int64                  `json:"id"`
					Method string                 `json:"method"`
					Params map[string]interface{} `json:"params"`
				}
				if err := c.ReadJSON(&msg); err != nil {
					return
				}
				if msg.Method == "Target.createTarget" {
					_ = c.WriteJSON(map[string]interface{}{
						"id": msg.ID,
						"result": map[string]string{
							"targetId": targetID,
						},
					})
				} else if msg.Method == "Target.closeTarget" {
					_ = c.WriteJSON(map[string]interface{}{
						"id":     msg.ID,
						"result": map[string]bool{"success": true},
					})
				}
			}
		}

		if strings.HasPrefix(r.URL.Path, "/devtools/page/") {
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			for {
				var msg struct {
					ID     int64                  `json:"id"`
					Method string                 `json:"method"`
					Params map[string]interface{} `json:"params"`
				}
				if err := c.ReadJSON(&msg); err != nil {
					return
				}
				if msg.Method == "Page.enable" || msg.Method == "Page.navigate" {
					_ = c.WriteJSON(map[string]interface{}{
						"id":     msg.ID,
						"result": map[string]interface{}{},
					})
				} else if msg.Method == "Runtime.evaluate" {
					_ = c.WriteJSON(map[string]interface{}{
						"id": msg.ID,
						"result": map[string]interface{}{
							"result": map[string]interface{}{
								"value": map[string]interface{}{
									"offscreen": "$19.99",
									"availText": "In Stock",
									"title":     "Test Mock Product",
								},
							},
						},
					})
				}
			}
		}
	}))
	defer ts.Close()

	client := NewBrowserClient(ts.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := client.FetchAmazon(ctx, "https://www.amazon.com/dp/B000000000")
	if err != nil {
		t.Fatalf("FetchAmazon: %v", err)
	}

	if res.Status != "priced" {
		t.Errorf("status = %q, want 'priced'", res.Status)
	}
	if res.Price == nil || *res.Price != 19.99 {
		t.Errorf("price = %v, want 19.99", res.Price)
	}
	if res.Currency != "USD" {
		t.Errorf("currency = %q, want 'USD'", res.Currency)
	}
}
