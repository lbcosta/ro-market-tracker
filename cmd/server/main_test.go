package main

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	// As variáveis inválidas testadas aqui geram logs de erro de propósito.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

func TestEnvOrDefault(t *testing.T) {
	t.Setenv("RO_TESTE_DEFINIDA", "valor")
	t.Setenv("RO_TESTE_VAZIA", "")

	tests := []struct {
		name     string
		key      string
		fallback string
		want     string
	}{
		{"variável definida", "RO_TESTE_DEFINIDA", "padrão", "valor"},
		{"variável vazia usa o padrão", "RO_TESTE_VAZIA", "padrão", "padrão"},
		{"variável ausente usa o padrão", "RO_TESTE_INEXISTENTE", "padrão", "padrão"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := envOrDefault(tt.key, tt.fallback); got != tt.want {
				t.Errorf("envOrDefault(%q, %q) = %q, quero %q", tt.key, tt.fallback, got, tt.want)
			}
		})
	}
}

// TestRateLimitOptionFromEnv cobre a configuração do único parâmetro capaz de
// causar bloqueio no site: um valor inválido não pode virar "sem limite
// nenhum", tem de cair no padrão conservador.
func TestRateLimitOptionFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		rps     string
		burst   string
		wantOk  bool
		comment string
	}{
		{
			name: "nada definido mantém o padrão do client",
			// ok=false faz o chamador nem aplicar a option.
			wantOk: false,
		},
		{name: "só rps", rps: "5", wantOk: true},
		{name: "só burst", burst: "3", wantOk: true},
		{name: "rps e burst", rps: "2.5", burst: "2", wantOk: true},
		{
			name:   "rps inválido sozinho não configura nada",
			rps:    "rápido",
			wantOk: false,
		},
		{
			name:   "burst inválido sozinho não configura nada",
			burst:  "muitos",
			wantOk: false,
		},
		{
			// O valor válido vale; o inválido cai no padrão.
			name: "um válido e um inválido", rps: "4", burst: "muitos", wantOk: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GNJOY_RATE_LIMIT_RPS", tt.rps)
			t.Setenv("GNJOY_RATE_LIMIT_BURST", tt.burst)

			opt, ok := rateLimitOptionFromEnv()
			if ok != tt.wantOk {
				t.Fatalf("ok = %v, quero %v", ok, tt.wantOk)
			}
			if ok && opt == nil {
				t.Error("ok = true mas a option veio nil")
			}
			if !ok && opt != nil {
				t.Error("ok = false mas veio uma option")
			}
		})
	}
}

// TestLogLevelFromEnv fixa o padrão silencioso: quem abriu o executável não
// quer ver o registro de cada requisição passando na janela, mas quem está
// investigando um problema precisa conseguir ligá-lo sem outro binário.
func TestLogLevelFromEnv(t *testing.T) {
	tests := []struct {
		valor string
		want  slog.Level
	}{
		{"", slog.LevelWarn},
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"error", slog.LevelError},
		// Maiúsculas/minúsculas não deveriam importar para quem digita.
		{"DEBUG", slog.LevelDebug},
		{"Info", slog.LevelInfo},
		// Um valor sem sentido cai no padrão em vez de deixar o programa mudo.
		{"faladeira", slog.LevelWarn},
	}

	for _, tt := range tests {
		t.Run(tt.valor, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", tt.valor)
			if got := logLevelFromEnv(); got != tt.want {
				t.Errorf("logLevelFromEnv() = %v, quero %v", got, tt.want)
			}
		})
	}
}

// TestPrintBoasVindasMostraOEndereco: a tela inicial existe para dizer a quem
// abriu o executável o que fazer em seguida. O endereço é o essencial dela —
// sem ele, a janela não serve para nada.
func TestPrintBoasVindasMostraOEndereco(t *testing.T) {
	saida := capturarStdout(t, func() { printBoasVindas("9999") })

	for _, trecho := range []string{
		"RO Market Tracker",
		"http://localhost:9999",
		"watchlist",
		"Ctrl+C",
	} {
		if !strings.Contains(saida, trecho) {
			t.Errorf("a tela inicial não menciona %q.\nSaída:\n%s", trecho, saida)
		}
	}
}

// TestMensagemDePortaOcupada: quando a porta está em uso o programa morre
// antes de servir qualquer coisa, então a mensagem no terminal é a única
// chance de o usuário entender o que houve e o que fazer.
func TestMensagemDePortaOcupada(t *testing.T) {
	msg := mensagemDePortaOcupada("8080", os.ErrPermission)

	for _, trecho := range []string{"8080", "PORT=8081"} {
		if !strings.Contains(msg, trecho) {
			t.Errorf("a mensagem não menciona %q.\nMensagem:\n%s", trecho, msg)
		}
	}
}

// capturarStdout troca o os.Stdout por um pipe durante f e devolve o que foi
// escrito nele.
func capturarStdout(t *testing.T, f func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("criando pipe: %v", err)
	}
	original := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = original }()

	f()
	w.Close()

	saida, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("lendo a saída: %v", err)
	}
	return string(saida)
}
