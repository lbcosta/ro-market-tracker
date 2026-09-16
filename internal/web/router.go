package web

import (
	"net/http"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

// RegisterRoutes registra as rotas do frontend HTMX (as duas páginas, busca,
// expand e assets estáticos) no mux informado. version é a versão do binário (ver
// main.version em cmd/server) — mostrada num canto discreto da página, é o
// que o navegador compara com a última release do GitHub. opts é onde entra
// WithTelegramClient, quando a integração estiver configurada.
func RegisterRoutes(mux *http.ServeMux, client *gnjoy.Client, version string, opts ...HandlerOption) {
	h := NewHandler(client, version, opts...)

	mux.HandleFunc("GET /{$}", h.Watchlist)
	mux.HandleFunc("GET /estoque", h.Estoque)
	mux.HandleFunc("GET /web/estoque/validar", h.EstoqueValidar)
	mux.HandleFunc("GET /web/search", h.Search)
	mux.HandleFunc("GET /web/search/variants", h.Variants)
	mux.HandleFunc("GET /web/shops/{svrId}/{mapId}/{ssi}/expand", h.Expand)
	mux.HandleFunc("GET /web/watchlist/price", h.WatchlistPrice)
	mux.HandleFunc("POST /web/watchlist/notify-telegram", h.NotifyTelegram)
	mux.HandleFunc("POST /web/cache/reset", h.ResetCaches)
	mux.HandleFunc("GET /web/activity/stream", h.ActivityStream)
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler()))
}
