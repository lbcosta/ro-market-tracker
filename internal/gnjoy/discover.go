package gnjoy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sync"
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
	})
	if err != nil {
		return "", fmt.Errorf("gnjoy: buscando página para descoberta do action id: %w", err)
	}

	chunkPaths := dedupeStrings(chunkPathRe.FindAllString(string(body), -1))
	if len(chunkPaths) == 0 {
		return "", fmt.Errorf("%w: nenhum chunk JS encontrado na página", ErrActionIDNotFound)
	}

	searchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct{ id string }
	results := make(chan result, len(chunkPaths))
	sem := make(chan struct{}, 6)

	var wg sync.WaitGroup
	for _, path := range chunkPaths {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			req, err := http.NewRequestWithContext(searchCtx, http.MethodGet, c.baseURL+path, nil)
			if err != nil {
				return
			}
			chunkBody, err := c.do(req, activityLabels{
				InProgress: "Procurando a rota atualizada nos arquivos do site",
				Success:    "Arquivo do site consultado",
				Error:      "Falha ao consultar arquivo do site",
			})
			if err != nil {
				return
			}
			if m := serverActionRe.FindSubmatch(chunkBody); m != nil {
				results <- result{id: string(m[1])}
				cancel()
			}
		}(path)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		if r.id != "" {
			return r.id, nil
		}
	}
	return "", ErrActionIDNotFound
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
