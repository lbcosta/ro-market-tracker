// Package api expõe, em formato REST/JSON, os dados de mercado obtidos do
// site do GnJoy LATAM através do pacote internal/gnjoy.
package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

type Handler struct {
	client *gnjoy.Client
}

func NewHandler(client *gnjoy.Client) *Handler {
	return &Handler{client: client}
}

// SearchShops trata GET /api/v1/shops?server=X&storeType=BUY&item=Y
func (h *Handler) SearchShops(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	server := q.Get("server")
	item := q.Get("item")
	storeType := gnjoy.StoreType(strings.ToUpper(q.Get("storeType")))

	if server == "" || item == "" {
		writeErrorMsg(w, http.StatusBadRequest, "os parâmetros 'server' e 'item' são obrigatórios")
		return
	}
	if storeType != gnjoy.StoreTypeBuy && storeType != gnjoy.StoreTypeSell {
		writeErrorMsg(w, http.StatusBadRequest, "o parâmetro 'storeType' deve ser BUY ou SELL")
		return
	}

	result, err := h.client.SearchShops(r.Context(), gnjoy.SearchShopsParams{
		ServerType: server,
		StoreType:  storeType,
		SearchWord: item,
	})
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// GetStoreDetail trata GET /api/v1/shops/{svrId}/{mapId}/{ssi}
func (h *Handler) GetStoreDetail(w http.ResponseWriter, r *http.Request) {
	loc, ok := parseStoreLocation(w, r)
	if !ok {
		return
	}
	detail, err := h.client.GetStoreDetail(r.Context(), loc)
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// GetItemDetail trata GET /api/v1/shops/{svrId}/{mapId}/{ssi}/item?lang=en-US
func (h *Handler) GetItemDetail(w http.ResponseWriter, r *http.Request) {
	loc, ok := parseStoreLocation(w, r)
	if !ok {
		return
	}
	detail, err := h.client.GetItemDetail(r.Context(), loc, r.URL.Query().Get("lang"))
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func parseStoreLocation(w http.ResponseWriter, r *http.Request) (gnjoy.StoreLocation, bool) {
	svrId, err := strconv.Atoi(r.PathValue("svrId"))
	if err != nil {
		writeErrorMsg(w, http.StatusBadRequest, "'svrId' deve ser um número inteiro")
		return gnjoy.StoreLocation{}, false
	}
	mapId, err := strconv.Atoi(r.PathValue("mapId"))
	if err != nil {
		writeErrorMsg(w, http.StatusBadRequest, "'mapId' deve ser um número inteiro")
		return gnjoy.StoreLocation{}, false
	}
	ssi := r.PathValue("ssi")
	if ssi == "" {
		writeErrorMsg(w, http.StatusBadRequest, "'ssi' é obrigatório")
		return gnjoy.StoreLocation{}, false
	}
	return gnjoy.StoreLocation{SvrId: svrId, MapId: mapId, SSI: ssi}, true
}

// GetPriceHistory trata GET /api/v1/items/{itemId}/price-history?server=303&page=1&limit=10
func (h *Handler) GetPriceHistory(w http.ResponseWriter, r *http.Request) {
	itemId, err := strconv.Atoi(r.PathValue("itemId"))
	if err != nil {
		writeErrorMsg(w, http.StatusBadRequest, "'itemId' deve ser um número inteiro")
		return
	}

	q := r.URL.Query()
	serverStr := q.Get("server")
	if serverStr == "" {
		writeErrorMsg(w, http.StatusBadRequest, "o parâmetro 'server' (svrId numérico) é obrigatório")
		return
	}
	svrId, err := strconv.Atoi(serverStr)
	if err != nil {
		writeErrorMsg(w, http.StatusBadRequest, "'server' deve ser um número inteiro (svrId)")
		return
	}

	params := gnjoy.PriceHistoryParams{ItemId: itemId, SvrId: svrId}

	if pageStr := q.Get("page"); pageStr != "" {
		page, err := strconv.Atoi(pageStr)
		if err != nil || page < 1 {
			writeErrorMsg(w, http.StatusBadRequest, "'page' deve ser um número inteiro positivo")
			return
		}
		params.Page = page
	}
	if limitStr := q.Get("limit"); limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit < 1 {
			writeErrorMsg(w, http.StatusBadRequest, "'limit' deve ser um número inteiro positivo")
			return
		}
		params.Limit = limit
	}
	if period := q.Get("period"); period != "" {
		params.Period = &period
	}

	history, err := h.client.GetPriceHistory(r.Context(), params)
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func Healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
