# ro-market-tracker

[![CI](https://github.com/lbcosta/ro-market-tracker/actions/workflows/ci.yml/badge.svg)](https://github.com/lbcosta/ro-market-tracker/actions/workflows/ci.yml)

Tracker de preços do mercado RO LATAM

Client HTTP em Go, com uma API REST própria e um frontend em HTMX, que
consulta as rotas internas do site do GnJoy LATAM (reverse-engineered a
partir do DevTools, ver anexo no fim deste arquivo) e expõe os dados de
mercado.

## Estrutura do projeto

```
cmd/server/main.go              binário do servidor HTTP
internal/gnjoy/                 client para as rotas internas do GnJoy LATAM
  client.go, flight.go            requisições, rate limiting, parser do formato RSC Flight
  discover.go                     descoberta/auto-refresh do action id da Server Action
  refine.go, types.go             parsing de refino, tipos de dados devolvidos pelo client
internal/api/                   API REST própria (JSON) — handlers + roteador
internal/web/                   frontend HTMX — handlers + roteador
  templates/                      *.html.tmpl (página, fragmentos de busca/expand)
  static/                         CSS, JS (app.js, watchlist.js) e htmx.min.js vendorizado
  watchlist.go                    endpoint JSON de preço/refino ao vivo p/ a watchlist
  history.go                      preços praticados p/ item fora do mercado (busca sem resultado)
  stats.go, navi.go               estatísticas de 7 dias, comando /navi
  activity.go                     stream SSE da atividade do client (barra de atividades)
internal/gnjoytest/             mock do site do GnJoy usado por todos os testes
  cmd/mockgnjoy/                  o mesmo mock como processo, para os testes de navegador
e2e/                            testes de navegador (Playwright)
docs/webtools-api-research.md   pesquisa original no DevTools (captura de tráfego bruta)
```

## Baixando o executável

Cada release publica um binário pronto em
[Releases](https://github.com/lbcosta/ro-market-tracker/releases), para Linux,
macOS (Intel e Apple Silicon) e Windows. Não há nada a instalar junto: o
frontend e o HTMX são embutidos no binário (`go:embed`), então é um arquivo só.

```sh
# Linux (x86-64); troque o sufixo pelo do seu sistema
curl -LO https://github.com/lbcosta/ro-market-tracker/releases/latest/download/ro-market-tracker_1.0.0_linux_amd64.tar.gz
tar -xzf ro-market-tracker_1.0.0_linux_amd64.tar.gz
./ro-market-tracker
```

Toda release traz um `SHA256SUMS` para conferir o download:

```sh
sha256sum -c SHA256SUMS
```

Enquanto o repositório for público, os binários também saem com atestação de
proveniência, que amarra cada artefato ao commit e ao workflow que o gerou (o
GitHub não oferece o recurso em repositório privado de conta pessoal):

```sh
gh attestation verify ro-market-tracker_1.0.0_linux_amd64.tar.gz \
  --repo lbcosta/ro-market-tracker
```

Para saber qual versão você tem em mãos: `./ro-market-tracker -version`.

## Rodando a partir do código

```
go run ./cmd/server
```

Depois, acesse `http://localhost:8080/` para a página de busca (frontend),
ou use a [API REST](#endpoints-da-api-rest) diretamente em `/api/v1/...`.

## Testes

```
go test ./...      # client, API REST e frontend
cd e2e && npm test # testes de navegador (ver e2e/README.md)
```

**Nenhum teste toca a API real.** O site do GnJoy LATAM tem rate limiting
próprio e não é uma API pública documentada, então uma suíte batendo nele
seria frágil e um jeito rápido de tomar bloqueio. No lugar dele há
`internal/gnjoytest`: um mock que fala o mesmo protocolo das rotas internas
(RSC Flight, Server Actions e a descoberta do action id nos chunks JS), com
injeção de falhas, de atrasos e de deploys do site (troca do action id).

Os tipos do mock são declarados de forma independente dos tipos do client, de
propósito: eles descrevem o que o site devolve (ver
`docs/webtools-api-research.md`), não o que o client espera. Se um json tag do
client divergir do contrato real, os testes quebram — o que não aconteceria se
mock e client compartilhassem a mesma struct.

Os testes de navegador sobem esse mesmo mock como processo e apontam o
servidor real para ele, então frontend, servidor e client são exercitados de
ponta a ponta contra um upstream controlado.

## CI/CD

Dois workflows do GitHub Actions (`.github/workflows/`):

- **`ci.yml`** — roda a cada push (em qualquer branch) e em pull requests:
  formatação, `go vet`, `go mod tidy` limpo, testes nos três sistemas que
  recebem binário (com detector de corrida no Linux) e os testes de navegador.
- **`release.yml`** — dispara ao empurrar uma tag `v*`. Reaproveita a CI
  inteira e só publica se ela passar; depois compila os binários, gera
  checksums, atesta a proveniência e cria a release.

Para publicar uma versão:

```sh
git tag -a v1.0.0 -m "v1.0.0"
git push origin v1.0.0
```

Tags com sufixo (`v1.0.0-rc1`, `v1.0.0-beta2`) saem marcadas como pré-release,
para não virarem o download padrão da página do projeto.

## Frontend (HTMX)

Página de busca em `/`: escolha o servidor (`NIDHOGG` ou `FREYA`, `NIDHOGG`
por padrão), digite o nome do item e clique na lupa. A busca sempre procura
lojas comprando o item (ou seja, anúncios de jogadores vendendo o item) —
não há seletor de tipo de negociação na UI. A busca (`GET /web/search`)
devolve o fragmento HTML da tabela — nome, preço, loja e quantidade — que o
HTMX troca dentro do resultado, sem recarregar a página.

Cada linha da tabela tem um indicador "▸" mostrando que é expansível.
Clicar nela dispara `GET /web/shops/{svrId}/{mapId}/{ssi}/expand` (com um
spinner enquanto carrega) e expande a linha em um card com:

- Refino do equipamento (só aparece aqui — ver aviso na seção da API sobre
  por que a busca não traz essa informação).
- Vendedor, loja e tipo do item.
- Localização como um botão com o comando `/navi <mapa>/<x>/<y>`: clicar
  copia o comando para a área de transferência.
- Estatísticas dos últimos 7 dias (mínimo, médio, máximo, quantidade
  vendida e desvio padrão), calculadas a partir dos agregados diários que o
  GnJoy LATAM devolve (`GetPriceHistory` com `Limit: 7`) — a média e o
  desvio padrão são ponderados pela quantidade negociada em cada dia, já
  que só temos a média diária, não o preço de cada venda individual.

A busca ao servidor só acontece na primeira vez que uma linha é expandida
(`hx-trigger="click once"`, o ícone vira "▾"). Clicar de novo apenas
colapsa o card (ícone volta a "▸") sem descartar o que foi carregado;
clicar uma terceira vez reexpande mostrando os mesmos dados instantaneamente,
sem refazer a consulta — só um `toggleRow` em `internal/web/static/app.js`
alternando a visibilidade, nenhuma requisição nova ao upstream.

### Item fora do mercado atual

Quem procura um item para rastrear muitas vezes o procura justamente porque
ninguém está anunciando. Nesse caso a busca não para em "nenhum resultado":
ela consulta os preços praticados no servidor e mostra por quanto o item
vinha sendo vendido, com um "+ Watchlist" por linha para ser avisado quando
ele voltar a aparecer.

Isso usa a OUTRA página da seção de busca do site,
`/intro/shop-search/market-price` (`gnjoy.SearchMarketPrice`), que é a única
que busca **por nome** e já devolve mínimo, médio, máximo e volume agregados
por item. Não confundir com a Server Action `price` (`GetPriceHistory`), que
é por `itemId` e devolve a série diária usada no card de detalhe de um
anúncio — ela não serve aqui, porque um item fora do mercado não traz o
próprio id junto.

Como a busca casa por trecho do nome, o resultado costuma ter mais de uma
linha: "rapidez" traz "Módulo de S-Rapidez" e "Automódulo de M-Rapidez", e a
ordem devolvida pelo site (por relevância) é preservada.

O período consultado é o de 7 dias (`period=7` — o site aceita `1`, `7`, `30`
e `ALL`). Se essa janela vier vazia, `internal/web/history.go` refaz a
consulta com `ALL` antes de concluir qualquer coisa: um item pode nunca ter
sido vendido no servidor ou apenas estar parado há mais de uma semana, e a
diferença muda a resposta. Daí os três desfechos:

| Últimos 7 dias | Histórico completo | O que aparece |
| --- | --- | --- |
| tem vendas | (não consultado) | tabela dos últimos 7 dias |
| vazio | tem vendas | tabela do histórico completo, avisando que não houve venda na última semana |
| vazio | vazio | aviso de que o item nunca foi vendido no servidor |

### Watchlist

Painel fixo do lado direito da página. O botão "+ Watchlist", ao lado do
cabeçalho "Resultados de \<item\>", adiciona o item buscado (identificado
por servidor + itemId, para não duplicar) a uma lista mantida inteiramente
no navegador via `localStorage` (`internal/web/static/watchlist.js`) — não
há conta de usuário nem persistência no servidor; a lista é local a cada
navegador.

Cada linha da watchlist mostra:

- Uma luz verde (monitorando) ou vermelha (não monitorando), que alterna de
  estado ao clicar — é o que decide se o item participa da checagem
  periódica descrita abaixo.
- Nome do item e, se a loja de menor preço for uma arma ou armadura
  (`databaseType` "weapon"/"armor"), um badge com o refino dessa unidade —
  para itens sem refino (não são equipamento), nada é mostrado ali.
- Preço alvo, editável: clicar no valor ("Alvo: —" ou "Alvo: X z") vira um
  campo numérico; `Enter` confirma e salva, `Esc` ou perder o foco sem
  confirmar descarta a edição. Deixar o campo vazio e confirmar remove o
  alvo (volta a "—").
- Para itens que já mostraram ter refino, o badge também é editável do
  mesmo jeito (clicar, digitar, `Enter`): em vez de mostrar o refino de
  qualquer loja que estiver mais barata, passa a exigir esse refino
  específico — o "menor preço atual" da linha vira o menor preço só entre
  lojas NESSE refino (ex.: fixar "+10" numa arma que só é barata em +0
  passa a mostrar o preço da unidade +10, mesmo que ela não seja a mais
  barata no geral). Deixar o campo vazio e confirmar volta ao padrão
  (qualquer refino, o que estiver mais barato).
- O menor preço anunciado agora (respeitando o refino fixado, se houver).
- Um badge "🎯 Alvo atingido" e a linha destacada com borda verde, quando o
  menor preço atual está no valor do alvo ou abaixo dele.
- Um "×" para remover da lista.

O preço atual (e o refino) é a única parte que depende do servidor:
`GET /web/watchlist/price?server=...&itemId=...&item=...&refine=...`
(`refine` é opcional) refaz a mesma busca por nome usada na página
principal, filtra pelo `itemId` exato (uma busca por nome pode casar itens
diferentes — ver teste com "Espada", que retorna itens com itemId 7110,
24246 e 600009) e:

- Sem `refine`: pega o menor preço entre eles e, se a loja mais barata for
  equipamento, busca o refino dela via `GetStoreDetail` — só informativo.
- Com `refine`: ordena os candidatos por preço crescente e consulta o
  detalhe de cada um (`GetStoreDetail`), um de cada vez, até achar o
  primeiro cujo refino bate exatamente com o pedido. Isso pode custar várias
  chamadas ao upstream para um item com muitos anúncios — o rate limiter do
  client já serializa tudo, então o efeito é essa checagem demorar mais
  (a linha mostra o spinner enquanto isso), não rajadas de requisições.

Ligar/desligar, editar o alvo, editar o refino fixado e remover um item são
só atualizações de `localStorage` + DOM, sem chamada ao servidor (exceto
editar o refino, que dispara uma nova consulta de preço — o filtro mudou, o
preço em cache não serve mais); adicionar um item novo busca o preço só
dele (os demais já carregados não são recarregados).

#### Acompanhar preço ou acompanhar disponibilidade

Uma entrada acompanha uma de duas condições, conforme de qual tabela ela foi
adicionada (é o `data-mode` do botão que decide — ver `MODE_PRICE` e
`MODE_AVAILABILITY` em `watchlist.js`):

| Origem | O que se espera | Como a linha aparece |
| --- | --- | --- |
| tabela de resultados | um **preço** | "Alvo: X" (editável) e "Atual: Y"; avisa quando o menor preço chega ao alvo |
| tabela de histórico | o item **voltar ao mercado** | "Nenhum anúncio" → "Produto encontrado por Y"; avisa no primeiro anúncio, seja qual for o preço |

O segundo caso existe porque quem chega pela tabela de histórico está
olhando um item que ninguém está anunciando: não há preço a esperar, e um
campo "Alvo: —" ali só confundiria o que a linha acompanha — por isso ele
nem é renderizado nesse modo. Fora essa diferença, a mecânica é a mesma
descrita abaixo, inclusive o "só um aviso por cruzamento": se o item sumir do
mercado de novo, a entrada volta a esperar e avisa quando ele reaparecer.

Entradas gravadas antes dessa distinção existir não têm o campo `mode` e são
tratadas como acompanhamento de preço, que era o único comportamento.

#### Monitoramento e alertas

Enquanto a página estiver aberta, a cada 10 minutos
(`MONITOR_INTERVAL_MS` em `watchlist.js`) os itens com o indicador ligado
(verde) têm o preço reconsultado. Se a condição que o item acompanha passar
a valer, o usuário é avisado de duas formas:

- Um toast no canto inferior direito da página (sempre aparece, não
  depende de nenhuma permissão do navegador).
- Uma notificação nativa do sistema operacional, via `Notification` do
  navegador — a permissão só é pedida na hora em que ela faz falta (o
  primeiro aviso), não no carregamento da página; se for negada ou o
  navegador não suportar, só o toast é mostrado.

Cada item só notifica uma vez por "cruzamento" da condição: enquanto ela
continuar valendo, as checagens seguintes não repetem o aviso (o card e o
badge continuam mostrando o status, só o toast/notificação não se repetem);
se ela deixar de valer e voltar a valer depois, um novo aviso é disparado.
Esse estado ("já avisado desta vez") é persistido em `localStorage` junto
com o resto da entrada.

Itens com o indicador desligado (vermelho) continuam na lista, mas não são
reconsultados pela checagem periódica nem podem disparar aviso enquanto
assim permanecerem — eles ainda têm o preço atualizado ao carregar a
página ou ao serem adicionados, só não entram no ciclo de 10 minutos.

Como a watchlist vive só no navegador, o monitoramento também só roda
enquanto uma aba com a página estiver aberta; fechar a aba interrompe as
checagens até ela ser reaberta (quando o ciclo recomeça imediatamente,
antes do primeiro intervalo de 10 minutos).

O HTMX é vendorizado localmente em `internal/web/static/htmx.min.js`
(embutido no binário via `go:embed`) — não depende de CDN em runtime.

Variáveis de ambiente (todas opcionais):

| Variável                 | Padrão                                | Descrição                                          |
|--------------------------|----------------------------------------|-----------------------------------------------------|
| `PORT`                   | `8080`                                 | Porta HTTP do servidor                               |
| `GNJOY_BASE_URL`         | `https://ro.gnjoylatam.com`            | Domínio base do site do GnJoy LATAM                  |
| `GNJOY_LOCALE`           | `pt`                                   | Locale usado nas rotas (`pt`, `en` ou `es`)          |
| `GNJOY_ACTION_ID`        | ver `gnjoy.DefaultActionID` no código  | Hash da Next.js Server Action (ver aviso abaixo)     |
| `GNJOY_RATE_LIMIT_RPS`   | `1` (`gnjoy.DefaultRateLimitRPS`)      | Requisições por segundo permitidas ao upstream       |
| `GNJOY_RATE_LIMIT_BURST` | `1` (`gnjoy.DefaultRateLimitBurst`)    | Rajada inicial permitida acima do ritmo sustentado   |

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