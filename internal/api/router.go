package api

import (
	"net/http"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

// RegisterRoutes registra as rotas REST da API no mux informado, a partir de
// um cliente gnjoy já configurado. Fica a cargo de quem chama compor esse
// mux com outras rotas (ex.: o frontend web) e aplicar middlewares.
func RegisterRoutes(mux *http.ServeMux, client *gnjoy.Client) {
	h := NewHandler(client)

	mux.HandleFunc("GET /healthz", Healthz)
	mux.HandleFunc("GET /api/v1/shops", h.SearchShops)
	mux.HandleFunc("GET /api/v1/shops/{svrId}/{mapId}/{ssi}", h.GetStoreDetail)
	mux.HandleFunc("GET /api/v1/shops/{svrId}/{mapId}/{ssi}/item", h.GetItemDetail)
	mux.HandleFunc("GET /api/v1/items/{itemId}/price-history", h.GetPriceHistory)
}
