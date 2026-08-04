package gnjoy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
)

// chunkPathRe casa caminhos de chunks JS publicados pelo Next.js
// (ex.: "/_next/static/chunks/5525-d546c5164dd262a4.js") em qualquer lugar
// do HTML da página — tanto em tags <script src="...">, quanto em <link
// rel="preload">.
var chunkPathRe = regexp.MustCompile(`/_next/static/chunks/[^"'\s]+\.js`)

// serverActionRe casa a declaração de uma Server Action do Next.js dentro
// de um chunk JS, ex.:
//
//	(0,r.createServerReference)("404ed8774f606f8b1eb689aac3cb179d34321adc53",r.callServer,...)
//
// O "(0,r." antes de createServerReference é a forma como o bundler chama
// o método preservando o "this"; o alias do módulo ("r" no exemplo) varia a
// cada build, por isso não faz parte do padrão.
var serverActionRe = regexp.MustCompile(`createServerReference\)?\(\s*"([0-9a-f]{16,64})"`)

// ErrActionIDNotFound é retornado quando a descoberta automática do action
// id não encontra nenhum candidato nos chunks JS da página.
var ErrActionIDNotFound = errors.New("gnjoy: não foi possível localizar o action id nos chunks da página")

// isStaleActionIDErr decide, a partir do erro de uma chamada de action, se
// vale a pena tentar redescobrir o action id e tentar de novo. O sinal mais
// forte é o upstream responder 404 (Next.js não reconhece o hash) ou 500;
// como fallback, uma resposta 200 que não tem o formato esperado (sem os
// campos "data"/"success") também é tratada como suspeita, já que pode ser
// uma página de erro renderizada no lugar do envelope da action.
func isStaleActionIDErr(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusNotFound || httpErr.StatusCode >= http.StatusInternalServerError
	}
	return errors.Is(err, ErrFieldsNotFound)
}

// RefreshActionID redescobre o id atual da Next.js Server Action usada
// pelas rotas de detalhe, buscando a página de comércio e varrendo os
// chunks JS que ela referencia até encontrar a declaração
// "createServerReference(...)". Se encontrado, o Client passa a usar esse
// id nas chamadas seguintes.
//
// callAction já chama isto sozinho quando detecta uma falha compatível com
// o id estar desatualizado, então normalmente não é necessário chamar este
// método diretamente — ele é exportado para permitir um refresh manual
// (por exemplo, em um job periódico) ou aquecer o cache no startup.
func (c *Client) RefreshActionID(ctx context.Context) (string, error) {
	before := c.currentActionID()

	c.discoverMu.Lock()
	defer c.discoverMu.Unlock()

	// Outra chamada pode ter feito a descoberta enquanto esperávamos a vez.
	if current := c.currentActionID(); current != before {
		return current, nil
	}

	id, err := c.discoverActionID(ctx)
	if err != nil {
		return "", err
	}
	c.setActionID(id)
	return id, nil
}

func (c *Client) discoverActionID(ctx context.Context) (string, error) {
	pageURL := c.pageURL(tradingPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return "", fmt.Errorf("gnjoy: montando requisição de descoberta do action id: %w", err)
	}
	req.Header.Set("accept", "text/html")

	body, err := c.do(req, activityLabels{
		InProgress: "Verificando se a rota da loja mudou",
		Success:    "Rota da loja verificada",
		Error:      "Falha ao verificar a rota da loja",
	}, newCallConfig(nil))
	if err != nil {
		return "", fmt.Errorf("gnjoy: buscando página para descoberta do action id: %w", err)
	}

	chunkPaths := dedupeStrings(chunkPathRe.FindAllString(string(body), -1))
	if len(chunkPaths) == 0 {
		return "", fmt.Errorf("%w: nenhum chunk JS encontrado na página", ErrActionIDNotFound)
	}

	// Os chunks são varridos um a um, e não em paralelo. O rate limiter do
	// Client serializa as requisições de qualquer forma (uma por segundo, por
	// padrão), então disparar todas de uma vez nunca economizou tempo de
	// verdade contra o site real — só empilhava goroutines na fila do
	// limiter. Em compensação custava caro: ao achar o id, as buscas
	// perdedoras eram canceladas com requisições JÁ EM VOO, que chegavam ao
	// site depois de a descoberta ter terminado. Daí vinham as entradas de
	// "falha" no log de atividade que não correspondiam a falha nenhuma, e a
	// impossibilidade de medir com precisão o custo da chamada seguinte —
	// uma requisição atrasada aparecia na conta dela.
	for _, path := range chunkPaths {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
		if err != nil {
			continue
		}
		// NoRetry: um 429 no meio da varredura significa que o site está
		// pedindo calma AGORA, e ainda haveria vários chunks pela frente —
		// insistir chunk a chunk só atrasaria a recuperação do bloqueio. A
		// varredura inteira é abortada; a próxima action real redispara a
		// descoberta quando o site voltar a aceitar.
		chunkBody, err := c.do(req, activityLabels{
			InProgress: "Procurando a rota atualizada nos arquivos do site",
			Success:    "Arquivo do site consultado",
			Error:      "Falha ao consultar arquivo do site",
		}, newCallConfig([]CallOption{NoRetry()}))
		if err != nil {
			var httpErr *HTTPError
			if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusTooManyRequests {
				return "", fmt.Errorf("gnjoy: descoberta do action id interrompida, o site está limitando requisições (429): %w", err)
			}
			continue
		}
		if m := serverActionRe.FindSubmatch(chunkBody); m != nil {
			return string(m[1]), nil
		}
	}
	return "", ErrActionIDNotFound
}

// WarmupActionID testa se o action id atual ainda é aceito pelo site,
// gastando uma única requisição mínima: um pedido de detalhe de loja com
// parâmetros que não correspondem a loja nenhuma de verdade. Uma resposta
// "loja não encontrada" (o esperado, e o que ErrActionFailed sinaliza) já
// confirma que o id está válido; só uma resposta que indique o id
// desatualizado dispara a redescoberta automática embutida em callAction —
// do mesmo jeito que aconteceria durante a primeira ação real do usuário, só
// que fora do caminho crítico dela.
//
// Ao contrário de RefreshActionID, NÃO força uma varredura completa dos
// chunks JS da página: essa varredura, mais cara e mais sujeita a esbarrar
// no rate limiter do site, só acontece quando de fato é necessária.
func (c *Client) WarmupActionID(ctx context.Context) error {
	params := map[string]any{"svrId": 0, "mapId": 0, "ssi": ""}
	labels := activityLabels{
		InProgress: "Verificando conexão com o mercado",
		Success:    "Conexão com o mercado verificada",
		Error:      "Falha ao verificar conexão com o mercado",
	}
	// NoRetry: o aquecimento é uma otimização de fundo; se o site estiver
	// pedindo calma, desistir de imediato é o certo — a primeira ação real
	// do usuário faz o mesmo papel mais tarde.
	err := c.callAction(ctx, "store", params, nil, labels, newCallConfig([]CallOption{NoRetry()}))
	if err == nil || errors.Is(err, ErrActionFailed) {
		return nil
	}
	return err
}

func dedupeStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, it := range items {
		if !seen[it] {
			seen[it] = true
			out = append(out, it)
		}
	}
	return out
}
