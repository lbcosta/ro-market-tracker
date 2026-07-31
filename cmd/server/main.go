// Comando server sobe a API REST do ro-market-tracker: um serviço HTTP que
// traduz consultas REST em chamadas às rotas internas do site do GnJoy
// LATAM e devolve os dados de mercado em JSON.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/lbcosta/ro-market-tracker/internal/api"
	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
	"github.com/lbcosta/ro-market-tracker/internal/web"
)

// version identifica o binário. O build de release a injeta com a tag do Git
// (ver .github/workflows/release.yml); em builds locais fica "dev", que é o
// que distingue "compilei aqui agora" de "baixei o executável da release".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "mostra a versão do binário e sai")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	addr := ":" + envOrDefault("PORT", "8080")

	var opts []gnjoy.Option
	if baseURL := os.Getenv("GNJOY_BASE_URL"); baseURL != "" {
		opts = append(opts, gnjoy.WithBaseURL(baseURL))
	}
	if locale := os.Getenv("GNJOY_LOCALE"); locale != "" {
		opts = append(opts, gnjoy.WithLocale(locale))
	}
	if actionID := os.Getenv("GNJOY_ACTION_ID"); actionID != "" {
		opts = append(opts, gnjoy.WithActionID(actionID))
	}
	if opt, ok := rateLimitOptionFromEnv(); ok {
		opts = append(opts, opt)
	}

	client := gnjoy.New(opts...)

	mux := http.NewServeMux()
	api.RegisterRoutes(mux, client)
	web.RegisterRoutes(mux, client)

	slog.Info("iniciando ro-market-tracker", "versao", version, "addr", addr)
	if err := http.ListenAndServe(addr, withLogging(mux)); err != nil {
		slog.Error("servidor encerrado", "error", err)
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}

// rateLimitOptionFromEnv monta a option de rate limit a partir de
// GNJOY_RATE_LIMIT_RPS / GNJOY_RATE_LIMIT_BURST, caindo para o padrão
// conservador de gnjoy.DefaultRateLimitRPS/Burst quando uma delas não é
// informada. Retorna ok=false se nenhuma das duas foi definida (mantém o
// padrão do client).
func rateLimitOptionFromEnv() (gnjoy.Option, bool) {
	rps := gnjoy.DefaultRateLimitRPS
	burst := gnjoy.DefaultRateLimitBurst
	configured := false

	if v := os.Getenv("GNJOY_RATE_LIMIT_RPS"); v != "" {
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			slog.Error("GNJOY_RATE_LIMIT_RPS inválido, usando padrão", "value", v, "error", err)
		} else {
			rps = parsed
			configured = true
		}
	}
	if v := os.Getenv("GNJOY_RATE_LIMIT_BURST"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil {
			slog.Error("GNJOY_RATE_LIMIT_BURST inválido, usando padrão", "value", v, "error", err)
		} else {
			burst = parsed
			configured = true
		}
	}
	if !configured {
		return nil, false
	}
	return gnjoy.WithRateLimit(rps, burst), true
}
