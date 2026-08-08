package gnjoy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// DefaultBaseURL é o domínio do site GnJoy LATAM do Ragnarok Online.
	DefaultBaseURL = "https://ro.gnjoylatam.com"

	// DefaultLocale é o idioma padrão usado nas rotas ("pt", "en" ou "es").
	DefaultLocale = "pt"

	// shopSearchPath é o caminho (relativo ao locale) da seção de busca de
	// mercado do site. Ela tem duas páginas, e cada uma responde a uma
	// pergunta diferente: tradingPageID lista o que está anunciado AGORA,
	// marketPricePageID resume por quanto cada item ANDOU sendo vendido.
	shopSearchPath = "/intro/shop-search"

	// tradingPageID é a página de lojas de comércio, usada tanto pela busca
	// em si (GET) quanto pelas Server Actions de detalhe (POST).
	tradingPageID = "trading"

	// marketPricePageID é a página de preços de mercado, que resume o
	// histórico de vendas de cada item que casa com o termo buscado.
	marketPricePageID = "market-price"

	tradingPath     = shopSearchPath + "/" + tradingPageID
	marketPricePath = shopSearchPath + "/" + marketPricePageID

	// DefaultActionID é o identificador da Next.js Server Action usado
	// pelas rotas de detalhe de loja/item/histórico de preço, capturado a
	// partir do tráfego real do site. É usado apenas como um "chute
	// inicial" para evitar uma requisição extra de descoberta logo na
	// primeira chamada.
	//
	// Next.js recalcula esse hash a cada novo deploy do site, então ele
	// FICARÁ desatualizado eventualmente. Quando isso acontece, o Client
	// detecta a falha sozinho e redescobre o hash atual automaticamente
	// (veja discover.go) — não é necessário atualizar esta constante nem
	// reiniciar o processo.
	DefaultActionID = "40a3f7a2ade1ce8f0b65438f43a533e65968363fe9"

	// DefaultRateLimitRPS e DefaultRateLimitBurst controlam o ritmo padrão
	// de requisições enviadas ao GnJoy LATAM. O site tem um rate limiter
	// que responde 429 quando ultrapassado, mas seus parâmetros exatos não
	// são conhecidos publicamente — por isso o padrão aqui é
	// deliberadamente conservador (uma requisição por segundo, sem rajada)
	// em vez de tentar descobrir o limite real por tentativa e erro.
	// Ajuste via WithRateLimit se, na prática, o site permitir mais.
	DefaultRateLimitRPS   = 1.0
	DefaultRateLimitBurst = 1

	// maxTooManyRequestsRetries é quantas vezes uma requisição é refeita
	// após um 429, antes de desistir e propagar o erro. É o padrão por
	// chamada; NoRetry desliga a insistência para chamadas de fundo.
	maxTooManyRequestsRetries = 5

	// maxRetryAfter limita o tempo de espera por tentativa mesmo que o
	// upstream mande um "Retry-After" maior, para não travar uma
	// requisição indefinidamente por causa de um valor anômalo.
	maxRetryAfter = 2 * time.Minute

	// activityLogCapacity é quantas chamadas recentes o ActivityLog do
	// Client mantém em memória para a barra de atividades do frontend.
	activityLogCapacity = 200
)

// Client consulta as rotas internas do site do GnJoy LATAM usadas pela
// página de busca de mercado.
//
// Essas rotas não são uma API pública documentada: são simplesmente o que o
// navegador chama internamente ao navegar pela página em Next.js. Elas podem
// mudar de formato, exigir novos cabeçalhos ou parar de funcionar sem aviso
// a qualquer momento.
type Client struct {
	baseURL    string
	locale     string
	userAgent  string
	httpClient *http.Client

	// limiter regula o ritmo de TODAS as requisições enviadas ao upstream
	// (busca, actions de detalhe e a descoberta de action id), formando
	// uma fila com atraso em vez de disparar tudo de uma vez — o objetivo
	// é nunca esbarrar no rate limiter do site.
	limiter *rate.Limiter

	// activity registra o histórico de chamadas ao upstream (fila do rate
	// limiter, em voo, sucesso/erro) para quem quiser observar a atividade
	// do Client em tempo real — ver Activity() e internal/web/activity.go.
	activity *ActivityLog

	// cooldownMu protege cooldownUntil: o instante até o qual TODAS as
	// requisições seguram o disparo por causa de um 429 recente (ver
	// extendCooldown). Zero significa "sem calmaria em vigor".
	cooldownMu    sync.Mutex
	cooldownUntil time.Time

	// suspension é a segunda camada de defesa contra o 429, acima da
	// calmaria: enquanto suspensa, nenhuma chamada sai (ver suspend.go).
	// suspendOn429 diz se um 429 chega a alimentá-la — sem a option, o
	// Client insiste como sempre insistiu.
	suspension   *SuspensionState
	suspendOn429 bool

	// dumpActions registra no log a resposta crua de cada Server Action, para
	// inspecionar o que o site manda além do que este pacote decodifica —
	// ver WithActionDump.
	dumpActions bool

	actionIDMu sync.RWMutex
	actionID   string

	// discoverMu serializa as tentativas de redescoberta do action id, para
	// que várias chamadas falhando ao mesmo tempo (por causa de um deploy
	// no site) não disparem N requisições de descoberta em paralelo.
	discoverMu sync.Mutex
}

// Option customiza a construção de um Client.
type Option func(*Client)

func WithBaseURL(baseURL string) Option {
	return func(c *Client) { c.baseURL = baseURL }
}

// WithLocale define o idioma das rotas ("pt", "en" ou "es").
func WithLocale(locale string) Option {
	return func(c *Client) { c.locale = locale }
}

// WithActionID sobrescreve o identificador da Server Action do Next.js.
// Veja DefaultActionID.
func WithActionID(actionID string) Option {
	return func(c *Client) { c.actionID = actionID }
}

func WithUserAgent(userAgent string) Option {
	return func(c *Client) { c.userAgent = userAgent }
}

func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// WithRateLimit define o ritmo máximo de requisições enviadas ao upstream:
// rps requisições por segundo em regime permanente, com uma rajada inicial
// de até burst requisições. Veja DefaultRateLimitRPS/DefaultRateLimitBurst.
func WithRateLimit(rps float64, burst int) Option {
	return func(c *Client) { c.limiter = rate.NewLimiter(rate.Limit(rps), burst) }
}

// WithActionDump registra, no log, o JSON CRU que cada Server Action devolve,
// antes de ele ser decodificado nas structs deste pacote.
//
// Existe porque a decodificação é silenciosa quanto ao que não conhece: os
// campos são lidos com json.Unmarshal sem DisallowUnknownFields, então um
// campo que o site passe a mandar — ou que nunca tenhamos reparado — some sem
// deixar rastro. Quando surge a pergunta "o site expõe X?", esta é a única
// forma de responder olhando o dado real em vez do que já mapeamos.
//
// Fica desligada por padrão e atrás de uma option própria (e não do nível de
// log) porque o volume é alto e o conteúdo é a resposta inteira do upstream.
func WithActionDump() Option {
	return func(c *Client) { c.dumpActions = true }
}

// WithSuspendOn429 faz o primeiro 429 suspender TODAS as consultas até o site
// voltar a responder (ver suspend.go), em vez de apenas registrar a calmaria e
// insistir. A partir daí só ProbeUpstream atravessa, e é a resposta dela que
// reabre.
//
// É opt-in porque muda o contrato para quem usa o pacote como biblioteca:
// desligada, o Client continua com o retry com backoff de sempre, que é o
// comportamento certo quando quem chama trata o erro por conta própria. O
// binário a liga (ver cmd/server/main.go), porque lá há uma tela para avisar o
// usuário e uma sonda para reabrir.
func WithSuspendOn429() Option {
	return func(c *Client) { c.suspendOn429 = true }
}

// CallOption ajusta o comportamento de UMA chamada específica, sem afetar o
// Client — o complemento por chamada das Options de construção. É aceita
// pelos métodos públicos de consulta (SearchShops, GetStoreDetail etc.).
type CallOption func(*callConfig)

// callConfig é o comportamento efetivo de uma chamada depois de aplicadas as
// CallOptions.
type callConfig struct {
	// maxRetries é quantas vezes a chamada é refeita após um 429 antes de
	// desistir e propagar o erro.
	maxRetries int

	// allowWhileSuspended deixa a chamada atravessar a suspensão. Não tem
	// CallOption pública de propósito: só ProbeUpstream o liga, porque só
	// ela existe para descobrir se o site voltou (ver suspend.go).
	allowWhileSuspended bool
}

func newCallConfig(opts []CallOption) callConfig {
	cfg := callConfig{maxRetries: maxTooManyRequestsRetries}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// NoRetry faz a chamada desistir no primeiro 429, em vez de aguardar e tentar
// de novo. É para chamadas de fundo que têm repetição própria (a checagem
// periódica da watchlist, o aquecimento do action id): quando o site está
// pedindo calma, insistir por elas não traz benefício — o ciclo seguinte
// refaz a consulta de qualquer forma — e só disputa a cota com as ações que o
// usuário está esperando. O 429 recebido ainda estabelece a calmaria global
// (ver extendCooldown); esta opção controla apenas a insistência da própria
// chamada.
func NoRetry() CallOption {
	return func(cfg *callConfig) { cfg.maxRetries = 0 }
}

// New cria um Client pronto para uso com os valores padrão, aplicando as
// opções informadas.
func New(opts ...Option) *Client {
	c := &Client{
		baseURL:  DefaultBaseURL,
		locale:   DefaultLocale,
		actionID: DefaultActionID,
		userAgent: "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
			"(KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36",
		httpClient: &http.Client{Timeout: 15 * time.Second},
		limiter:    rate.NewLimiter(rate.Limit(DefaultRateLimitRPS), DefaultRateLimitBurst),
		activity:   NewActivityLog(activityLogCapacity),
		suspension: newSuspensionState(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Activity devolve o log de atividade do Client — histórico das chamadas
// mais recentes ao upstream e um jeito de assinar atualizações em tempo
// real. Usado pela barra de atividades do frontend (ver
// internal/web/activity.go).
func (c *Client) Activity() *ActivityLog {
	return c.activity
}

// HTTPError é retornado quando o upstream responde com um status HTTP
// diferente de 200.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("gnjoy: resposta inesperada do upstream: status %d: %s", e.StatusCode, e.Body)
}

// ErrActionFailed é devolvido quando uma Server Action foi aceita pelo site
// (ou seja, o action id era válido) mas respondeu "success: false" — o
// equivalente, do lado do Next.js, a um pedido que não achou o que
// procurava (ex.: uma loja ou item que já não existe mais). Ao contrário de
// um action id desatualizado, isso não é sinal de nada errado com o id.
var ErrActionFailed = errors.New("gnjoy: action reportou falha")

// do executa req respeitando o rate limiter e a calmaria global do Client —
// se necessário, a chamada fica bloqueada em fila até ser a vez dela (ver
// awaitTurn) — e, caso o upstream mesmo assim responda 429, registra a
// calmaria para todas as chamadas (extendCooldown) e tenta de novo, até
// cfg.maxRetries vezes.
//
// labels são as descrições amigáveis da chamada, uma por fase do ciclo de
// vida (ver activityLabels) — usadas só para popular o ActivityLog do
// Client, não afetam a requisição em si. Todo o ciclo de vida da chamada
// (fila do rate limiter, em voo, sucesso/erro/retry) é publicado lá para
// quem estiver observando (a barra de atividades do frontend).
func (c *Client) do(req *http.Request, labels activityLabels, cfg callConfig) ([]byte, error) {
	req.Header.Set("user-agent", c.userAgent)

	// Recusa antes de abrir a atividade: uma varredura de trinta anúncios
	// contra uma porta fechada despejaria trinta linhas vermelhas na barra e
	// evictaria o histórico útil, que é justamente onde está o 429 que
	// explica tudo.
	gen, err := c.suspension.admit(cfg.allowWhileSuspended)
	if err != nil {
		return nil, err
	}

	handle := c.activity.begin(labels)

	var lastErr error
	for attempt := 0; attempt <= cfg.maxRetries; attempt++ {
		// De novo a cada tentativa: um 429 de outra chamada pode ter fechado
		// a porta enquanto esta esperava para insistir, e insistir agora seria
		// gastar cota contra um bloqueio já conhecido.
		if attempt > 0 {
			if gen, err = c.suspension.admit(cfg.allowWhileSuspended); err != nil {
				handle.fail(err)
				return nil, err
			}
		}

		if attempt > 0 && req.Body != nil {
			// Requisições sem corpo (GET) não precisam de nada aqui; as
			// com corpo (POST) precisam recriá-lo, já que ele já foi
			// consumido na tentativa anterior.
			if req.GetBody == nil {
				err := fmt.Errorf("gnjoy: upstream retornou 429 e a requisição não pode ser refeita (sem corpo reaproveitável): %w", lastErr)
				handle.fail(err)
				return nil, err
			}
			body, err := req.GetBody()
			if err != nil {
				err = fmt.Errorf("gnjoy: recriando corpo da requisição para nova tentativa: %w", err)
				handle.fail(err)
				return nil, err
			}
			req.Body = io.NopCloser(body)
		}

		if err := c.awaitTurn(req.Context(), handle, cfg); err != nil {
			handle.fail(err)
			return nil, err
		}

		handle.running()
		resp, err := c.httpClient.Do(req)
		if err != nil {
			err = fmt.Errorf("gnjoy: executando requisição: %w", err)
			handle.fail(err)
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if err != nil {
			err = fmt.Errorf("gnjoy: lendo corpo da resposta: %w", err)
			handle.fail(err)
			return nil, err
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			lastErr = &HTTPError{StatusCode: resp.StatusCode, Body: truncateBody(body)}
			wait := retryAfterDuration(resp.Header.Get("Retry-After"), attempt)
			// O 429 é do site inteiro, não desta chamada: a espera vira uma
			// calmaria global, e as demais chamadas enfileiradas param de
			// gastar as próprias tentativas contra um bloqueio já conhecido.
			c.extendCooldown(time.Now().Add(wait))
			if c.suspendOn429 {
				// Além da calmaria: o 429 pode ser a cota do dia acabando, e
				// aí o site não diz quando volta. Fecha a porta e deixa a
				// sonda descobrir (ver suspend.go).
				c.suspension.suspend(time.Now())
			}
			if attempt == cfg.maxRetries {
				break
			}
			slog.Warn("gnjoy: upstream respondeu 429, aguardando antes de tentar de novo",
				"path", req.URL.Path, "tentativa", attempt+1, "espera", wait)
			// A espera em si acontece em awaitTurn, na volta do laço: é lá
			// que a calmaria recém-registrada (desta ou de qualquer outra
			// chamada) é respeitada antes do próximo disparo.
			continue
		}

		// O site respondeu algo que não é 429 — 200, 404, 500, tanto faz. Ele
		// está atendendo, e é isso que a suspensão espera para reabrir. A
		// guarda de geração impede que uma resposta admitida ANTES do 429, e
		// que só chegou agora, desfaça uma suspensão mais nova que ela.
		c.suspension.release(gen)

		if resp.StatusCode != http.StatusOK {
			err := &HTTPError{StatusCode: resp.StatusCode, Body: truncateBody(body)}
			handle.fail(err)
			return nil, err
		}
		handle.succeed()
		return body, nil
	}
	err = fmt.Errorf("gnjoy: upstream retornou 429 em todas as %d tentativa(s): %w", cfg.maxRetries+1, lastErr)
	handle.fail(err)
	return nil, err
}

// awaitTurn segura a chamada até ser a vez dela disparar: primeiro aguarda o
// fim de qualquer calmaria global em vigor (ver extendCooldown), depois a
// vaga no rate limiter — e reconfere a calmaria ao final, porque um 429 de
// outra chamada pode tê-la estabelecido (ou estendido) enquanto esta esperava
// a vaga. Nesse caso a vaga é devolvida e a espera recomeça, com uma vaga
// nova ao final: é isso que faz as chamadas represadas saírem espaçadas pelo
// limiter quando a calmaria acaba, em vez de todas de uma vez.
func (c *Client) awaitTurn(ctx context.Context, handle *activityHandle, cfg callConfig) error {
	for {
		// A suspensão é reconferida junto da calmaria, e pelo mesmo motivo:
		// uma chamada estacionada numa calmaria de dois minutos dispararia
		// assim que ela acabasse, mesmo já suspensa — e é exatamente esse
		// tráfego represado que a suspensão existe para impedir.
		if _, err := c.suspension.admit(cfg.allowWhileSuspended); err != nil {
			return err
		}

		if wait := time.Until(c.cooldownDeadline()); wait > 0 {
			handle.waiting(time.Now().Add(wait))
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}

		now := time.Now()
		reservation := c.limiter.ReserveN(now, 1)
		delay := reservation.DelayFrom(now)
		if delay > 0 {
			handle.waiting(now.Add(delay))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				reservation.Cancel()
				return ctx.Err()
			}
		}

		if time.Until(c.cooldownDeadline()) > 0 {
			reservation.Cancel()
			continue
		}
		return nil
	}
}

// cooldownDeadline devolve até quando as chamadas devem segurar o disparo por
// causa de um 429 recente — o zero de time.Time (sempre no passado) quando
// não há calmaria em vigor.
func (c *Client) cooldownDeadline() time.Time {
	c.cooldownMu.Lock()
	defer c.cooldownMu.Unlock()
	return c.cooldownUntil
}

// extendCooldown registra que o site pediu calma: até o instante informado,
// nenhuma chamada dispara (todas aguardam em awaitTurn). Sem isso, cada
// chamada já enfileirada só descobriria o bloqueio tomando o próprio 429,
// gastando tentativas e cota exatamente enquanto o site está recusando.
// Estender nunca encurta uma calmaria já registrada.
func (c *Client) extendCooldown(until time.Time) {
	c.cooldownMu.Lock()
	if until.After(c.cooldownUntil) {
		c.cooldownUntil = until
	}
	c.cooldownMu.Unlock()
}

func truncateBody(body []byte) string {
	const maxLen = 500
	if len(body) > maxLen {
		body = body[:maxLen]
	}
	return string(body)
}

// truncateDump corta o dump da resposta crua de uma action (ver
// WithActionDump). O teto é muito maior que o de truncateBody porque os dois
// servem a coisas opostas: aquele resume o corpo de um erro em uma linha de
// log, este existe para o payload chegar INTEIRO — cortar em 500 bytes
// esconderia justamente os campos do fim do objeto, que são o motivo de
// alguém ligar o dump. O teto continua existindo só para uma resposta
// inesperadamente enorme não afogar o log.
func truncateDump(data []byte) string {
	const maxLen = 8 << 10
	if len(data) > maxLen {
		return string(data[:maxLen]) + "…(cortado)"
	}
	return string(data)
}

// retryAfterDuration decide quanto esperar antes de repetir uma requisição
// que recebeu 429, priorizando o cabeçalho "Retry-After" enviado pelo
// upstream (em segundos ou como data HTTP) e caindo para um backoff
// exponencial com teto quando o cabeçalho não vem ou é inválido.
func retryAfterDuration(retryAfter string, attempt int) time.Duration {
	if retryAfter != "" {
		if secs, err := strconv.Atoi(retryAfter); err == nil && secs >= 0 {
			return capDuration(time.Duration(secs) * time.Second)
		}
		if when, err := http.ParseTime(retryAfter); err == nil {
			if d := time.Until(when); d > 0 {
				return capDuration(d)
			}
		}
	}
	backoff := time.Second * time.Duration(1<<uint(attempt))
	return capDuration(backoff)
}

func capDuration(d time.Duration) time.Duration {
	if d > maxRetryAfter {
		return maxRetryAfter
	}
	if d < 0 {
		return 0
	}
	return d
}

func (c *Client) pageURL(path string) string {
	return c.baseURL + "/" + c.locale + path
}

func (c *Client) currentActionID() string {
	c.actionIDMu.RLock()
	defer c.actionIDMu.RUnlock()
	return c.actionID
}

func (c *Client) setActionID(id string) {
	c.actionIDMu.Lock()
	c.actionID = id
	c.actionIDMu.Unlock()
}

// routerStateTree monta (de forma best-effort) o cabeçalho
// "Next-Router-State-Tree" que o navegador envia ao navegar dentro da seção
// de busca de mercado. O formato é interno do Next.js e não documentado
// oficialmente; o objetivo aqui não é reproduzi-lo byte a byte, e sim
// enviar uma árvore de rotas plausível o bastante para o servidor aceitar a
// requisição como uma navegação para a página pageID (que é o segmento "id"
// da rota, ver tradingPageID e marketPricePageID).
func (c *Client) routerStateTree(pageID string) string {
	leaf := []any{"__PAGE__", map[string]any{}, shopSearchPath + "/" + pageID, "refresh"}
	idNode := []any{[]any{"id", pageID, "d"}, map[string]any{"children": leaf}, nil, nil}
	shopSearchNode := []any{"shop-search", map[string]any{"children": idNode}, nil, nil}
	introNode := []any{"intro", map[string]any{"children": shopSearchNode}, nil, nil}
	primaryNode := []any{"(primary)", map[string]any{"children": introNode}, nil, nil}
	localeTuple := []any{[]any{"locale", c.locale, "d"}, map[string]any{"children": primaryNode}, nil, nil}
	root := []any{"", map[string]any{"children": localeTuple}, nil, nil, true}

	b, err := json.Marshal(root)
	if err != nil {
		// Não deve acontecer: a estrutura acima é sempre serializável.
		return ""
	}
	return url.QueryEscape(string(b))
}

// SearchShopsParams são os filtros aceitos pela busca de lojas de comércio.
type SearchShopsParams struct {
	// ServerType é o nome do servidor (ex.: "NIDHOGG").
	ServerType string
	// StoreType filtra por tipo de negociação (compra ou venda).
	StoreType StoreType
	// SearchWord é o nome (ou parte do nome) do item procurado.
	SearchWord string
}

// SearchShops busca as lojas de comércio que estão comprando ou vendendo um
// item pelo nome, em um servidor específico. Equivale a digitar um item na
// busca da página "/intro/shop-search/trading".
//
// Um termo com pontuação (hífen, "+", parênteses, colchetes...) é contornado
// antes de ir ao upstream, que só aceita letras, dígitos e espaços; veja
// splitSearchWord.
func (c *Client) SearchShops(ctx context.Context, p SearchShopsParams, opts ...CallOption) (*ShopSearchResult, error) {
	send, filter := splitSearchWord(p.SearchWord)
	if filter != "" && send == "" {
		// Termo feito só de hífens: não há trecho a procurar, e mandar vazio
		// devolveria o mercado inteiro. Nenhum item se chama assim.
		return &ShopSearchResult{Items: []ShopListItem{}}, nil
	}

	q := url.Values{}
	q.Set("storeType", string(p.StoreType))
	q.Set("serverType", p.ServerType)
	q.Set("searchWord", send)

	reqURL := c.pageURL(tradingPath) + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gnjoy: montando requisição de busca: %w", err)
	}
	req.Header.Set("accept", "*/*")
	req.Header.Set("rsc", "1")
	req.Header.Set("next-url", "/"+c.locale+tradingPath)
	req.Header.Set("next-router-state-tree", c.routerStateTree(tradingPageID))
	req.Header.Set("referer", c.pageURL(tradingPath))

	labels := activityLabels{
		InProgress: fmt.Sprintf("Buscando %q no servidor %s", p.SearchWord, p.ServerType),
		Success:    fmt.Sprintf("Busca por %q no servidor %s concluída", p.SearchWord, p.ServerType),
		Error:      fmt.Sprintf("Falha ao buscar %q no servidor %s", p.SearchWord, p.ServerType),
	}
	body, err := c.do(req, labels, newCallConfig(opts))
	if err != nil {
		return nil, err
	}

	obj, err := parseFlightObject(body, "list", "totalCount")
	if err != nil {
		return nil, fmt.Errorf("gnjoy: interpretando resposta da busca por %q (o site respondeu sem a lista de resultados — provavelmente a página de erro dele): %w", send, err)
	}
	result := &ShopSearchResult{}
	if err := decodeInto(obj, result); err != nil {
		return nil, err
	}

	if filter != "" {
		// A busca enviada foi mais ampla que a pedida (ver splitSearchWord);
		// só ficam os itens que casariam com o termo inteiro.
		items := make([]ShopListItem, 0, len(result.Items))
		for _, item := range result.Items {
			// O nome com slots entra no casamento porque é o que o site
			// mostra: quem procura "Selo de Loki [1]" digitou o que viu.
			if matchesSearchWord(filter, item.ItemName, item.DisplayName()) {
				items = append(items, item)
			}
		}
		result.Items = items
		result.TotalCount = len(items)
	}
	return result, nil
}

// Períodos aceitos pela busca de preços de mercado, os mesmos oferecidos no
// seletor do site. Um valor fora desta lista faz o site devolver uma lista
// vazia, e não um erro — então não dá para distinguir "período inválido" de
// "item nunca vendido".
const (
	// MarketPricePeriodAll é o padrão do site: todo o histórico conhecido.
	MarketPricePeriodAll   = "ALL"
	MarketPricePeriodDay   = "1"
	MarketPricePeriodWeek  = "7"
	MarketPricePeriodMonth = "30"
)

// MarketPriceParams são os filtros aceitos pela busca de preços de mercado.
type MarketPriceParams struct {
	// ServerType é o nome do servidor (ex.: "NIDHOGG").
	ServerType string
	// SearchWord é o nome (ou parte do nome) do item procurado.
	SearchWord string
	// Period é a janela do resumo (ver MarketPricePeriod*). Se vazio, o site
	// usa MarketPricePeriodAll.
	Period string
}

// SearchMarketPrice busca, pelo nome do item, o resumo de preços praticados
// no mercado de um servidor: mínimo, médio, máximo e volume negociado no
// período, já agregados pelo próprio site.
//
// Ao contrário de SearchShops, que só enxerga o que está anunciado neste
// instante, esta busca enxerga o que já foi vendido — é o que responde
// "quanto esse item costuma custar?" para um item que ninguém está
// anunciando agora. Equivale a usar a página "/intro/shop-search/market-price".
//
// Assim como SearchShops, contorna a pontuação que o upstream não aceita;
// veja splitSearchWord.
func (c *Client) SearchMarketPrice(ctx context.Context, p MarketPriceParams, opts ...CallOption) (*MarketPriceResult, error) {
	send, filter := splitSearchWord(p.SearchWord)
	if filter != "" && send == "" {
		return &MarketPriceResult{Items: []MarketPriceItem{}}, nil
	}

	q := url.Values{}
	q.Set("serverType", p.ServerType)
	q.Set("searchWord", send)
	if p.Period != "" {
		q.Set("period", p.Period)
	}

	reqURL := c.pageURL(marketPricePath) + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gnjoy: montando requisição de preços de mercado: %w", err)
	}
	req.Header.Set("accept", "*/*")
	req.Header.Set("rsc", "1")
	req.Header.Set("next-url", "/"+c.locale+marketPricePath)
	req.Header.Set("next-router-state-tree", c.routerStateTree(marketPricePageID))
	req.Header.Set("referer", c.pageURL(marketPricePath))

	labels := activityLabels{
		InProgress: fmt.Sprintf("Consultando preços praticados de %q no servidor %s", p.SearchWord, p.ServerType),
		Success:    fmt.Sprintf("Preços praticados de %q no servidor %s consultados", p.SearchWord, p.ServerType),
		Error:      fmt.Sprintf("Falha ao consultar preços praticados de %q no servidor %s", p.SearchWord, p.ServerType),
	}
	body, err := c.do(req, labels, newCallConfig(opts))
	if err != nil {
		return nil, err
	}

	obj, err := parseFlightObject(body, "list", "totalCount")
	if err != nil {
		return nil, fmt.Errorf("gnjoy: interpretando resposta de preços de mercado para %q (o site respondeu sem a lista de resultados — provavelmente a página de erro dele): %w", send, err)
	}
	result := &MarketPriceResult{}
	if err := decodeInto(obj, result); err != nil {
		return nil, err
	}

	if filter != "" {
		items := make([]MarketPriceItem, 0, len(result.Items))
		for _, item := range result.Items {
			if matchesSearchWord(filter, item.ItemName) {
				items = append(items, item)
			}
		}
		result.Items = items
		result.TotalCount = len(items)
	}
	return result, nil
}

type actionRequest struct {
	Type   string `json:"type"`
	Params any    `json:"params"`
}

type actionEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

// callAction invoca a Server Action interna do Next.js usada pelas rotas de
// detalhe (loja, item e histórico de preço), todas multiplexadas pelo mesmo
// endpoint via o campo "type" do payload.
//
// Se a chamada falhar de um jeito consistente com o action id estar
// desatualizado (o site teve um novo deploy), o id atual é redescoberto
// automaticamente a partir dos chunks JS da página e a chamada é refeita
// uma única vez.
func (c *Client) callAction(ctx context.Context, actionType string, params any, out any, labels activityLabels, cfg callConfig) error {
	actionID := c.currentActionID()

	err := c.callActionWithID(ctx, actionType, params, out, actionID, labels, cfg)
	if err == nil || !isStaleActionIDErr(err) {
		return err
	}

	newID, derr := c.RefreshActionID(ctx)
	if derr != nil {
		return fmt.Errorf("gnjoy: action %q falhou (%v); redescoberta do action id também falhou: %w", actionType, err, derr)
	}
	if newID == actionID {
		// Já era o id atual (outra goroutine redescobriu antes de nós, ou
		// a falha não era mesmo sobre o action id estar desatualizado).
		return err
	}
	return c.callActionWithID(ctx, actionType, params, out, newID, labels, cfg)
}

func (c *Client) callActionWithID(ctx context.Context, actionType string, params any, out any, actionID string, labels activityLabels, cfg callConfig) error {
	payload, err := json.Marshal([]actionRequest{{Type: actionType, Params: params}})
	if err != nil {
		return fmt.Errorf("gnjoy: montando payload da action %q: %w", actionType, err)
	}

	reqURL := c.pageURL(tradingPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("gnjoy: montando requisição da action %q: %w", actionType, err)
	}
	req.Header.Set("accept", "text/x-component")
	req.Header.Set("content-type", "text/plain;charset=UTF-8")
	req.Header.Set("next-action", actionID)
	req.Header.Set("next-router-state-tree", c.routerStateTree(tradingPageID))
	req.Header.Set("origin", c.baseURL)
	req.Header.Set("referer", reqURL)

	body, err := c.do(req, labels, cfg)
	if err != nil {
		return err
	}

	obj, err := parseFlightObject(body, "data", "success")
	if err != nil {
		return fmt.Errorf("gnjoy: interpretando resposta da action %q: %w", actionType, err)
	}
	var env actionEnvelope
	if err := decodeInto(obj, &env); err != nil {
		return err
	}
	if !env.Success {
		return fmt.Errorf("gnjoy: action %q reportou falha: %w", actionType, ErrActionFailed)
	}
	// Antes de decodificar, e não depois: o objetivo é justamente ver o que a
	// decodificação descarta. Os params vão junto para dar para saber de qual
	// anúncio é cada dump quando há vários no log.
	if c.dumpActions {
		slog.Info("gnjoy: resposta crua da action",
			"action", actionType, "params", params, "data", truncateDump(env.Data))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("gnjoy: decodificando dados da action %q: %w", actionType, err)
	}
	return nil
}

// StoreLocation identifica uma loja específica aberta por um jogador:
// servidor + mapa + "ssi" (o identificador interno da sessão da loja).
type StoreLocation struct {
	SvrId int
	MapId int
	SSI   string
}

// GetStoreDetail retorna os detalhes de uma loja específica (posição no
// mapa, nome, personagem vendedor, preço e quantidade do item anunciado).
func (c *Client) GetStoreDetail(ctx context.Context, loc StoreLocation, opts ...CallOption) (*StoreDetail, error) {
	params := map[string]any{
		"svrId": loc.SvrId,
		"mapId": loc.MapId,
		"ssi":   loc.SSI,
	}
	var detail StoreDetail
	labels := activityLabels{
		InProgress: fmt.Sprintf("Consultando detalhes de uma loja (mapa %d)", loc.MapId),
		Success:    fmt.Sprintf("Detalhes da loja consultados (mapa %d)", loc.MapId),
		Error:      fmt.Sprintf("Falha ao consultar detalhes da loja (mapa %d)", loc.MapId),
	}
	if err := c.callAction(ctx, "store", params, &detail, labels, newCallConfig(opts)); err != nil {
		return nil, err
	}
	return &detail, nil
}

// GetItemDetail retorna os detalhes do item anunciado em uma loja
// específica. lang segue o formato de locale usado pelo site para
// nome/descrição do item (ex.: "en-US"); se vazio, "en-US" é usado.
func (c *Client) GetItemDetail(ctx context.Context, loc StoreLocation, lang string, opts ...CallOption) (*ItemDetail, error) {
	if lang == "" {
		lang = "en-US"
	}
	params := map[string]any{
		"svrId":    loc.SvrId,
		"mapId":    loc.MapId,
		"ssi":      loc.SSI,
		"multiLan": lang,
	}
	var detail ItemDetail
	labels := activityLabels{
		InProgress: "Consultando detalhes do item anunciado",
		Success:    "Detalhes do item consultados",
		Error:      "Falha ao consultar detalhes do item",
	}
	if err := c.callAction(ctx, "item", params, &detail, labels, newCallConfig(opts)); err != nil {
		return nil, err
	}
	return &detail, nil
}

// PriceHistoryParams são os filtros aceitos pela consulta de histórico de
// preços de um item em um servidor.
type PriceHistoryParams struct {
	ItemId int
	SvrId  int
	// Page é a página de resultados, começando em 1. Se zero, usa 1.
	Page int
	// Limit é a quantidade de itens por página. Se zero, usa 10.
	Limit int
	// Period é o período do histórico solicitado. O formato exato usado
	// pelo site não foi observado (a captura original mandava o valor
	// "indefinido" do Next.js); se nil, esse comportamento é replicado.
	Period *string
}

// GetPriceHistory retorna o histórico de preços (mínimo, máximo e médio,
// por dia) pelos quais um item foi anunciado em um servidor.
func (c *Client) GetPriceHistory(ctx context.Context, p PriceHistoryParams, opts ...CallOption) (*PriceHistory, error) {
	page := p.Page
	if page == 0 {
		page = 1
	}
	limit := p.Limit
	if limit == 0 {
		limit = 10
	}

	var period any = "$undefined"
	if p.Period != nil {
		period = *p.Period
	}

	params := map[string]any{
		"itemId": p.ItemId,
		"svrId":  p.SvrId,
		"page":   page,
		"limit":  limit,
		"period": period,
	}
	var history PriceHistory
	labels := activityLabels{
		InProgress: fmt.Sprintf("Consultando histórico de preço do item #%d", p.ItemId),
		Success:    fmt.Sprintf("Histórico de preço do item #%d consultado", p.ItemId),
		Error:      fmt.Sprintf("Falha ao consultar histórico de preço do item #%d", p.ItemId),
	}
	if err := c.callAction(ctx, "price", params, &history, labels, newCallConfig(opts)); err != nil {
		return nil, err
	}
	return &history, nil
}
