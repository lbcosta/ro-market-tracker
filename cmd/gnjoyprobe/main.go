// Comando gnjoyprobe é uma sonda de diagnóstico do motor de requisições ao
// GnJoy: exercita, uma a uma, TODAS as rotas e Server Actions que o servidor
// usa (internal/gnjoy.Client) e mostra o que passou pelo fio — a requisição
// montada, o status, o tempo e, quando pedido, o corpo CRU da resposta.
//
// Existe porque a aplicação só mostra o resultado já decodificado: quando algo
// "não funciona", a tela não distingue o site tendo respondido a página de
// erro dele, um 429, um action id desatualizado ou uma lista em formato novo
// que o parser não reconhece mais. Aqui cada rota falha (ou passa) sozinha, e
// o corpo aparece como veio.
//
// São sete testes, um por rota do motor, rodados nesta ordem — a mesma em que
// o uso real da aplicação os dispara, e que faz a busca produzir o anúncio de
// verdade que as actions de detalhe precisam:
//
//	search    GET  /{locale}/intro/shop-search/trading        (SearchShops)
//	market    GET  /{locale}/intro/shop-search/market-price   (SearchMarketPrice)
//	store     POST action "store"                             (GetStoreDetail)
//	item      POST action "item"                              (GetItemDetail)
//	price     POST action "price"                             (GetPriceHistory)
//	ping      POST action "store" com parâmetros vazios        (WarmupActionID)
//	discover  GET  página HTML + chunks JS                    (RefreshActionID)
//
// Uso:
//
//	go run ./cmd/gnjoyprobe                          # a suíte inteira
//	go run ./cmd/gnjoyprobe -only search -raw        # só a busca, corpo cru
//	go run ./cmd/gnjoyprobe -only store,item,price   # só as actions de detalhe
//	go run ./cmd/gnjoyprobe -item "Espada Primordial" -server FREYA
//
// Respeita as mesmas variáveis de ambiente do servidor (GNJOY_BASE_URL,
// GNJOY_LOCALE, GNJOY_ACTION_ID), para que a sonda converse com exatamente o
// mesmo destino que a aplicação com problema.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httputil"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

// itemLang é o idioma pedido no detalhe de item, o mesmo que o frontend usa
// (ver a constante homônima em internal/web/handlers.go): é dele que saem os
// bônus aleatórios em português.
const itemLang = "pt-BR"

func main() {
	item := flag.String("item", "Bênção do Ferreiro", "nome (ou trecho) do item procurado")
	server := flag.String("server", "NIDHOGG", "servidor consultado (NIDHOGG ou FREYA)")
	store := flag.String("store", string(gnjoy.StoreTypeBuy), "tipo de loja: BUY (o que a aplicação usa) ou SELL")
	period := flag.String("period", gnjoy.MarketPricePeriodAll, "período dos preços de mercado: ALL, 1, 7 ou 30")
	only := flag.String("only", "", "roda apenas os testes indicados, separados por vírgula (ver a lista em -h)")
	raw := flag.Bool("raw", false, "despeja o corpo CRU de TODA resposta (verboso: a descoberta baixa chunks JS inteiros)")
	maxBody := flag.Int("maxbody", 4096, "quantos bytes do corpo mostrar ao explicar uma falha")
	jsonOut := flag.Bool("json", false, "imprime também a struct decodificada de cada teste que passa")
	timeout := flag.Duration("timeout", 2*time.Minute, "prazo de cada teste, incluindo fila do rate limiter e retentativas")
	flag.Parse()

	tr := &dumpTransport{base: http.DefaultTransport, raw: *raw}

	// O mesmo http.Client que o gnjoy.New monta por padrão (15s de prazo por
	// requisição e o cookie jar que guarda o _cfuvid do Cloudflare), só que com
	// o transporte embrulhado pelo dump: qualquer outro valor mudaria
	// justamente o comportamento sob investigação.
	jar, _ := cookiejar.New(nil)
	opts := []gnjoy.Option{gnjoy.WithHTTPClient(&http.Client{
		Timeout:   15 * time.Second,
		Transport: tr,
		Jar:       jar,
	})}
	if baseURL := os.Getenv("GNJOY_BASE_URL"); baseURL != "" {
		opts = append(opts, gnjoy.WithBaseURL(baseURL))
	}
	if locale := os.Getenv("GNJOY_LOCALE"); locale != "" {
		opts = append(opts, gnjoy.WithLocale(locale))
	}
	if actionID := os.Getenv("GNJOY_ACTION_ID"); actionID != "" {
		opts = append(opts, gnjoy.WithActionID(actionID))
	}
	// Sem WithSuspendOn429 de propósito: aqui um 429 é o diagnóstico, e não
	// algo a esconder atrás da tela de suspensão do servidor.
	st := &state{
		client:    gnjoy.New(opts...),
		item:      *item,
		server:    *server,
		storeType: gnjoy.StoreType(strings.ToUpper(*store)),
		period:    *period,
	}

	selected, err := selectChecks(*only)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	fmt.Printf("=== SONDA ===\nitem      : %q\nservidor  : %s\nstoreType : %s\nperíodo   : %s\nbase      : %s\naction id : %s (embutido no binário)\ntestes    : %d\n",
		st.item, st.server, st.storeType, st.period, baseOrDefault(), gnjoy.DefaultActionID, len(selected))

	results := make([]result, 0, len(selected))
	for i, c := range selected {
		fmt.Printf("\n%s\n[%d/%d] %s · %s\n", strings.Repeat("-", 76), i+1, len(selected), c.name, c.route)

		from := tr.len()
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		start := time.Now()
		summary, payload, err := c.run(ctx, st)
		elapsed := time.Since(start)
		cancel()

		calls := tr.since(from)
		for _, call := range calls {
			fmt.Println("   " + call.line())
		}

		res := result{check: c, elapsed: elapsed, summary: summary, err: err}
		switch {
		case errors.Is(err, errSkip):
			res.verdict = "PULADO"
			fmt.Printf("   PULADO · %v\n", err)
		case err != nil:
			res.verdict = "FALHA"
			fmt.Printf("   FALHA (%s) · %v\n", round(elapsed), err)
			// O corpo da última resposta é o que explica a falha; com -raw ele
			// já saiu inteiro acima, então não se repete aqui.
			if !*raw {
				if last := lastWithBody(calls); last != nil {
					fmt.Printf("   ---- corpo da resposta #%d (%d bytes, mostrando até %d) ----\n%s\n   ---- fim ----\n",
						last.n, last.size, *maxBody, indent(preview(last.body, *maxBody)))
				}
			}
		default:
			res.verdict = "OK"
			fmt.Printf("   OK (%s) · %s\n", round(elapsed), summary)
			if *jsonOut && payload != nil {
				fmt.Printf("%s\n", indent(jsonDump(payload)))
			}
		}
		results = append(results, res)
	}

	os.Exit(report(results))
}

// report imprime o resumo final e devolve o código de saída: 1 se qualquer
// teste falhou, 0 caso contrário. Um teste pulado não reprova a suíte — ele
// depende de outro que já reprovou, e reportá-lo como falha duplicaria a
// mesma causa em várias linhas.
func report(results []result) int {
	fmt.Printf("\n%s\n=== RESUMO ===\n", strings.Repeat("=", 76))
	failed := 0
	for _, r := range results {
		fmt.Printf("  %-7s %-9s %-52s %s\n", r.verdict, r.check.name, r.check.route, round(r.elapsed))
		if r.verdict == "FALHA" {
			failed++
		}
	}
	if failed > 0 {
		fmt.Printf("\n%d de %d teste(s) falharam.\n", failed, len(results))
		return 1
	}
	fmt.Printf("\nTodos os %d teste(s) passaram.\n", len(results))
	return 0
}

type result struct {
	check   check
	verdict string
	elapsed time.Duration
	summary string
	err     error
}

// state é o que os testes compartilham: o Client e o anúncio de referência
// que a busca encontrou. As três actions de detalhe não têm parâmetros
// próprios que se possa inventar — svrId, mapId e ssi identificam uma loja
// ABERTA AGORA no jogo, e um valor inventado responde "loja não encontrada",
// que não distingue rota quebrada de loja inexistente. Por isso o teste da
// busca roda primeiro e passa adiante o primeiro anúncio real que achar.
type state struct {
	client    *gnjoy.Client
	item      string
	server    string
	storeType gnjoy.StoreType
	period    string

	ref *gnjoy.ShopListItem
}

func (s *state) loc() gnjoy.StoreLocation {
	return gnjoy.StoreLocation{SvrId: s.ref.SvrId, MapId: s.ref.MapId, SSI: s.ref.SSI}
}

// needRef é a guarda das actions de detalhe: sem anúncio de referência não há
// o que consultar, e o teste é pulado em vez de inventar parâmetros.
func (s *state) needRef() error {
	if s.ref == nil {
		return skipf("depende do teste \"search\", que não devolveu nenhum anúncio para usar de referência")
	}
	return nil
}

type check struct {
	name  string
	route string
	run   func(ctx context.Context, st *state) (string, any, error)
}

// errSkip marca o teste que não chegou a rodar por falta de pré-requisito.
var errSkip = errors.New("pulado")

func skipf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errSkip, fmt.Sprintf(format, a...))
}

func checks() []check {
	return []check{
		{
			name:  "search",
			route: "GET  /{locale}/intro/shop-search/trading",
			run: func(ctx context.Context, st *state) (string, any, error) {
				res, err := st.client.SearchShops(ctx, gnjoy.SearchShopsParams{
					ServerType: st.server,
					StoreType:  st.storeType,
					SearchWord: st.item,
				})
				if err != nil {
					return "", nil, err
				}
				if len(res.Items) == 0 {
					// Rota sadia: o site respondeu no formato esperado. Só não
					// há anúncio deste item agora — as actions de detalhe, que
					// precisam de um, serão puladas.
					return fmt.Sprintf("rota OK, mas totalCount=%d: ninguém está anunciando %q em %s agora",
						res.TotalCount, st.item, st.server), res, nil
				}
				st.ref = &res.Items[0]
				return fmt.Sprintf("totalCount=%d · referência: %q %s x%d na loja %q (svrId=%d mapId=%d ssi=%s itemId=%d)",
					res.TotalCount, st.ref.DisplayName(), zeny(st.ref.ItemPrice), st.ref.ItemCnt,
					st.ref.StoreName, st.ref.SvrId, st.ref.MapId, st.ref.SSI, st.ref.ItemId), res, nil
			},
		},
		{
			name:  "market",
			route: "GET  /{locale}/intro/shop-search/market-price",
			run: func(ctx context.Context, st *state) (string, any, error) {
				res, err := st.client.SearchMarketPrice(ctx, gnjoy.MarketPriceParams{
					ServerType: st.server,
					SearchWord: st.item,
					Period:     st.period,
				})
				if err != nil {
					return "", nil, err
				}
				if len(res.Items) == 0 {
					return fmt.Sprintf("rota OK, mas totalCount=%d: nenhum preço praticado de %q no período %s",
						res.TotalCount, st.item, st.period), res, nil
				}
				first := res.Items[0]
				return fmt.Sprintf("totalCount=%d · %q: min %s / méd %s / máx %s, volume %d",
					res.TotalCount, first.ItemName, zeny(first.MinItemPrice), zeny(first.AvgItemPrice),
					zeny(first.MaxItemPrice), first.TotalItemCnt), res, nil
			},
		},
		{
			name:  "store",
			route: "POST action \"store\" (detalhe da loja)",
			run: func(ctx context.Context, st *state) (string, any, error) {
				if err := st.needRef(); err != nil {
					return "", nil, err
				}
				d, err := st.client.GetStoreDetail(ctx, st.loc())
				if err != nil {
					return "", nil, err
				}
				return fmt.Sprintf("%q de %s em %s (%s,%s) · %q %s x%d · refino=%d",
					d.StoreName, d.ItemSellerCharName, d.MapName, d.Xpos, d.Ypos,
					d.ItemFullName, zeny(d.ItemPrice), d.ItemCnt, d.Refine), d, nil
			},
		},
		{
			name:  "item",
			route: "POST action \"item\" (detalhe do item anunciado)",
			run: func(ctx context.Context, st *state) (string, any, error) {
				if err := st.needRef(); err != nil {
					return "", nil, err
				}
				d, err := st.client.GetItemDetail(ctx, st.loc(), itemLang)
				if err != nil {
					return "", nil, err
				}
				bonus := d.RandomOptions()
				if len(bonus) == 0 {
					bonus = []string{"(nenhum)"}
				}
				return fmt.Sprintf("%q (itemId=%d, tipo %q, noDatabase=%t) · bônus: %s",
					d.ItemName, d.ItemId, d.ItemType, d.HasDatabaseItem, strings.Join(bonus, " | ")), d, nil
			},
		},
		{
			name:  "price",
			route: "POST action \"price\" (histórico de preço)",
			run: func(ctx context.Context, st *state) (string, any, error) {
				if err := st.needRef(); err != nil {
					return "", nil, err
				}
				h, err := st.client.GetPriceHistory(ctx, gnjoy.PriceHistoryParams{
					ItemId: st.ref.ItemId,
					SvrId:  st.ref.SvrId,
				})
				if err != nil {
					return "", nil, err
				}
				last := "sem dias no histórico"
				if n := len(h.DayStatsList); n > 0 {
					d := h.DayStatsList[n-1]
					last = fmt.Sprintf("último dia %s: min %s / méd %s / máx %s (%d anúncios)",
						d.Date, zeny(d.MinItemPrice), zeny(d.AvgItemPrice), zeny(d.MaxItemPrice), d.ItemCnt)
				}
				return fmt.Sprintf("min %s / máx %s · %d ponto(s) de gráfico, %d dia(s) · %s",
					zeny(h.ItemPriceMin), zeny(h.ItemPriceMax), len(h.ChartList), len(h.DayStatsList), last), h, nil
			},
		},
		{
			name:  "ping",
			route: "POST action \"store\" vazia (aquecimento do action id)",
			run: func(ctx context.Context, st *state) (string, any, error) {
				if err := st.client.WarmupActionID(ctx); err != nil {
					return "", nil, err
				}
				// Sem payload: a resposta desta chamada é descartada de
				// propósito pelo motor (ver pingUpstream) — o que importa é o
				// site ter respondido, não o que respondeu.
				return "o site aceitou o action id em vigor (é isto que a página inicial faz ao abrir)", nil, nil
			},
		},
		{
			name:  "discover",
			route: "GET  página HTML + /_next/static/chunks/*.js",
			run: func(ctx context.Context, st *state) (string, any, error) {
				id, err := st.client.RefreshActionID(ctx)
				if err != nil {
					return "", nil, err
				}
				if id == gnjoy.DefaultActionID {
					return fmt.Sprintf("action id publicado pelo site: %s — igual ao DefaultActionID embutido", id), id, nil
				}
				return fmt.Sprintf("action id publicado pelo site: %s — DIFERENTE do DefaultActionID embutido (%s); "+
					"o site teve deploy desde a captura da constante, e a redescoberta automática é o que segura isso",
					id, gnjoy.DefaultActionID), id, nil
			},
		},
	}
}

func selectChecks(only string) ([]check, error) {
	all := checks()
	if strings.TrimSpace(only) == "" {
		return all, nil
	}
	byName := make(map[string]check, len(all))
	names := make([]string, 0, len(all))
	for _, c := range all {
		byName[c.name] = c
		names = append(names, c.name)
	}
	var selected []check
	for _, want := range strings.Split(only, ",") {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		c, ok := byName[want]
		if !ok {
			return nil, fmt.Errorf("teste %q não existe; os disponíveis são: %s", want, strings.Join(names, ", "))
		}
		selected = append(selected, c)
	}
	if len(selected) == 0 {
		return nil, errors.New("-only não selecionou nenhum teste")
	}
	return selected, nil
}

func baseOrDefault() string {
	if v := os.Getenv("GNJOY_BASE_URL"); v != "" {
		return v
	}
	return gnjoy.DefaultBaseURL
}

// dumpTransport registra cada requisição que o Client dispara junto da
// resposta correspondente, repondo o corpo em seguida para que o Client o leia
// normalmente — a sonda observa o tráfego, não o substitui. Com raw ligado,
// despeja tudo no terminal na hora.
//
// Fica no transporte, e não em volta de cada método, porque é o único ponto
// que enxerga a requisição já montada pelo pacote (cabeçalhos RSC, user-agent,
// next-action, next-router-state-tree) e TODAS as requisições de um teste,
// inclusive as repetidas após um 429 e as da redescoberta automática do action
// id — que é justamente o que se quer flagrar quando uma action falha.
type dumpTransport struct {
	base http.RoundTripper
	raw  bool

	mu    sync.Mutex
	calls []*wireCall
}

// wireCall é uma requisição registrada, com o bastante para explicar o que
// aconteceu sem precisar do corpo inteiro na tela.
type wireCall struct {
	n      int
	method string
	uri    string
	action string // cabeçalho next-action, presente só nos POSTs de Server Action
	status int
	dur    time.Duration
	size   int
	body   []byte // truncado em maxStoredBody
	err    error
}

// maxStoredBody limita o que a sonda guarda de cada corpo: um chunk JS do
// Next.js passa de 500 KB, e a suíte inteira varre dezenas deles.
const maxStoredBody = 64 << 10

func (c *wireCall) line() string {
	head := fmt.Sprintf("#%d %s %s", c.n, c.method, elide(c.uri, 96))
	if c.action != "" {
		head += fmt.Sprintf(" [next-action %s]", c.action)
	}
	if c.err != nil {
		return fmt.Sprintf("%s -> ERRO DE REDE (%s): %v", head, round(c.dur), c.err)
	}
	return fmt.Sprintf("%s -> %d (%s, %d bytes)", head, c.status, round(c.dur), c.size)
}

func (t *dumpTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	call := &wireCall{
		method: req.Method,
		uri:    req.URL.RequestURI(),
		action: req.Header.Get("next-action"),
	}
	t.mu.Lock()
	t.calls = append(t.calls, call)
	call.n = len(t.calls)
	t.mu.Unlock()

	if t.raw {
		// DumpRequestOut com body=false: as buscas são GET, e o corpo dos
		// POSTs de action é o payload curto já descrito por -only/params.
		if dump, err := httputil.DumpRequestOut(req, false); err == nil {
			fmt.Printf("\n=== REQUISIÇÃO #%d ===\n%s\n", call.n, strings.TrimRight(string(dump), "\r\n"))
		} else {
			fmt.Printf("\n=== REQUISIÇÃO #%d ===\n%s %s (falha ao despejar cabeçalhos: %v)\n", call.n, req.Method, req.URL, err)
		}
	}

	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		call.dur = time.Since(start)
		call.err = err
		return nil, err
	}

	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		call.dur = time.Since(start)
		call.err = readErr
		return nil, readErr
	}
	// Repõe o corpo consumido: daqui para frente o Client lê como se nada
	// tivesse acontecido.
	resp.Body = io.NopCloser(bytes.NewReader(body))

	call.dur = time.Since(start)
	call.status = resp.StatusCode
	call.size = len(body)
	call.body = body
	if len(call.body) > maxStoredBody {
		call.body = call.body[:maxStoredBody]
	}

	if t.raw {
		fmt.Printf("\n=== RESPOSTA #%d (%s) ===\n%s\n", call.n, round(call.dur), resp.Status)
		for _, name := range sortedHeaderNames(resp.Header) {
			for _, v := range resp.Header[name] {
				fmt.Printf("%s: %s\n", name, v)
			}
		}
		fmt.Printf("\n=== CORPO CRU #%d (%d bytes) ===\n", call.n, len(body))
		os.Stdout.Write(body)
		if len(body) > 0 && !bytes.HasSuffix(body, []byte("\n")) {
			fmt.Println()
		}
		fmt.Printf("=== FIM DO CORPO CRU #%d ===\n", call.n)
	}

	return resp, nil
}

func (t *dumpTransport) len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.calls)
}

// since devolve as requisições registradas depois de from — as de um teste.
func (t *dumpTransport) since(from int) []*wireCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*wireCall(nil), t.calls[from:]...)
}

func lastWithBody(calls []*wireCall) *wireCall {
	for i := len(calls) - 1; i >= 0; i-- {
		if len(calls[i].body) > 0 {
			return calls[i]
		}
	}
	return nil
}

func sortedHeaderNames(h http.Header) []string {
	names := make([]string, 0, len(h))
	for name := range h {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func preview(body []byte, max int) string {
	if max > 0 && len(body) > max {
		return string(body[:max]) + fmt.Sprintf("\n[... %d bytes omitidos ...]", len(body)-max)
	}
	return string(body)
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = "   " + line
	}
	return strings.Join(lines, "\n")
}

func elide(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func round(d time.Duration) time.Duration {
	return d.Round(time.Millisecond)
}

// zeny formata um preço como o jogo mostra: milhar separado por ponto.
func zeny(v int64) string {
	s := fmt.Sprintf("%d", v)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out) + "z"
	}
	return string(out) + "z"
}

// jsonDump serve para inspecionar uma struct decodificada por inteiro quando o
// resumo de uma linha não basta.
func jsonDump(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("(falha ao serializar: %v)", err)
	}
	return string(b)
}
