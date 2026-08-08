// Comando server sobe a API REST do ro-market-tracker: um serviço HTTP que
// traduz consultas REST em chamadas às rotas internas do site do GnJoy
// LATAM e devolve os dados de mercado em JSON.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
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

	configureLogging()

	port := envOrDefault("PORT", "8080")

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
	// Ferramenta de diagnóstico: mostra no log a resposta crua do site, para
	// descobrir se ele manda algum campo que o programa ainda não lê (ver
	// gnjoy.WithActionDump). Desligada por padrão — o log fica volumoso.
	if os.Getenv("GNJOY_DUMP_ACTIONS") == "1" {
		opts = append(opts, gnjoy.WithActionDump())
	}
	// Um 429 aqui não é um tropeço a insistir: é o site dizendo que a cota
	// acabou. O programa para de falar com ele, avisa na tela e espera —
	// insistir a partir daí só prolonga o bloqueio (ver internal/gnjoy/suspend.go).
	opts = append(opts, gnjoy.WithSuspendOn429())

	client := gnjoy.New(opts...)

	mux := http.NewServeMux()
	api.RegisterRoutes(mux, client)
	web.RegisterRoutes(mux, client, version)

	// Quem tira o programa da suspensão: sonda o site de tempos em tempos e
	// libera tudo quando ele volta. context.Background() porque não há
	// shutdown ordenado para pendurar isto — ver o comentário no http.Serve
	// abaixo.
	web.StartSuspensionProbe(context.Background(), client, probeIntervalFromEnv())

	// Ocupa a porta antes de dar as boas-vindas: se ela estiver em uso, o
	// usuário precisa ver o motivo, e não um endereço que não vai abrir.
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		fmt.Fprint(os.Stderr, mensagemDePortaOcupada(port, err))
		os.Exit(1)
	}

	printBoasVindas(port)

	// http.Serve direto, sem os timeouts de um http.Server: a ausência deles
	// é deliberada. Um WriteTimeout mataria o stream SSE da barra de
	// atividades, que por construção nunca termina, e cortaria a busca com a
	// verificação de refino/bônus ligada, que pode passar de um minuto.
	if err := http.Serve(ln, withLogging(mux)); err != nil {
		slog.Error("servidor encerrado", "error", err)
		os.Exit(1)
	}
}

// printBoasVindas é a primeira coisa que alguém vê ao abrir o executável: um
// duplo clique no .exe abre um terminal e, sem isto, a janela ficaria em
// branco sem dizer o que fazer em seguida.
//
// Sai pelo fmt, e não pelo slog: é uma tela para ler, não um registro de
// evento — não faz sentido carregar horário e nível de severidade. O endereço
// vai como URL crua porque os terminais atuais (Windows Terminal, iTerm2,
// GNOME Terminal) a transformam em link clicável sozinhos; sequências de
// escape para hyperlink apareceriam como lixo nos que não as suportam.
func printBoasVindas(port string) {
	fmt.Printf(`
  RO Market Tracker %s
  Preços do mercado de comércio do Ragnarok Online LATAM

  Abra no navegador:  http://localhost:%s

  O que dá para fazer por lá:
    - Buscar um item e ver quem está anunciando, por quanto e em que loja
    - Clicar em um anúncio para ver o refino, onde fica o vendedor e como
      o preço se comportou nos últimos dias
    - Acompanhar itens na watchlist e ser avisado quando o preço cair até
      o alvo que você definir

  Esta janela precisa ficar aberta enquanto você usa o site.
  Para encerrar, feche-a ou pressione Ctrl+C.

`, version, port)
}

// mensagemDePortaOcupada troca o erro cru do sistema por uma saída acionável:
// quase sempre a causa é outro programa (ou outra cópia deste) já ocupando a
// porta, e o que falta ao usuário é saber como escolher outra.
func mensagemDePortaOcupada(port string, err error) string {
	return fmt.Sprintf(`
  Não foi possível abrir a porta %s: %v

  Se outro programa já estiver usando essa porta, escolha outra:

    Windows:       set PORT=8081 && ro-market-tracker.exe
    Linux/macOS:   PORT=8081 ./ro-market-tracker

`, port, err)
}

// configureLogging deixa o programa quieto por padrão: depois da tela inicial,
// quem está usando só precisa saber de algo que exija atenção. LOG_LEVEL=debug
// liga o registro de cada requisição, para investigar um problema sem precisar
// de outro binário.
func configureLogging() {
	opts := &slog.HandlerOptions{
		Level: logLevelFromEnv(),
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Data e fuso não ajudam quem está com a janela aberta na frente.
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.String(slog.TimeKey, a.Value.Time().Format("15:04:05"))
			}
			return a
		},
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, opts)))
}

func logLevelFromEnv() slog.Level {
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "error":
		return slog.LevelError
	default:
		// Avisos e erros: o que o usuário precisa ver, e nada além disso.
		return slog.LevelWarn
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// withLogging registra cada requisição em nível de depuração. Uma sessão
// normal gera uma enxurrada delas (cada asset, cada consulta da watchlist, o
// stream da barra de atividades), e nenhuma diz nada a quem está usando o
// programa — mas todas importam quando se está investigando um problema, e é
// para isso que existe LOG_LEVEL=debug.
func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Debug("requisição", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
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

// probeIntervalFromEnv lê de quanto em quanto tempo sondar o site enquanto as
// consultas estiverem suspensas por um 429. Existe como variável de ambiente
// para os testes de navegador poderem observar a recuperação sem esperar dez
// minutos — exercitando o caminho real, em vez de uma rota de escape que só
// existiria para eles.
func probeIntervalFromEnv() time.Duration {
	v := os.Getenv("GNJOY_SUSPENSION_PROBE_INTERVAL")
	if v == "" {
		return 0 // o padrão do pacote web
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		slog.Error("GNJOY_SUSPENSION_PROBE_INTERVAL inválido, usando padrão", "value", v, "error", err)
		return 0
	}
	return d
}
