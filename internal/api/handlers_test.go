package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/lbcosta/ro-market-tracker/internal/api"
	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
	"github.com/lbcosta/ro-market-tracker/internal/gnjoytest"
)

func TestMain(m *testing.M) {
	// Os handlers logam os erros de upstream que os testes provocam de
	// propósito; sem isso a saída do teste fica poluída de ruído esperado.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// newAPIServer sobe a API REST real ligada ao site falso do GnJoy — nenhum
// teste toca a API de verdade.
func newAPIServer(t *testing.T) (*httptest.Server, *gnjoytest.Server) {
	t.Helper()

	mock := gnjoytest.New(gnjoytest.DemoConfig())
	t.Cleanup(mock.Close)

	client := gnjoy.New(
		gnjoy.WithBaseURL(mock.URL),
		gnjoy.WithActionID(mock.ActionID()),
		gnjoy.WithRateLimit(1000, 1000),
	)

	mux := http.NewServeMux()
	api.RegisterRoutes(mux, client)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv, mock
}

func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("lendo corpo de %s: %v", path, err)
	}
	return resp, body
}

// errorMessage extrai a mensagem do corpo de erro padrão da API, falhando o
// teste se o corpo não tiver esse formato.
func errorMessage(t *testing.T, body []byte) string {
	t.Helper()
	var parsed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("corpo não é um erro JSON: %s", body)
	}
	if parsed.Error == "" {
		t.Fatalf("corpo de erro sem mensagem: %s", body)
	}
	return parsed.Error
}

func TestHealthz(t *testing.T) {
	srv, _ := newAPIServer(t)

	resp, body := get(t, srv, "/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200", resp.StatusCode)
	}
	if got := resp.Header.Get("content-type"); got != "application/json; charset=utf-8" {
		t.Errorf("content-type = %q, quero JSON", got)
	}

	var parsed map[string]string
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if parsed["status"] != "ok" {
		t.Errorf("status = %q, quero \"ok\"", parsed["status"])
	}
}

func TestSearchShops(t *testing.T) {
	srv, mock := newAPIServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "storeType": {"BUY"}, "item": {"Espada"}}
	resp, body := get(t, srv, "/api/v1/shops?"+q.Encode())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200; corpo: %s", resp.StatusCode, body)
	}

	var result gnjoy.ShopSearchResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if len(result.Items) != 5 || result.TotalCount != 5 {
		t.Errorf("resultado = %d itens / total %d, quero 5/5", len(result.Items), result.TotalCount)
	}

	if got := mock.Requests()[0].Query.Get("searchWord"); got != "Espada" {
		t.Errorf("o termo repassado ao upstream foi %q, quero \"Espada\"", got)
	}
}

// TestSearchShopsStoreTypeMinusculo documenta que o parâmetro é tratado sem
// diferenciar maiúsculas — quem consome a API não precisa adivinhar o caso.
func TestSearchShopsStoreTypeMinusculo(t *testing.T) {
	srv, mock := newAPIServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "storeType": {"sell"}, "item": {"Espada"}}
	resp, body := get(t, srv, "/api/v1/shops?"+q.Encode())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200; corpo: %s", resp.StatusCode, body)
	}
	if got := mock.Requests()[0].Query.Get("storeType"); got != "SELL" {
		t.Errorf("storeType repassado = %q, quero \"SELL\"", got)
	}
}

func TestSearchShopsParametrosInvalidos(t *testing.T) {
	srv, mock := newAPIServer(t)

	tests := []struct {
		name  string
		query url.Values
	}{
		{"sem server", url.Values{"storeType": {"BUY"}, "item": {"Espada"}}},
		{"sem item", url.Values{"server": {"NIDHOGG"}, "storeType": {"BUY"}}},
		{"sem storeType", url.Values{"server": {"NIDHOGG"}, "item": {"Espada"}}},
		{"storeType desconhecido", url.Values{"server": {"NIDHOGG"}, "storeType": {"TROCA"}, "item": {"Espada"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock.ResetRequests()

			resp, body := get(t, srv, "/api/v1/shops?"+tt.query.Encode())
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, quero 400; corpo: %s", resp.StatusCode, body)
			}
			errorMessage(t, body)

			// Validação inválida não pode custar uma requisição ao upstream.
			if got := mock.RequestCount(); got != 0 {
				t.Errorf("houve %d requisições ao upstream, quero 0", got)
			}
		})
	}
}

// TestSearchShopsErroDoUpstream cobre a tradução de falhas do site em status
// HTTP próprios: um rate limiting persistente vira 503 (condição temporária),
// qualquer outra resposta estranha vira 502.
func TestSearchShopsErroDoUpstream(t *testing.T) {
	tests := []struct {
		name       string
		failure    gnjoytest.Failure
		times      int
		wantStatus int
	}{
		{
			name:       "429 persistente vira 503",
			failure:    gnjoytest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "0"},
			times:      10,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "500 do site vira 502",
			failure:    gnjoytest.Failure{Status: http.StatusInternalServerError},
			times:      1,
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "resposta fora do formato esperado vira 502",
			failure:    gnjoytest.Failure{Malformed: true},
			times:      1,
			wantStatus: http.StatusBadGateway,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, mock := newAPIServer(t)
			mock.QueueFailure(tt.failure, tt.times)

			q := url.Values{"server": {"NIDHOGG"}, "storeType": {"BUY"}, "item": {"Espada"}}
			resp, body := get(t, srv, "/api/v1/shops?"+q.Encode())
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, quero %d; corpo: %s", resp.StatusCode, tt.wantStatus, body)
			}
			errorMessage(t, body)
		})
	}
}

func TestGetStoreDetail(t *testing.T) {
	srv, _ := newAPIServer(t)

	resp, body := get(t, srv, "/api/v1/shops/303/835/s-primordial-158")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200; corpo: %s", resp.StatusCode, body)
	}

	var detail gnjoy.StoreDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	// O refino é o dado que só esta rota expõe — precisa chegar já parseado
	// ao consumidor da API, não embutido no nome.
	if detail.Refine != 7 {
		t.Errorf("Refine = %d, quero 7", detail.Refine)
	}
	if detail.StoreName != "Wololol" {
		t.Errorf("StoreName = %q, quero \"Wololol\"", detail.StoreName)
	}
}

func TestGetStoreDetailPathInvalido(t *testing.T) {
	srv, mock := newAPIServer(t)

	tests := []struct {
		name string
		path string
	}{
		{"svrId não numérico", "/api/v1/shops/abc/835/s-citadina"},
		{"mapId não numérico", "/api/v1/shops/303/xyz/s-citadina"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock.ResetRequests()

			resp, body := get(t, srv, tt.path)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, quero 400; corpo: %s", resp.StatusCode, body)
			}
			errorMessage(t, body)
			if got := mock.RequestCount(); got != 0 {
				t.Errorf("houve %d requisições ao upstream, quero 0", got)
			}
		})
	}
}

// TestGetStoreDetailSsiVazio chama o handler direto porque o roteador nunca
// deixaria um segmento vazio chegar até ele — a validação existe como defesa
// para quem registrar a rota de outro jeito.
func TestGetStoreDetailSsiVazio(t *testing.T) {
	mock := gnjoytest.New(gnjoytest.DemoConfig())
	defer mock.Close()

	client := gnjoy.New(gnjoy.WithBaseURL(mock.URL), gnjoy.WithRateLimit(1000, 1000))
	h := api.NewHandler(client)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/shops/303/835/", nil)
	req.SetPathValue("svrId", "303")
	req.SetPathValue("mapId", "835")
	req.SetPathValue("ssi", "")
	rec := httptest.NewRecorder()

	h.GetStoreDetail(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400", rec.Code)
	}
	if got := mock.RequestCount(); got != 0 {
		t.Errorf("houve %d requisições ao upstream, quero 0", got)
	}
}

func TestGetStoreDetailErroDoUpstream(t *testing.T) {
	srv, mock := newAPIServer(t)
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusBadGateway}, 10)

	resp, body := get(t, srv, "/api/v1/shops/303/835/s-citadina")
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, quero 502; corpo: %s", resp.StatusCode, body)
	}
	errorMessage(t, body)
}

func TestGetItemDetail(t *testing.T) {
	srv, mock := newAPIServer(t)

	resp, body := get(t, srv, "/api/v1/shops/303/835/s-citadina/item")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200; corpo: %s", resp.StatusCode, body)
	}

	var detail gnjoy.ItemDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if detail.ItemId != 1147 || detail.ItemName != "Espada Citadina" {
		t.Errorf("item = (%d, %q), quero (1147, \"Espada Citadina\")", detail.ItemId, detail.ItemName)
	}
	if got := mock.Requests()[0].Body; !strings.Contains(got, `"multiLan":"en-US"`) {
		t.Errorf("payload = %s, quero o lang padrão en-US", got)
	}
}

func TestGetItemDetailComLang(t *testing.T) {
	srv, mock := newAPIServer(t)

	resp, _ := get(t, srv, "/api/v1/shops/303/835/s-citadina/item?lang=pt-BR")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200", resp.StatusCode)
	}
	if got := mock.Requests()[0].Body; !strings.Contains(got, `"multiLan":"pt-BR"`) {
		t.Errorf("payload = %s, quero multiLan pt-BR", got)
	}
}

func TestGetItemDetailPathInvalido(t *testing.T) {
	srv, _ := newAPIServer(t)

	resp, body := get(t, srv, "/api/v1/shops/abc/835/s-citadina/item")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400", resp.StatusCode)
	}
	errorMessage(t, body)
}

func TestGetItemDetailErroDoUpstream(t *testing.T) {
	srv, mock := newAPIServer(t)
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError}, 10)

	resp, body := get(t, srv, "/api/v1/shops/303/835/s-citadina/item")
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, quero 502; corpo: %s", resp.StatusCode, body)
	}
	errorMessage(t, body)
}

func TestGetPriceHistory(t *testing.T) {
	srv, mock := newAPIServer(t)

	resp, body := get(t, srv, "/api/v1/items/600009/price-history?server=303")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200; corpo: %s", resp.StatusCode, body)
	}

	var history gnjoy.PriceHistory
	if err := json.Unmarshal(body, &history); err != nil {
		t.Fatalf("corpo inválido: %s", body)
	}
	if len(history.DayStatsList) != 3 {
		t.Errorf("len(DayStatsList) = %d, quero 3", len(history.DayStatsList))
	}

	// Sem page/limit na query, o client aplica os padrões dele.
	payload := mock.Requests()[0].Body
	if !strings.Contains(payload, `"itemId":600009`) || !strings.Contains(payload, `"svrId":303`) {
		t.Errorf("payload = %s, quero itemId 600009 e svrId 303", payload)
	}
}

func TestGetPriceHistoryComPaginacao(t *testing.T) {
	srv, mock := newAPIServer(t)

	resp, _ := get(t, srv, "/api/v1/items/600009/price-history?server=303&page=2&limit=5&period=WEEK")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200", resp.StatusCode)
	}

	payload := mock.Requests()[0].Body
	for _, want := range []string{`"page":2`, `"limit":5`, `"period":"WEEK"`} {
		if !strings.Contains(payload, want) {
			t.Errorf("payload = %s, quero que contenha %s", payload, want)
		}
	}
}

func TestGetPriceHistoryParametrosInvalidos(t *testing.T) {
	srv, mock := newAPIServer(t)

	tests := []struct {
		name string
		path string
	}{
		{"itemId não numérico", "/api/v1/items/abc/price-history?server=303"},
		{"sem server", "/api/v1/items/600009/price-history"},
		{"server não numérico", "/api/v1/items/600009/price-history?server=NIDHOGG"},
		{"page não numérica", "/api/v1/items/600009/price-history?server=303&page=x"},
		{"page zero", "/api/v1/items/600009/price-history?server=303&page=0"},
		{"page negativa", "/api/v1/items/600009/price-history?server=303&page=-1"},
		{"limit não numérico", "/api/v1/items/600009/price-history?server=303&limit=x"},
		{"limit zero", "/api/v1/items/600009/price-history?server=303&limit=0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock.ResetRequests()

			resp, body := get(t, srv, tt.path)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, quero 400; corpo: %s", resp.StatusCode, body)
			}
			errorMessage(t, body)
			if got := mock.RequestCount(); got != 0 {
				t.Errorf("houve %d requisições ao upstream, quero 0", got)
			}
		})
	}
}

func TestGetPriceHistoryErroDoUpstream(t *testing.T) {
	srv, mock := newAPIServer(t)
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError}, 10)

	resp, body := get(t, srv, "/api/v1/items/600009/price-history?server=303")
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, quero 502; corpo: %s", resp.StatusCode, body)
	}
	errorMessage(t, body)
}
