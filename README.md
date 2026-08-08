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
  searchword.go                   contorno da pontuação que o backend de busca recusa
  suspend.go                      suspensão de todas as consultas após um 429
internal/api/                   API REST própria (JSON) — handlers + roteador
internal/web/                   frontend HTMX — handlers + roteador
  templates/                      *.html.tmpl (página, fragmentos de busca/expand)
  static/                         CSS, JS (app.js, watchlist.js, theme.js, activity-bar.js, version.js) e htmx.min.js vendorizado
  watchlist.go                    endpoint JSON de preço/refino ao vivo p/ a watchlist
  bonus.go                        varredura de refino/bônus por anúncio, sob demanda, memoizada
  suspension.go                   sonda que reabre as consultas quando o site volta
  variants.go                     versões do item que existem no servidor mas não estão à venda
  cache.go                        cache TTL + deduplicação das consultas ao upstream
  history.go                      preços praticados p/ item fora do mercado (busca sem resultado)
  stats.go, navi.go               estatísticas de 7 dias, comando /navi
  activity.go                     stream SSE da atividade do client (barra de atividades)
internal/gnjoytest/             mock do site do GnJoy usado por todos os testes
  cmd/mockgnjoy/                  o mesmo mock como processo, para os testes de navegador
e2e/                            testes de navegador (Playwright)
docs/webtools-api-research.md   pesquisa original no DevTools (captura de tráfego bruta)
build/windows/                  ícone e metadados do .exe, usados só pelo release.yml
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
- Bônus aleatórios daquela unidade, quando houver (ver a seção abaixo). Aqui
  eles não custam nada: o card já consulta o detalhe do item para se montar.
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

### Separar os resultados por refino e por bônus aleatórios

Bônus aleatórios são as propriedades sorteadas de uma unidade específica de um
equipamento ("CRIT +4", "Conjuração variável -5%"). Como o refino, eles são
uma propriedade do **anúncio**, não do item de catálogo — e, diferente do
refino, valem para qualquer coisa que se vista, inclusive acessórios e
chapéus. São eles que explicam a maior parte da diferença de preço entre dois
anúncios do "mesmo" item: um Selo de Loki [1] custa 135M ou 300M dependendo do
que saiu nele.

Nenhum dos dois vem na busca. O refino só existe no prefixo `+N` do
`itemFullName` do detalhe da **loja**; os bônus, nos `randomOpt1..4` do detalhe
do **item**. Descobrir qualquer um deles custa, portanto, **uma requisição por
anúncio** — duas, para os dois.

No card de detalhe eles saem de graça, porque o card já consulta as duas rotas
para se montar. Na tabela de resultados, não: por isso a varredura fica atrás
de dois checkboxes desmarcados por padrão ("Verificar refino" e "Verificar
bônus aleatórios"), com o custo escrito ao lado assim que um deles é marcado.

Com a varredura ligada, a tabela deixa de agrupar só por item e passa a abrir
uma seção por combinação de item, refino e bônus — que é o que torna visível
*por que* três anúncios da mesma espada custam 129, 158 e 299 milhões. O botão
"+ Watchlist" de uma seção de refino conhecido já nasce com aquele refino
fixado.

Três coisas seguram o custo:

- **Memoização por anúncio.** Refino e bônus nunca mudam para um mesmo `ssi`
  (a loja fechada e reaberta ganha um `ssi` novo), então cada anúncio custa no
  máximo uma consulta de cada na vida do processo. Reordenar a tabela depois
  da varredura não custa nada.
- **Nada de consultar o que não pode ter refino.** Carta, consumível e
  material são pulados; numa busca ampla isso é metade dos resultados.
- **Aborto no primeiro erro.** Numa varredura de N anúncios, um erro é o site
  pedindo calma — insistir nos N-1 seguintes é o que transforma um tropeço em
  bloqueio. Os anúncios que ficaram sem resposta viram seções próprias,
  marcadas como não verificadas: "desconhecido" não é "+0", e misturá-los
  colocaria uma +10 de 300M dentro da seção "+0".

Esta feature já tinha sido tentada uma vez e desligada, justamente por bater
em 429. O que faltava então era a rede de proteção descrita em
[Rate limiting](#rate-limiting): hoje um 429 suspende as consultas em vez de
gerar mais.

### Versões do mesmo item que só diferem pelos slots

O site devolve o nome do item e o sufixo de slots em campos separados
(`itemName` + `slotMaxCount`, este já entre colchetes), e itens que só diferem
nisso são itens de catálogo **diferentes**, com `itemId` e preço próprios:
"Selo de Loki" (410232) e "Selo de Loki [1]" (410233). A tabela mostra o nome
completo — sem isso, as duas seções saíam com o mesmo cabeçalho repetido, sem
nada que explicasse a diferença de preço.

A watchlist guarda os dois nomes: o com slots é o que ela exibe, e o sem
slots é o que ela manda ao GnJoy ao consultar o preço — a busca do site casa
contra o nome cru do anúncio, então procurar por "Selo de Loki [1]" não
acharia nada.

### Outras versões do item, fora do mercado

Um termo de busca costuma casar mais itens do que o mercado tem anunciado no
momento: a "Caixa de Armadura" existe em +5, +7, +8, +9, +11, +12 e +13 no
servidor, e é normal que só uma ou duas dessas versões estejam à venda agora.
Quem procura justamente a +13 via a tabela com a +7 e não tinha por onde
acompanhar a que queria — a tela de histórico, que é onde as versões fora do
mercado aparecem com um botão de watchlist, só é mostrada quando a busca não
acha **nada**.

Abaixo da tabela de resultados há, por isso, um "Ver outras versões deste item
fora do mercado" (`GET /web/search/variants`), que consulta os preços
praticados de todo o histórico e lista o que casou com o termo e **não** está
na tabela acima — cada linha com um "+ Watchlist" em modo de disponibilidade.
É sob demanda, e não junto da busca, porque custa outra consulta ao site: o
link já leva os `itemId` que a tabela está mostrando, para descobrir a
diferença não exigir refazer a busca de mercado.

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

São sempre **duas** consultas, uma por janela (`period` aceita `1`, `7`, `30`
e `ALL`), porque elas respondem coisas diferentes:

- **`ALL`, o histórico completo, define QUAIS itens existem.** É ele que
  monta a lista. Usar a janela curta para isso escondia todo item que não
  vendeu na última semana: buscar "reformador primordial ii" mostrava só o
  "III", porque só ele tinha vendido nos últimos 7 dias — o "II" sumia da
  tela sem o usuário ter como saber que existe.
- **`7` refina os números.** Em um mercado volátil o mínimo de todo o
  histórico diz pouco sobre quanto o item custa hoje (para o "Reformador
  Primordial III", 38,5M no histórico contra 130M na semana).

`mergeHistoryRows` junta as duas: uma linha por item do histórico completo,
com os números da janela recente para quem vendeu nela. Quando as linhas
divergem de janela, a tabela ganha uma coluna "Período" dizendo de qual veio
cada uma — quando todas concordam, o texto acima da tabela já diz isso e a
coluna não é renderizada. Um item presente só na janela recente (o upstream
se contradizendo entre as duas consultas) também entra na tabela: a regra é
não perder item nenhum.

Falhar a consulta de `ALL` é fatal — sem a lista não há tabela. Falhar a de 7
dias não é: a tabela sai inteira com os números do histórico completo, e cada
linha já diz de qual janela veio. Se `ALL` vier vazio, o item nunca foi
vendido no servidor e a resposta é o aviso correspondente, sem tabela.

### Watchlist

Painel fixo do lado direito da página. Como a busca por palavra pode casar
itens de nomes diferentes (ex.: "Espada" acha "Espada Primordial", "Espada
Citadina" e "Carta Peixe-Espada"), a tabela de resultados agrupa os anúncios
por item — uma seção por nome casado, cada uma com seu próprio cabeçalho e
botão "+ Watchlist". Clicar nele adiciona exatamente o item daquela seção
(identificado por servidor + itemId, para não duplicar nem confundir com
outro item de nome parecido) a uma lista mantida inteiramente no navegador
via `localStorage` (`internal/web/static/watchlist.js`) — não há conta de
usuário nem persistência no servidor; a lista é local a cada navegador.

Cada linha da watchlist mostra:

- Uma luz verde (monitorando) ou vermelha (não monitorando), que alterna de
  estado ao clicar — é o que decide se o item participa da checagem
  periódica descrita abaixo.
- Uma alça (`⠿`) para reordenar a lista: arrastando, ou com as setas para
  cima e para baixo quando ela está com o foco. A ordem é a do próprio array
  no `localStorage`, que já era a ordem de exibição — não há campo novo nem
  migração das entradas existentes. Reordenar move o nó que já está na tela,
  sem reconstruir o painel: reconstruí-lo custaria uma consulta ao site por
  item a cada arrastar.
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
- Junto do badge, a localização da loja mais barata como um botão
  `/navi <mapa> <x>/<y>` — mesmo botão e mesma função `copyNavi` do card de
  detalhe da busca (ver acima): clicar copia o comando para a área de
  transferência. Só aparece enquanto o badge estiver visível (some de novo
  se o preço voltar a subir do alvo) — o servidor manda a localização em
  toda consulta que encontra um anúncio (ele não sabe qual é o alvo do
  usuário, que vive só no navegador), mas quem decide mostrar é o
  front-end.
- Um "↻" para consultar só aquele item na hora, ignorando o cache do
  servidor — a única forma de forçar uma atualização; não existe mais um
  botão que force a lista inteira (ver "Monitoramento e alertas" abaixo).
- Numa linha própria, para itens que são equipamento, **dois campos de bônus
  aleatório** editáveis do mesmo jeito (clicar, digitar, `Enter`; vazio
  remove a exigência). Quando o item entra pela busca com "Verificar bônus
  aleatórios" marcado, eles já nascem preenchidos com a combinação daquela
  seção — e a linha passa a acompanhar o anúncio mais barato **que tenha
  esses bônus**, não o mais barato do item. Como duas combinações do mesmo
  item são duas coisas diferentes de acompanhar, elas viram duas linhas
  (igual ao refino).

  A comparação é por texto, e o servidor só normaliza caixa e espaços — quem
  digitar a frase diferente da que o site usa não acha nada. Por isso os
  campos já nascem preenchidos: o valor de referência é o que veio da busca.
  O site expõe quatro bônus por anúncio, mas na prática só dois aparecem
  preenchidos, e dois campos bastam (`BONUS_FILTER_SLOTS`) — como o filtro
  exige presença, pedir dois de uma unidade que tem quatro continua achando
  ela.
- Um "×" para remover da lista.

O preço atual (e o refino) é a única parte que depende do servidor:
`GET /web/watchlist/price?server=...&itemId=...&item=...&refine=...&bonus=...`
(`refine` e `bonus` são opcionais) refaz a mesma busca por nome usada na
página principal, filtra pelo `itemId` exato (uma busca por nome pode casar
itens diferentes — ver teste com "Espada", que retorna itens com itemId
600009, 1147 e 4089) e:

- Sem filtro: pega o menor preço entre eles e busca o detalhe da loja mais
  barata via `GetStoreDetail`, de onde saem a localização e (se for
  equipamento) o refino — só informativos.
- Com `refine` e/ou `bonus`: ordena os candidatos por preço crescente e
  procura o primeiro que satisfaça o pedido. `bonus` é **repetido, uma vez
  por frase exigida** (`&bonus=CRIT+%2B4&bonus=ATQ+%2B3%25`) — as frases têm
  pontuação livre, e qualquer separador escolhido a dedo poderia aparecer
  dentro de uma delas. Todas as frases pedidas precisam estar no anúncio;
  bônus a mais nele não desqualificam, então preencher só um campo é um
  filtro mais frouxo.
- Nem o refino nem os bônus vêm na busca por nome: cada um custa um detalhe
  (`GetStoreDetail` e `GetItemDetail`, respectivamente). Ambos ficam
  **memoizados por anúncio** (nenhum dos dois muda para um mesmo `ssi` — a
  loja fechada e reaberta ganha um `ssi` novo) e cada checagem gasta **no
  máximo 8 consultas novas** (`maxDetailFetches`, contado em requisições e
  não em anúncios, já que um candidato com os dois filtros custa duas): a
  cobertura cresce a cada ciclo até abranger todos os anúncios, sem que um
  item popular custe dezenas de chamadas em cada checagem.
- O refino é conferido antes dos bônus de propósito: reprovar por ele
  dispensa a segunda consulta daquele candidato.
- Enquanto a varredura não tiver alcançado todos os anúncios, a resposta vem
  com `partial: true` e a linha mostra "Verificando…" em vez de "Sem
  anúncios" — a diferença entre "ninguém anuncia isso" e "ainda não cheguei
  lá".
- A resposta também traz `equipment`, que diz se o item aceita bônus
  aleatórios (e portanto se a linha oferece os campos). Vem do
  `databaseType`, que a busca já devolve, então não custa requisição — e é
  informado mesmo quando o filtro não acha nada, senão a linha perderia os
  campos justamente quando o usuário precisa corrigir o que digitou.

As consultas do frontend passam por um cache de resultados no servidor
(`internal/web/cache.go`, TTL + deduplicação de buscas concorrentes via
`singleflight`): a checagem automática da watchlist aceita um resultado de
até 4 minutos (`monitorMaxAge` — como o array da watchlist mora em
`localStorage`, compartilhado entre abas da mesma origem, várias abas tendem
a escolher o mesmo item a cada tick, e é esse cache que absorve a
sobreposição), e a busca/histórico interativos aceitam até 30 segundos
(`freshMaxAge` — um F5 ou um clique de ordenação não vão ao upstream de
novo). O botão "↻" de cada linha da watchlist envia `fresh=1`, que ignora o
cache: quem apertou quer o estado de agora.

Ligar/desligar, editar o alvo e remover um item são só atualizações de
`localStorage` + DOM, sem chamada ao servidor. Editar o refino fixado dispara
uma nova consulta de preço — o filtro mudou, o preço em cache não serve mais.
Adicionar um item novo busca o preço só dele (os demais já carregados não são
recarregados).

**Limite de 50 itens.** O ritmo automático é sempre de UMA consulta por
minuto, revezando entre os itens monitorados (ver "Monitoramento e alertas"
abaixo) — o intervalo entre duas consultas do MESMO item cresce com o
tamanho da lista, aproximadamente `N × 1 min`. Sem teto, uma watchlist muito
grande faria cada item demorar cada vez mais para ser reconsultado; 50 itens
já significa quase uma hora entre uma consulta e a seguinte do mesmo item —
grande o bastante para acompanhar listas razoáveis sem o intervalo virar
impraticável. Ao esbarrar nele, um toast explica e nada é adicionado.

**Expandir o painel.** O botão "«"/"»" no cabeçalho (ao lado do cronômetro)
esconde a coluna de resultados e faz a watchlist ocupar a largura inteira da
página — útil para quem acompanha muitos itens; a grade de cartões
resultante usa o espaço bem melhor que uma coluna de 300px. A escolha fica
salva em `localStorage` e é reaplicada antes da primeira pintura (o mesmo
truque do tema, para não saltar de layout a cada carregamento).

Sem nenhuma escolha salva ainda, o painel aparece expandido por padrão
sempre que a área de resultados está vazia (todo carregamento novo da
página começa assim, já que nenhuma busca foi enviada ainda) — não há por
que reservar espaço para uma tabela que não existe. Isso não é uma
preferência gravada, só um padrão: a primeira busca enviada recolhe a
watchlist sozinha (via o evento `htmx:beforeRequest` do formulário), e um
próximo carregamento sem busca volta a mostrar o painel expandido. Uma
preferência explícita (o usuário já clicou no botão alguma vez) sempre vale
sobre esse padrão, buscas ou não.

Como a troca de layout reorganiza `grid-template-areas` — algo que nenhum
navegador sabe interpolar entre um estado e outro —, a transição entre os
dois modos ganha um fade curto (0.18s) nos painéis que trocam de lugar, via
uma classe transitória (`watchlist-layout-mudando`) que o próprio evento
`animationend` remove ao final; respeita `prefers-reduced-motion`.

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

Enquanto a página estiver aberta, a cada minuto (`MONITOR_TICK_MS` em
`watchlist.js`) UM item entre os com o indicador ligado (verde) tem o preço
reconsultado — nunca a lista inteira de uma vez. O escolhido é sempre o que
está há mais tempo sem consulta (`pickNextEntry`: o `lastCheckedAt` mais
antigo entre os monitorados, nunca consultado contando como o mais antigo de
todos, com empate desfeito pela ordem de exibição); isso faz os itens
revezarem sozinhos, sem um cursor/fila separado para acompanhar
adições e remoções — remover um item da lista simplesmente tira-o da
disputa, e um item recém-adicionado (ou recolocado) entra na frente por
nunca ter sido consultado. É esse rodízio, e não mais um ciclo de vários
minutos despachando tudo de uma vez em série, que garante o ritmo constante
de uma requisição por minuto não importa quantos itens a watchlist tenha.

Cada linha nasce com o último resultado conhecido — persistido na própria
entrada (`lastResult`, junto de `lastCheckedAt`) — pintado na hora, sem
nenhuma requisição; a única consulta de verdade do carregamento é a do item
escolhido pelo rodízio, disparada imediatamente (sem esperar o primeiro
minuto). O botão "↻" de cada linha (ver acima) força uma consulta só
daquele item a qualquer momento, sem tocar no cronômetro nem no item que o
tick automático escolheria a seguir — o `lastCheckedAt` dele também é
atualizado, então ele naturalmente sai da frente da fila até voltar a ser o
mais antigo.

Se a condição que o item acompanha passar a valer, o usuário é avisado de
duas formas:

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

Itens com o indicador desligado (vermelho) continuam na lista, mas não
disputam a vez do rodízio nem podem disparar aviso enquanto assim
permanecerem — eles ainda têm o preço atualizado ao carregar a página (do
cache) ou ao serem adicionados, só não voltam a ser consultados
automaticamente enquanto desligados.

Como a watchlist vive só no navegador, o monitoramento também só roda
enquanto uma aba com a página estiver aberta; fechar a aba interrompe as
checagens até ela ser reaberta (quando o rodízio retoma de onde parou,
graças ao `lastCheckedAt` persistido, e a primeira consulta sai na hora, sem
esperar o primeiro minuto). Várias abas abertas rodam cada uma o próprio
tick, mas como o array da watchlist mora no mesmo `localStorage`
compartilhado entre elas, tendem a escolher o mesmo item por vez — e o
cache do servidor absorve essa sobreposição, sem coordenação explícita entre
abas.

O HTMX é vendorizado localmente em `internal/web/static/htmx.min.js`
(embutido no binário via `go:embed`) — não depende de CDN em runtime.

### Tema (claro/escuro)

Segue a preferência do sistema (`prefers-color-scheme`) por padrão. O botão
no topo da página força um dos dois independente do sistema, guardando a
escolha em `localStorage` (`internal/web/static/theme.js`); um script
inline em `index.html.tmpl`, antes do `<link>` da folha de estilo, aplica
essa escolha antes da primeira pintura, para não piscar o tema errado a
cada carregamento. Todas as cores do `style.css` são variáveis (`--bg`,
`--text`, `--accent` etc.) redefinidas para os dois modos — não há cor fixa
fora desse bloco de tokens, propositalmente: foi assim que uma cor "só do
modo claro" (`--bg-card`) acabou vazando para o modo escuro antes desta
seção existir, apagando o texto de cima dela.

### Versão em execução

Um canto discreto da tela (`internal/web/static/version.js`) mostra a
versão do binário — o mesmo valor de `main.version`, injetado em tempo de
build (ver [CI/CD](#cicd)) e "dev" em builds locais. Ao carregar a página, o
navegador consulta, direto do lado do cliente, a API pública do GitHub
(`GET /repos/lbcosta/ro-market-tracker/releases/latest`) e mostra um link
para a release quando ela é mais nova que a versão em uso.

A consulta não passa pelo servidor Go nem pelo rate limiter do
`gnjoy.Client` — não tem nada a ver com o site do jogo — e fica em cache no
`localStorage` por 6 horas, para recarregar a página com frequência não
gastar a cota anônima da API do GitHub (60 requisições/hora por IP). Uma
build "dev" (sem tag) não tem com o que comparar e nunca mostra o aviso;
qualquer falha na consulta (sem internet, GitHub fora do ar, cota
esgotada) é silenciosa — é só um aviso de conveniência.

Variáveis de ambiente (todas opcionais):

| Variável                             | Padrão                                | Descrição                                          |
|---------------------------------------|----------------------------------------|-----------------------------------------------------|
| `PORT`                                | `8080`                                 | Porta HTTP do servidor                               |
| `GNJOY_BASE_URL`                      | `https://ro.gnjoylatam.com`            | Domínio base do site do GnJoy LATAM                  |
| `GNJOY_LOCALE`                        | `pt`                                   | Locale usado nas rotas (`pt`, `en` ou `es`)          |
| `GNJOY_ACTION_ID`                     | ver `gnjoy.DefaultActionID` no código  | Hash da Next.js Server Action (ver aviso abaixo)     |
| `GNJOY_RATE_LIMIT_RPS`                | `1` (`gnjoy.DefaultRateLimitRPS`)      | Requisições por segundo permitidas ao upstream       |
| `GNJOY_RATE_LIMIT_BURST`              | `1` (`gnjoy.DefaultRateLimitBurst`)    | Rajada inicial permitida acima do ritmo sustentado   |
| `GNJOY_SUSPENSION_PROBE_INTERVAL`     | `10m`                                  | Intervalo da sonda de recuperação após um `429` (ver [Suspensão](#suspensão-quando-o-429-não-é-um-tropeço)) |
| `GNJOY_DUMP_ACTIONS`                  | desligado                              | `1` registra no log a resposta crua do site (ver [Inspecionar a resposta crua](#inspecionar-a-resposta-crua-do-site)) |

### Inspecionar a resposta crua do site

As respostas do GnJoy são decodificadas com `json.Unmarshal` sem
`DisallowUnknownFields`, então **um campo que o site mande e o programa não
conheça some sem deixar rastro**. Quando a pergunta é "o site expõe X?", não
dá para responder olhando as structs — só olhando o dado real.

Subir com `GNJOY_DUMP_ACTIONS=1` faz cada Server Action (`store`, `item`,
histórico de preço) registrar no log o JSON que veio, antes de ser
decodificado:

```
gnjoy: resposta crua da action action=item params=map[...] data={"itemId":...}
```

O dump sai junto dos `params`, para dar para saber de qual anúncio é cada
linha quando há várias. **Expandir uma linha da busca** é o gesto que dispara
as duas actions de detalhe (`store` e `item`) de uma vez. Fica desligado por
padrão: o log é volumoso e traz a resposta inteira do upstream.

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
- Um `429` também estabelece uma **calmaria global** no client
  (`extendCooldown`): até o fim do `Retry-After`, nenhuma outra chamada
  dispara — as que já estavam na fila aguardam em vez de cada uma descobrir
  o bloqueio tomando o próprio `429` e gastando as próprias tentativas.
  Quando a calmaria acaba, as chamadas represadas saem espaçadas pelo
  limiter, não todas de uma vez.
- Chamadas de fundo com repetição própria — a checagem periódica da
  watchlist, o aquecimento do action id — usam `gnjoy.NoRetry()`: no
  primeiro `429` elas desistem (o ciclo seguinte refaz a consulta), em vez
  de insistir disputando a cota com as ações que o usuário está esperando.
  Pelo mesmo motivo, a varredura de chunks da redescoberta de action id é
  **abortada inteira** no primeiro `429`.
- Do lado do frontend, o cache de consultas do servidor web (ver a seção da
  watchlist) reduz quantas requisições sequer chegam a essa fila.

### Suspensão: quando o `429` não é um tropeço

A calmaria acima resolve o `429` passageiro — ela segura as chamadas pelo tempo
que o próprio site pediu e a vida segue. O que ela não resolve é o `429` que
significa "sua cota acabou": aí o site recusa tudo por um tempo que ele não
informa, e continuar mandando requisições só prolonga o bloqueio.

Por isso o binário sobe com `gnjoy.WithSuspendOn429()`. Com ela, o **primeiro**
`429` fecha a porta:

- As chamadas seguintes falham na saída com `gnjoy.ErrSuspended`, sem tocar no
  site — e o retry da chamada que tomou o `429` é cortado junto.
- A tela mostra um aviso fixo no topo e desliga a busca e a watchlist —
  inclusive o cronômetro dela, que some (`--:--`) em vez de continuar
  contando: um cronômetro correndo normalmente durante o bloqueio dava a
  falsa impressão de que a checagem automática seguia rodando, quando cada
  disparo dela era só recusado pelo servidor sem custar nada. O estado
  chega ao navegador pelo mesmo stream SSE da barra de atividades, como
  estado completo (nunca diferença), então uma aba nova ou uma reconexão
  resincronizam o aviso sozinhas.
- Uma sonda gasta **uma** requisição a cada 10 minutos
  (`GNJOY_SUSPENSION_PROBE_INTERVAL`) e é a única que atravessa. Quando o site
  responde qualquer coisa que não seja `429`, tudo é liberado sozinho e a
  watchlist faz uma checagem imediata.

A option é opt-in porque muda o contrato de quem usa o pacote como biblioteca:
sem ela, o `Client` continua insistindo com backoff, que é o certo quando quem
chama trata o erro por conta própria.

Dois detalhes da implementação que não são opcionais (ver
`internal/gnjoy/suspend.go`):

- **Contagem de gerações no release.** Uma requisição admitida *antes* do `429`
  pode responder `200` depois dele; sem comparar a geração, essa resposta velha
  desfaria uma suspensão recém-criada.
- **Reconferência dentro da espera.** Uma chamada estacionada numa calmaria de
  dois minutos dispararia assim que ela acabasse, mesmo já suspensa — que é
  exatamente o tráfego represado que a suspensão existe para impedir.

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

É a única rota que expõe os **bônus aleatórios** da unidade anunciada, nos
campos `randomOpt1..4` (frases prontas no idioma pedido em `lang`, ex.:
`"CRIT +4"`; `null` nos que não existem). Como o refino, eles não aparecem na
busca — e é por isso que o frontend só os mostra no card de detalhe de um
anúncio, onde essa consulta já acontece de qualquer forma.

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
- O backend de busca só aceita **letras, dígitos e espaços** no termo
  procurado. Qualquer outro caractere — hífen, "+", parênteses, colchetes,
  ponto, apóstrofo — faz as duas páginas de busca responderem 200 com a
  página de erro do próprio site no lugar da lista, o que deixaria itens
  reais impossíveis de buscar e de acompanhar ("Módulo de S-Rapidez",
  "Caixa de Arma +13", "Pedra de Mestre II (Baixo)", "[Visual] Chapéu
  Confeitado"). O client contorna isso enviando o maior trecho aceitável do
  termo e filtrando a resposta pelo termo inteiro
  (`internal/gnjoy/searchword.go`); o mock reproduz a recusa, para que um
  retrocesso no contorno apareça nos testes em vez de na mão do usuário.