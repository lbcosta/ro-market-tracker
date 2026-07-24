package web

import (
	"net/http"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

// RegisterRoutes registra as rotas do frontend HTMX (página, busca, expand
// e assets estáticos) no mux informado.
func RegisterRoutes(mux *http.ServeMux, client *gnjoy.Client) {
	h := NewHandler(client)

	mux.HandleFunc("GET /{$}", h.Index)
	mux.HandleFunc("GET /web/search", h.Search)
	mux.HandleFunc("GET /web/shops/{svrId}/{mapId}/{ssi}/expand", h.Expand)
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler()))
}
