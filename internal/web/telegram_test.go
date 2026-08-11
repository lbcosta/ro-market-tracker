package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
	"github.com/lbcosta/ro-market-tracker/internal/telegram"
)

// newWebServerComTelegram sobe o mux de verdade com um telegram.Client
// apontando para telegramMock (ou nil, se o teste quiser exercitar o caminho
// "não configurado").
func newWebServerComTelegram(t *testing.T, telegramClient *telegram.Client) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	RegisterRoutes(mux, gnjoy.New(), "test", WithTelegramClient(telegramClient))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestNotifyTelegramRepassaOTexto: é o caminho feliz — o texto que o
// navegador manda chega até a API do Telegram, com o chat_id configurado.
func TestNotifyTelegramRepassaOTexto(t *testing.T) {
	var corpoRecebido string
	mockTelegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		corpoRecebido = r.Form.Encode()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(mockTelegram.Close)

	client := telegram.New("TOKEN", "999", telegram.WithBaseURL(mockTelegram.URL))
	srv := newWebServerComTelegram(t, client)

	resp, err := http.Post(srv.URL+"/web/watchlist/notify-telegram", "application/json",
		strings.NewReader(`{"text":"Grimório de Combate atingiu o alvo: 6.500.000 z"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, esperado 204", resp.StatusCode)
	}
	if !strings.Contains(corpoRecebido, "chat_id=999") {
		t.Errorf("mock do Telegram não recebeu chat_id=999: %q", corpoRecebido)
	}
	if !strings.Contains(corpoRecebido, "Grim") {
		t.Errorf("mock do Telegram não recebeu o texto da mensagem: %q", corpoRecebido)
	}
}

// TestNotifyTelegramSemConfiguracaoENoOp: sem telegramClient (o estado padrão
// de quem baixou a release e não editou telegram.txt), o endpoint não erra
// — só não faz nada.
func TestNotifyTelegramSemConfiguracaoENoOp(t *testing.T) {
	srv := newWebServerComTelegram(t, nil)

	resp, err := http.Post(srv.URL+"/web/watchlist/notify-telegram", "application/json",
		strings.NewReader(`{"text":"oi"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, esperado 204", resp.StatusCode)
	}
}

// TestNotifyTelegramFalhaDoTelegramNaoInterrompeNada: uma falha na API do
// Telegram (token inválido, por exemplo) vira só um log no servidor — não
// há nada mais para o navegador fazer com isso (ver o comentário em
// NotifyTelegram), mas o endpoint precisa responder algo coerente.
func TestNotifyTelegramFalhaDoTelegramNaoInterrompeNada(t *testing.T) {
	mockTelegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
	}))
	t.Cleanup(mockTelegram.Close)

	client := telegram.New("token-invalido", "999", telegram.WithBaseURL(mockTelegram.URL))
	srv := newWebServerComTelegram(t, client)

	resp, err := http.Post(srv.URL+"/web/watchlist/notify-telegram", "application/json",
		strings.NewReader(`{"text":"oi"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, esperado 502", resp.StatusCode)
	}
}

// TestNotifyTelegramSemTextoDaBadRequest: o navegador sempre manda "text"
// preenchido (ver notifyTelegram em watchlist.js) — isto cobre uma
// requisição malformada, não um caminho que o app dispara sozinho.
func TestNotifyTelegramSemTextoDaBadRequest(t *testing.T) {
	client := telegram.New("TOKEN", "999")
	srv := newWebServerComTelegram(t, client)

	resp, err := http.Post(srv.URL+"/web/watchlist/notify-telegram", "application/json",
		strings.NewReader(`{"text":""}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, esperado 400", resp.StatusCode)
	}
}
