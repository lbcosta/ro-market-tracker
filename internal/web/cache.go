package web

import (
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Validades aceitas nas consultas ao upstream feitas pelo frontend. Todo o
// tráfego redundante do navegador afunila no mesmo processo (várias abas,
// F5, reordenação da tabela, o ciclo da watchlist), então é aqui — e não com
// coordenação entre abas — que ele deixa de virar requisição nova ao GnJoy.
const (
	// freshMaxAge é a validade aceita nas consultas interativas (a busca, o
	// histórico): reaproveita um resultado recém-buscado (um F5, um clique
	// de ordenação) sem servir nada que um humano perceberia como velho.
	freshMaxAge = 30 * time.Second

	// monitorMaxAge é a validade aceita pela checagem automática da
	// watchlist. Como o array da watchlist — inclusive quando cada item foi
	// consultado por último — mora no localStorage, compartilhado entre
	// abas da mesma origem, várias abas tendem a escolher o mesmo item a
	// cada tick (ver pickNextEntry em static/watchlist.js); esta validade é
	// o que faz a segunda (e a terceira...) encontrar um cache ainda válido
	// em vez de gerar outra ida ao upstream.
	monitorMaxAge = 4 * time.Minute

	searchCacheSize      = 256
	marketPriceCacheSize = 256
	storeDetailMemoSize  = 2048
	itemDetailMemoSize   = 2048
)

// cacheKey monta a chave de cache a partir das partes que identificam a
// consulta, separadas por um caractere de controle que não aparece em nome
// de item nem de servidor — concatenar direto poderia colidir chaves.
func cacheKey(parts ...string) string {
	return strings.Join(parts, "\x1f")
}

// ttlCache é um cache com validade por idade e deduplicação de buscas
// concorrentes, para ficar entre os handlers do frontend e o client do
// GnJoy. A validade não é do cache, é de cada leitura: o mesmo resultado
// pode valer por 30s para uma busca interativa e por 4min para a checagem da
// watchlist (ver freshMaxAge/monitorMaxAge). É seguro para uso concorrente.
type ttlCache[V any] struct {
	// group deduplica buscas concorrentes da mesma chave: várias abas
	// pedindo o mesmo item ao mesmo tempo geram UMA ida ao upstream, e
	// todas recebem o resultado dela.
	group singleflight.Group

	mu      sync.Mutex
	entries map[string]cacheEntry[V]
	maxSize int

	// gen conta os resets já feitos. Quem vai ao upstream captura a geração
	// ANTES da busca e a devolve no putIfCurrent, que descarta o resultado
	// se um reset tiver acontecido no meio — ver reset.
	gen int64

	// now existe para os testes controlarem o relógio.
	now func() time.Time
}

type cacheEntry[V any] struct {
	value     V
	fetchedAt time.Time
}

func newTTLCache[V any](maxSize int) *ttlCache[V] {
	return &ttlCache[V]{
		entries: make(map[string]cacheEntry[V]),
		maxSize: maxSize,
		now:     time.Now,
	}
}

// Do devolve o valor de key se ele tiver sido buscado há menos de maxAge;
// caso contrário executa fetch, guarda e devolve o resultado. Erros não são
// cacheados — a chamada seguinte tenta de novo. maxAge zero (ou negativo)
// significa "não aceito cache nenhum": sempre busca (as concorrentes ainda
// são deduplicadas).
func (c *ttlCache[V]) Do(key string, maxAge time.Duration, fetch func() (V, error)) (V, error) {
	if v, ok := c.peekFresh(key, maxAge); ok {
		return v, nil
	}
	vi, err, _ := c.group.Do(key, func() (any, error) {
		// Outra chamada pode ter buscado (e cacheado) enquanto esta
		// esperava a vez; se o resultado dela já serve, não busca de novo.
		if v, ok := c.peekFresh(key, maxAge); ok {
			return v, nil
		}
		gen := c.generation()
		v, err := fetch()
		if err != nil {
			return nil, err
		}
		c.putIfCurrent(gen, key, v)
		return v, nil
	})
	if err != nil {
		var zero V
		return zero, err
	}
	return vi.(V), nil
}

func (c *ttlCache[V]) peekFresh(key string, maxAge time.Duration) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || maxAge <= 0 || c.now().Sub(e.fetchedAt) >= maxAge {
		var zero V
		return zero, false
	}
	return e.value, true
}

// peek devolve o valor de key seja qual for a idade dele. É para dado
// imutável (o refino de uma loja nunca muda para um mesmo ssi), em que idade
// não é problema e quem chama decide por fora se — e quando — vale pagar uma
// busca (ver o orçamento de detalhes em watchlist.go).
func (c *ttlCache[V]) peek(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		var zero V
		return zero, false
	}
	return e.value, true
}

// generation identifica a "era" atual do cache, que muda a cada reset. Vale
// junto com putIfCurrent: capture a geração antes de ir ao upstream e a
// devolva ao guardar o resultado.
func (c *ttlCache[V]) generation() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen
}

// putIfCurrent guarda um valor buscado por quem chama (o caminho do peek); Do
// guarda sozinho. O valor é descartado — em vez de guardado — se o cache
// tiver sido resetado desde gen, porque então ele é dado de antes do reset
// (ver reset). Ao atingir maxSize, a entrada buscada há mais tempo sai.
func (c *ttlCache[V]) putIfCurrent(gen int64, key string, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gen != gen {
		return
	}
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxSize {
		c.evictOldestLocked()
	}
	c.entries[key] = cacheEntry[V]{value: value, fetchedAt: c.now()}
}

// reset esvazia o cache — ver Handler.ResetCaches.
//
// Esvaziar o mapa não basta: uma busca ao upstream disparada ANTES do reset
// só guarda o resultado quando termina, o que pode ser depois dele — e a
// entrada reapareceria, com a idade zerada, num cache que deveria estar
// vazio. Quem pede o reset ficaria com um dado de antes dele, servido por
// mais um TTL inteiro. Por isso o reset também troca a geração: os
// resultados em voo agora são de uma era passada e o putIfCurrent os
// descarta. Foi exatamente essa corrida que deixou os testes de navegador
// instáveis — o teste seguinte recebia do cache o resultado que o anterior
// tinha pedido, sem chamada nenhuma ao upstream.
func (c *ttlCache[V]) reset() {
	c.mu.Lock()
	c.entries = make(map[string]cacheEntry[V])
	c.gen++
	c.mu.Unlock()
}

// evictOldestLocked é O(n), o que está de bom tamanho para os maxSize usados
// aqui (centenas de entradas, processo local de um usuário só).
func (c *ttlCache[V]) evictOldestLocked() {
	var oldestKey string
	var oldestAt time.Time
	first := true
	for k, e := range c.entries {
		if first || e.fetchedAt.Before(oldestAt) {
			first = false
			oldestKey, oldestAt = k, e.fetchedAt
		}
	}
	if !first {
		delete(c.entries, oldestKey)
	}
}
