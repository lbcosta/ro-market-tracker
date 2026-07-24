# ro-market-tracker
Tracker de preços do mercado RO LATAM

Client HTTP em Go, com uma API REST própria, que consulta as rotas internas
do site do GnJoy LATAM (reverse-engineered a partir do DevTools, ver anexo
no fim deste arquivo) e expõe os dados de mercado como JSON.

## Estrutura do projeto

```
cmd/server/main.go        binário do servidor HTTP
internal/gnjoy/           client para as rotas internas do GnJoy LATAM
internal/api/             API REST própria (handlers + roteador)
```

## Rodando

```
go run ./cmd/server
```

Variáveis de ambiente (todas opcionais):

| Variável          | Padrão                                   | Descrição                                            |
|-------------------|-------------------------------------------|-------------------------------------------------------|
| `PORT`            | `8080`                                     | Porta HTTP do servidor                                |
| `GNJOY_BASE_URL`  | `https://ro.gnjoylatam.com`                | Domínio base do site do GnJoy LATAM                   |
| `GNJOY_LOCALE`    | `pt`                                       | Locale usado nas rotas (`pt`, `en` ou `es`)            |
| `GNJOY_ACTION_ID` | ver `gnjoy.DefaultActionID` no código      | Hash da Next.js Server Action (ver aviso abaixo)       |
| `GNJOY_RATE_LIMIT_RPS`   | `1` (`gnjoy.DefaultRateLimitRPS`)    | Requisições por segundo permitidas ao upstream          |
| `GNJOY_RATE_LIMIT_BURST` | `1` (`gnjoy.DefaultRateLimitBurst`)  | Rajada inicial permitida acima do ritmo sustentado       |

## Rate limiting

O site do GnJoy LATAM tem um rate limiter próprio que responde `429 Too Many
Requests` quando ultrapassado — e seus parâmetros exatos não são públicos.
Para nunca esbarrar nele, toda requisição enviada ao upstream (busca,
detalhe de loja/item, histórico de preço e a descoberta de action id)
passa por um único rate limiter compartilhado (`golang.org/x/time/rate`,
token bucket) dentro do `gnjoy.Client`:

- Requisições que excedem o ritmo configurado **ficam em fila, atrasadas**,
  em vez de serem disparadas todas de uma vez — é o próprio `Wait()` do
  limiter bloqueando a goroutine da requisição até haver uma "vaga".
- O padrão é deliberadamente conservador (**1 requisição/segundo, sem
  rajada**), já que não temos como confirmar o limite real do site. Ajuste
  via `GNJOY_RATE_LIMIT_RPS`/`GNJOY_RATE_LIMIT_BURST` (ou
  `gnjoy.WithRateLimit` no código) se, na prática, o site permitir mais.
- Como defesa extra, se mesmo assim vier um `429`, o client aguarda o tempo
  indicado no cabeçalho `Retry-After` (ou um backoff exponencial, se ele não
  vier) e tenta de novo, até 5 vezes, antes de desistir. Se todas as
  tentativas esgotarem, a API própria responde `503 Service Unavailable`
  (em vez de propagar o 429) para deixar claro que é uma condição
  temporária.

## Endpoints da API REST

Todas as respostas são JSON. Erros seguem o formato `{"error": "mensagem"}`.

### `GET /api/v1/shops`

Busca lojas de comércio que estão comprando ou vendendo um item pelo nome,
em um servidor.

Query params (todos obrigatórios): `server` (nome do servidor, ex.
`NIDHOGG`), `storeType` (`BUY` ou `SELL`), `item` (nome do item buscado).

```
curl -G http://localhost:8080/api/v1/shops \
  --data-urlencode "server=NIDHOGG" \
  --data-urlencode "storeType=BUY" \
  --data-urlencode "item=Pó de Éter"
```

### `GET /api/v1/shops/{svrId}/{mapId}/{ssi}`

Detalhes de uma loja específica (posição no mapa, nome da loja, personagem
vendedor, preço e quantidade). `svrId`, `mapId` e `ssi` vêm do resultado da
busca acima.

Inclui o campo `refine`: o nível de refino do equipamento (ex.: `7` para
"+7"). **A busca (`/api/v1/shops`) não traz o refino** — o site só expõe
essa informação embutida como um prefixo `+N` no nome completo do item
retornado por esta rota de detalhe (`itemFullName`, ex.:
`"+7Laço da Celine[1]"`), o que faz anúncios do "mesmo" item aparecerem na
busca com preços muito diferentes sem nenhuma explicação aparente. `refine`
já vem parseado desse prefixo; é `0` tanto para um item sem refino quanto
para um item não refinável — o site não permite diferenciar os dois casos.

### `GET /api/v1/shops/{svrId}/{mapId}/{ssi}/item`

Detalhes do item anunciado nessa loja. Aceita `?lang=` opcional (padrão
`en-US`).

### `GET /api/v1/items/{itemId}/price-history`

Histórico de preços (mínimo, máximo e médio, por dia) pelos quais um item já
foi anunciado no servidor.

Query params: `server` (svrId numérico, obrigatório), `page`, `limit`,
`period` (opcionais).

### `GET /healthz`

Health check simples.

## Aviso importante sobre fragilidade

Este client depende de rotas internas de uma aplicação Next.js que **não são
uma API pública documentada** — são apenas o que o navegador chama ao
navegar pela página de comércio. Em especial:

- As requisições de detalhe (loja, item, histórico de preço) usam uma
  *Server Action* do Next.js, identificada por um hash (`next-action`) que
  muda a cada novo deploy do site. O client detecta sozinho quando esse hash
  fica desatualizado (resposta 404/5xx ou fora do formato esperado),
  redescobre o valor atual varrendo os chunks JS publicados pela página
  (`internal/gnjoy/discover.go`) e refaz a chamada automaticamente — não é
  necessário atualizar nada manualmente. `GNJOY_ACTION_ID` /
  `gnjoy.WithActionID` continuam existindo apenas como um valor inicial
  opcional, para evitar a requisição extra de descoberta na primeira
  chamada.
- O cabeçalho `next-router-state-tree` é montado de forma best-effort pelo
  client (`internal/gnjoy/client.go`), já que seu formato é interno do
  Next.js e não documentado.
- O site pode adicionar proteções (rate limiting, CSRF, etc.) a qualquer
  momento sem aviso.