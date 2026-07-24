package gnjoy

import (
	"bytes"
	"context"
	"encoding/json"
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

	// tradingPath é o caminho (relativo ao locale) da página de busca de
	// lojas de comércio, tanto para a busca em si (GET) quanto para as
	// Server Actions de detalhe (POST).
	tradingPath = "/intro/shop-search/trading"

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
	DefaultActionID = "404ed8774f606f8b1eb689aac3cb179d34321adc53"

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
	// após um 429, antes de desistir e propagar o erro.
	maxTooManyRequestsRetries = 5

	// maxRetryAfter limita o tempo de espera por tentativa mesmo que o
	// upstream mande um "Retry-After" maior, para não travar uma
	// requisição indefinidamente por causa de um valor anômalo.
	maxRetryAfter = 2 * time.Minute
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
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
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

// do executa req respeitando o rate limiter do Client — se necessário, a
// chamada fica bloqueada em fila até haver "vaga" — e, caso o upstream
// mesmo assim responda 429, aguarda o tempo indicado em "Retry-After" (ou
// um backoff exponencial, se o cabeçalho não vier) e tenta de novo, até
// maxTooManyRequestsRetries vezes.
func (c *Client) do(req *http.Request) ([]byte, error) {
	req.Header.Set("user-agent", c.userAgent)

	var lastErr error
	for attempt := 0; attempt <= maxTooManyRequestsRetries; attempt++ {
		if attempt > 0 && req.Body != nil {
			// Requisições sem corpo (GET) não precisam de nada aqui; as
			// com corpo (POST) precisam recriá-lo, já que ele já foi
			// consumido na tentativa anterior.
			if req.GetBody == nil {
				return nil, fmt.Errorf("gnjoy: upstream retornou 429 e a requisição não pode ser refeita (sem corpo reaproveitável): %w", lastErr)
			}
			body, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("gnjoy: recriando corpo da requisição para nova tentativa: %w", err)
			}
			req.Body = io.NopCloser(body)
		}

		if err := c.limiter.Wait(req.Context()); err != nil {
			return nil, fmt.Errorf("gnjoy: aguardando vaga no rate limiter: %w", err)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("gnjoy: executando requisição: %w", err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("gnjoy: lendo corpo da resposta: %w", err)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			lastErr = &HTTPError{StatusCode: resp.StatusCode, Body: truncateBody(body)}
			if attempt == maxTooManyRequestsRetries {
				break
			}
			wait := retryAfterDuration(resp.Header.Get("Retry-After"), attempt)
			slog.Warn("gnjoy: upstream respondeu 429, aguardando antes de tentar de novo",
				"path", req.URL.Path, "tentativa", attempt+1, "espera", wait)
			select {
			case <-time.After(wait):
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return nil, &HTTPError{StatusCode: resp.StatusCode, Body: truncateBody(body)}
		}
		return body, nil
	}
	return nil, fmt.Errorf("gnjoy: upstream continuou retornando 429 após %d tentativas: %w", maxTooManyRequestsRetries+1, lastErr)
}

func truncateBody(body []byte) string {
	const maxLen = 500
	if len(body) > maxLen {
		body = body[:maxLen]
	}
	return string(body)
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
// "Next-Router-State-Tree" que o navegador envia ao navegar dentro da
// página de comércio. O formato é interno do Next.js e não documentado
// oficialmente; o objetivo aqui não é reproduzi-lo byte a byte, e sim
// enviar uma árvore de rotas plausível o bastante para o servidor aceitar a
// requisição como uma navegação para tradingPath.
func (c *Client) routerStateTree() string {
	leaf := []any{"__PAGE__", map[string]any{}, tradingPath, "refresh"}
	idNode := []any{[]any{"id", "trading", "d"}, map[string]any{"children": leaf}, nil, nil}
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
func (c *Client) SearchShops(ctx context.Context, p SearchShopsParams) (*ShopSearchResult, error) {
	q := url.Values{}
	q.Set("storeType", string(p.StoreType))
	q.Set("serverType", p.ServerType)
	q.Set("searchWord", p.SearchWord)

	reqURL := c.pageURL(tradingPath) + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gnjoy: montando requisição de busca: %w", err)
	}
	req.Header.Set("accept", "*/*")
	req.Header.Set("rsc", "1")
	req.Header.Set("next-url", "/"+c.locale+tradingPath)
	req.Header.Set("next-router-state-tree", c.routerStateTree())
	req.Header.Set("referer", c.pageURL(tradingPath))

	body, err := c.do(req)
	if err != nil {
		return nil, err
	}

	obj, err := parseFlightObject(body, "list", "totalCount")
	if err != nil {
		return nil, fmt.Errorf("gnjoy: interpretando resposta da busca: %w", err)
	}
	result := &ShopSearchResult{}
	if err := decodeInto(obj, result); err != nil {
		return nil, err
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
func (c *Client) callAction(ctx context.Context, actionType string, params any, out any) error {
	actionID := c.currentActionID()

	err := c.callActionWithID(ctx, actionType, params, out, actionID)
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
	return c.callActionWithID(ctx, actionType, params, out, newID)
}

func (c *Client) callActionWithID(ctx context.Context, actionType string, params any, out any, actionID string) error {
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
	req.Header.Set("next-router-state-tree", c.routerStateTree())
	req.Header.Set("origin", c.baseURL)
	req.Header.Set("referer", reqURL)

	body, err := c.do(req)
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
		return fmt.Errorf("gnjoy: action %q reportou falha", actionType)
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
func (c *Client) GetStoreDetail(ctx context.Context, loc StoreLocation) (*StoreDetail, error) {
	params := map[string]any{
		"svrId": loc.SvrId,
		"mapId": loc.MapId,
		"ssi":   loc.SSI,
	}
	var detail StoreDetail
	if err := c.callAction(ctx, "store", params, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}

// GetItemDetail retorna os detalhes do item anunciado em uma loja
// específica. lang segue o formato de locale usado pelo site para
// nome/descrição do item (ex.: "en-US"); se vazio, "en-US" é usado.
func (c *Client) GetItemDetail(ctx context.Context, loc StoreLocation, lang string) (*ItemDetail, error) {
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
	if err := c.callAction(ctx, "item", params, &detail); err != nil {
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
func (c *Client) GetPriceHistory(ctx context.Context, p PriceHistoryParams) (*PriceHistory, error) {
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
	if err := c.callAction(ctx, "price", params, &history); err != nil {
		return nil, err
	}
	return &history, nil
}
