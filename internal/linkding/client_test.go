package linkding

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListAllFiltersServerSideAndPaginates(t *testing.T) {
	var requests []*http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Clone(context.Background()))

		offset := r.URL.Query().Get("offset")
		w.Header().Set("Content-Type", "application/json")
		switch offset {
		case "":
			fmt.Fprint(w, `{
				"count": 2,
				"next": "http://example.invalid/api/bookmarks/?limit=100&offset=1&q=%23wishlist",
				"previous": null,
				"results": [{"id": 1, "url": "https://example.com/one", "tag_names": ["wishlist"]}]
			}`)
		case "1":
			fmt.Fprint(w, `{
				"count": 2,
				"next": null,
				"previous": null,
				"results": [{"id": 2, "url": "https://example.com/two", "tag_names": ["wishlist"]}]
			}`)
		default:
			t.Errorf("unexpected offset %q", offset)
			http.Error(w, "unexpected offset", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientOptions{BaseURL: server.URL, Token: "secret-token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}

	var progress []string
	bookmarks, err := client.ListAll(context.Background(), "#wishlist", false, 100, func(fetched, total int) {
		progress = append(progress, fmt.Sprintf("%d/%d", fetched, total))
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(bookmarks) != 2 || bookmarks[0].ID != 1 || bookmarks[1].ID != 2 {
		t.Fatalf("bookmarks = %#v", bookmarks)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	for _, request := range requests {
		if got := request.URL.Query().Get("q"); got != "#wishlist" {
			t.Fatalf("q = %q, want #wishlist", got)
		}
		if got := request.Header.Get("Authorization"); got != "Token secret-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := request.Header.Get("User-Agent"); !strings.Contains(got, "linkding-wishlist-ssg") {
			t.Fatalf("User-Agent = %q", got)
		}
		if request.URL.Path != "/api/bookmarks/" {
			t.Fatalf("path = %q, want /api/bookmarks/", request.URL.Path)
		}
	}
	if len(progress) != 2 || progress[0] != "1/2" || progress[1] != "2/2" {
		t.Fatalf("progress = %v", progress)
	}
}

func TestListAllUsesArchivedEndpoint(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"count": 0, "next": null, "previous": null, "results": []}`)
	}))
	defer server.Close()

	client, err := NewClient(ClientOptions{BaseURL: server.URL, Token: "t", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListAll(context.Background(), "#wishlist", true, 100, nil); err != nil {
		t.Fatal(err)
	}
	if path != "/api/bookmarks/archived/" {
		t.Fatalf("path = %q, want /api/bookmarks/archived/", path)
	}
}

func TestListAllReportsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"Invalid token"}`, http.StatusUnauthorized)
	}))
	defer server.Close()

	client, err := NewClient(ClientOptions{BaseURL: server.URL, Token: "bad", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListAll(context.Background(), "#wishlist", false, 100, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("error = %v, want HTTP 401", err)
	}
}

func TestListAllPreservesBaseURLPath(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"count": 0, "next": null, "previous": null, "results": []}`)
	}))
	defer server.Close()

	client, err := NewClient(ClientOptions{BaseURL: server.URL + "/linkding", Token: "t", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListAll(context.Background(), "#wishlist", false, 100, nil); err != nil {
		t.Fatal(err)
	}
	if path != "/linkding/api/bookmarks/" {
		t.Fatalf("path = %q, want /linkding/api/bookmarks/", path)
	}
}

func TestNewClientRejectsInvalidBaseURL(t *testing.T) {
	if _, err := NewClient(ClientOptions{BaseURL: "links.example.com"}); err == nil {
		t.Fatal("expected error for base URL without scheme")
	}
}
