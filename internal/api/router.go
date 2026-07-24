package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

// NewRouter monta as rotas REST da API a partir de um cliente gnjoy já
// configurado.
func NewRouter(client *gnjoy.Client) http.Handler {
	h := NewHandler(client)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", Healthz)
	mux.HandleFunc("GET /api/v1/shops", h.SearchShops)
	mux.HandleFunc("GET /api/v1/shops/{svrId}/{mapId}/{ssi}", h.GetStoreDetail)
	mux.HandleFunc("GET /api/v1/shops/{svrId}/{mapId}/{ssi}/item", h.GetItemDetail)
	mux.HandleFunc("GET /api/v1/items/{itemId}/price-history", h.GetPriceHistory)

	return withLogging(mux)
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}
