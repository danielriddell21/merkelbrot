/*
Package web renders a [github.com/danielriddell21/merkelbrot/scene.Scene] as a
zoomable page.

This is the only part of merkelbrot that knows about HTML, templ or HTTP, and it
is a separate Go module for that reason: importing the core packages never pulls
a web dependency into a consumer's build.

# What it produces

[Render] writes one self-contained page. The stylesheet, the viewer script and
the scene itself are all inlined, so the output needs no server, no asset paths
and no network access — it can be written to a file and opened directly, mailed
to someone, or committed as a build artefact.

[Handler] serves that same page over HTTP, along with the raw scene at
/scene.json for anything that would rather read the data than the picture.

# How zoom works

The page is a single canvas. The scene arrives in layout coordinates and the
client holds one transform — a scale and an offset — that maps those coordinates
to pixels. Zooming changes the scale and nothing else, so movement between the
whole graph and the inside of one node is continuous rather than a sequence of
discrete views.

Detail is a function of a node's radius on screen. Below roughly a third of a
pixel a node is not drawn at all and its contents are skipped with it, which is
what keeps a large graph responsive; past a few pixels its children are drawn;
past a few dozen its label appears; and only once a node is large does its
payload resolve into readable fields. Nothing is precomputed per zoom level, so
the transition between those thresholds is smooth.

Radius decides weight as well as detail. Everything the view is inside is still
being drawn, and with containment that is a dozen translucent discs stacked one
on the other, which washes out everything within them the deeper the zoom goes.
A disc wider than the window is therefore ground rather than content: its fill
fades in proportion to how far it overflows, leaving its outline and its label
to carry the context. That holds the background steady at any depth, and it
removes the full-window fills that were the most expensive part of a deep frame.
*/
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/danielriddell21/merkelbrot/scene"
)

//go:embed assets/merkelbrot.css assets/merkelbrot.js
var assets embed.FS

func mustAsset(name string) string {
	data, err := assets.ReadFile("assets/" + name)
	if err != nil {
		panic("web: missing embedded asset " + name + ": " + err.Error())
	}
	return string(data)
}

// Render writes a self-contained HTML page for the scene.
//
// Everything the page needs is inlined, so the result can be saved and opened
// without a server.
func Render(ctx context.Context, w io.Writer, s *scene.Scene) error {
	// json.Marshal escapes <, > and &, so the payload is safe to inline in a
	// script element.
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("web: encoding scene: %w", err)
	}
	title := s.Title
	if title == "" {
		title = "merkelbrot"
	}
	page := page(title, s, string(data), mustAsset("merkelbrot.css"), mustAsset("merkelbrot.js"))
	if err := page.Render(ctx, w); err != nil {
		return fmt.Errorf("web: rendering page: %w", err)
	}
	return nil
}

// Ask is a request for more of a graph than the page was first given.
//
// A nil field is one the page did not ask about, and is left as the server had
// it. A field set to zero asks for that limit to be lifted altogether.
type Ask struct {
	// Chain is a new limit on how many links of a chain are laid out, which is what
	// a page asks for when a history was cut short by
	// [github.com/danielriddell21/merkelbrot/layout.Options.MaxChain].
	Chain *int
	// Nodes is a new limit on how much of the source is read, which is what a page
	// asks for when the read stopped at a frontier under a
	// [github.com/danielriddell21/merkelbrot/graph.Limit].
	Nodes *int
}

// Server serves the viewer over HTTP.
type Server struct {
	// Scene is served when no other is asked for.
	Scene *scene.Scene
	// Expand, when set, builds the scene again under new limits, and is what lets a
	// page ask for the parts of the graph its own limits left out.
	//
	// Left nil, a request for more is refused and the page says so. An exported
	// page has no server to ask, so it always says so.
	Expand func(Ask) (*scene.Scene, error)
}

// Handler serves the viewer for a scene.
//
// It responds to GET / with the page and to GET /scene.json with the scene as
// JSON. Any other path returns 404.
func Handler(s *scene.Scene) http.Handler {
	return (&Server{Scene: s}).Handler()
}

// Handler returns the HTTP handler for the server.
//
// Both GET / and GET /scene.json accept chain and nodes query parameters asking
// for the scene to be built again under those limits, zero meaning no limit.
// Without [Server.Expand] set, such a request is refused with 501.
func (srv *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		s, err := srv.sceneFor(r)
		if err != nil {
			http.Error(w, err.Error(), statusFor(err))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := Render(r.Context(), w, s); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	mux.HandleFunc("GET /scene.json", func(w http.ResponseWriter, r *http.Request) {
		s, err := srv.sceneFor(r)
		if err != nil {
			http.Error(w, err.Error(), statusFor(err))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := s.WriteJSON(w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	return mux
}

// errNoExpand reports a request for more of the graph than this server can give.
var errNoExpand = errors.New("web: this server cannot lay the graph out again")

func statusFor(err error) int {
	if errors.Is(err, errNoExpand) {
		return http.StatusNotImplemented
	}
	return http.StatusBadRequest
}

func (srv *Server) sceneFor(r *http.Request) (*scene.Scene, error) {
	q := r.URL.Query()
	chain, err := limitParam(q, "chain")
	if err != nil {
		return nil, err
	}
	nodes, err := limitParam(q, "nodes")
	if err != nil {
		return nil, err
	}
	if chain == nil && nodes == nil {
		return srv.Scene, nil
	}
	if srv.Expand == nil {
		return nil, errNoExpand
	}
	s, err := srv.Expand(Ask{Chain: chain, Nodes: nodes})
	if err != nil {
		return nil, fmt.Errorf("web: building the scene again: %w", err)
	}
	return s, nil
}

// limitParam reads one limit from the query, absent meaning the page did not ask.
func limitParam(q url.Values, name string) (*int, error) {
	raw := q.Get(name)
	if raw == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return nil, fmt.Errorf("web: %s must be a count, zero for no limit: %q", name, raw)
	}
	return &n, nil
}
