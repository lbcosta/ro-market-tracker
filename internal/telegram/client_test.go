package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendMandaChatIDETextoParaOEndpointCerto(t *testing.T) {
	var metodo, caminho string
	var corpo string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metodo = r.Method
		caminho = r.URL.Path
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		corpo = r.Form.Encode()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := New("TOKEN123", "999", WithBaseURL(srv.URL))
	if err := client.Send(context.Background(), "Grimório de Combate atingiu o alvo: 6.500.000 z"); err != nil {
		t.Fatalf("Send retornou erro: %v", err)
	}

	if metodo != http.MethodPost {
		t.Errorf("método = %q, esperado POST", metodo)
	}
	if caminho != "/botTOKEN123/sendMessage" {
		t.Errorf("caminho = %q, esperado /botTOKEN123/sendMessage", caminho)
	}
	if !strings.Contains(corpo, "chat_id=999") {
		t.Errorf("corpo sem chat_id=999: %q", corpo)
	}
	if !strings.Contains(corpo, "Grim") {
		t.Errorf("corpo sem o texto da mensagem: %q", corpo)
	}
	if !strings.Contains(corpo, "parse_mode=HTML") {
		t.Errorf("corpo sem parse_mode=HTML: %q", corpo)
	}
}

func TestSendDevolveHTTPErrorQuandoATelegramRecusa(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
	}))
	defer srv.Close()

	client := New("token-invalido", "999", WithBaseURL(srv.URL))
	err := client.Send(context.Background(), "oi")
	if err == nil {
		t.Fatal("esperava erro, não veio nenhum")
	}

	httpErr, ok := err.(*HTTPError)
	if !ok {
		t.Fatalf("erro = %T, esperado *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, esperado 401", httpErr.StatusCode)
	}
	if !strings.Contains(httpErr.Body, "Unauthorized") {
		t.Errorf("Body sem a descrição do erro: %q", httpErr.Body)
	}
}
