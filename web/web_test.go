package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielriddell21/merkelbrot/examples/ledger"
	"github.com/danielriddell21/merkelbrot/graph"
	"github.com/danielriddell21/merkelbrot/layout"
	"github.com/danielriddell21/merkelbrot/scene"
	"github.com/danielriddell21/merkelbrot/web"
)

func demo(t *testing.T) *scene.Scene {
	t.Helper()
	g, err := graph.New(ledger.New(ledger.Config{Seed: 1, Transactions: 6}))
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	return scene.Builder[string]{Title: "test ledger"}.Scene(layout.Pack(g, layout.Options{}))
}

// TestRenderIsSelfContained is the property that makes the export useful: the page
// must carry its own styles, script and data, with nothing left to fetch.
func TestRenderIsSelfContained(t *testing.T) {
	var buf strings.Builder
	if err := web.Render(context.Background(), &buf, demo(t)); err != nil {
		t.Fatalf("Render: %v", err)
	}
	html := buf.String()

	for _, want := range []string{
		"<!doctype html>",
		"<title>test ledger</title>",
		`<canvas id="view">`,
		`id="scene"`,
		"hud-top",               // the stylesheet was inlined
		"requestAnimationFrame", // the viewer script was inlined
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered page is missing %q", want)
		}
	}
	for _, unwanted := range []string{"src=", "href=", "@templ.Raw"} {
		if strings.Contains(html, unwanted) {
			t.Errorf("rendered page contains %q, want nothing external or unevaluated", unwanted)
		}
	}
}

// TestRenderEscapesSceneJSON checks a label cannot break out of the script element
// it is embedded in.
func TestRenderEscapesSceneJSON(t *testing.T) {
	g, err := graph.New(graph.NewMemorySource([]string{"x"},
		graph.Node[string]{ID: "x", Label: `</script><script>alert(1)</script>`},
	))
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	s := scene.Builder[string]{}.Scene(layout.Pack(g, layout.Options{}))

	var buf strings.Builder
	if err := web.Render(context.Background(), &buf, s); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(buf.String(), "<script>alert(1)</script>") {
		t.Error("a node label escaped its script element")
	}
	if got, want := strings.Count(buf.String(), "<script"), 2; got != want {
		t.Errorf("%d script elements, want %d", got, want)
	}
}

func TestRenderFallsBackToADefaultTitle(t *testing.T) {
	g, _ := graph.New(graph.NewMemorySource([]string{"x"}, graph.Node[string]{ID: "x"}))
	s := scene.Builder[string]{}.Scene(layout.Pack(g, layout.Options{}))

	var buf strings.Builder
	if err := web.Render(context.Background(), &buf, s); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), "<title>merkelbrot</title>") {
		t.Error("an untitled scene did not fall back to the default title")
	}
}

func TestHandlerRoutes(t *testing.T) {
	srv := httptest.NewServer(web.Handler(demo(t)))
	defer srv.Close()

	tests := []struct {
		path        string
		status      int
		contentType string
	}{
		{"/", http.StatusOK, "text/html; charset=utf-8"},
		{"/scene.json", http.StatusOK, "application/json; charset=utf-8"},
		{"/nope", http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resp, err := srv.Client().Get(srv.URL + tt.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tt.path, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.status {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.status)
			}
			if tt.contentType != "" && resp.Header.Get("Content-Type") != tt.contentType {
				t.Errorf("Content-Type = %q, want %q", resp.Header.Get("Content-Type"), tt.contentType)
			}
		})
	}
}

func TestHandlerServesTheSceneAsJSON(t *testing.T) {
	want := demo(t)
	srv := httptest.NewServer(web.Handler(want))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/scene.json")
	if err != nil {
		t.Fatalf("GET /scene.json: %v", err)
	}
	defer resp.Body.Close()

	var got scene.Scene
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding scene: %v", err)
	}
	if got.Title != want.Title {
		t.Errorf("Title = %q, want %q", got.Title, want.Title)
	}
	if len(got.Nodes) != len(want.Nodes) {
		t.Errorf("%d nodes, want %d", len(got.Nodes), len(want.Nodes))
	}
}
