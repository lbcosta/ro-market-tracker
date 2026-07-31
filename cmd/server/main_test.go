package main

import (
	"io"
	"log/slog"
	"os"
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
