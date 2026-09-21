# ro-market-tracker

[![CI](https://github.com/lbcosta/ro-market-tracker/actions/workflows/ci.yml/badge.svg)](https://github.com/lbcosta/ro-market-tracker/actions/workflows/ci.yml)

Tracker de preços do mercado RO LATAM

Client HTTP em Go, com uma API REST própria e um frontend em HTMX, que
consulta as rotas internas do site do GnJoy Americas (reverse-engineered a
partir do DevTools, ver anexo no fim deste arquivo) e expõe os dados de
mercado.

## Estrutura do projeto

```
cmd/server/main.go              binário do servidor HTTP
internal/gnjoy/                 client para as rotas internas do GnJoy Americas
  client.go, flight.go            requisições, rate limiting, parser do formato RSC Flight
  discover.go                     descoberta/auto-refresh do action id da Server Action
  refine.go, types.go             parsing de refino, tipos de dados devolvidos pelo client
  searchword.go                   contorno da pontuação que o backend de busca recusa
  suspend.go                      suspensão de todas as consultas após um 429
internal/api/                   API REST própria (JSON) — handlers + roteador
internal/web/                   frontend HTMX — handlers + roteador
  templates/                      layout (cabeçalho/rodapé comuns), as duas páginas e os fragmentos de busca/expand
  static/                         CSS, JS (app.js, monitor.js, watchlist.js, sugestao.js, estoque.js, navegacao.js, theme.js, activity-bar.js, version.js) e htmx.min.js vendorizado
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

**Nenhum teste toca a API real.** O site do GnJoy Americas tem rate limiting
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

### As duas páginas

O site tem duas páginas, alternadas pelo menu em abas logo abaixo do
subtítulo:

| Aba | Rota | Conteúdo |
| --- | --- | --- |
| Estoque | `GET /estoque` | o que o usuário vende: cadastro, preço de venda e (em construção) os dados do mercado |
| Watchlist | `GET /{$}` | busca + watchlist (descritos no resto desta seção) |

**Trocar de aba não recarrega o documento.** Só o `#page-content` é
substituído (`internal/web/static/navegacao.js`); cabeçalho, menu, barra de
atividades e a versão no canto ficam no mesmo DOM do começo ao fim da sessão.
Isso não é preferência de estilo — é requisito. Quem fala com o site da GnJoy
é um motor só, no servidor, e recarregar a página a cada clique no menu
quebrava três coisas ao mesmo tempo:

- **Uma conexão SSE nova por navegação** (medido: duas por ida e volta entre
  as abas), com as anteriores penduradas até o navegador recolhê-las. O
  HTTP/1.1 permite seis conexões por host, então depois de algumas trocas a
  próxima página ficava esperando um socket vagar — carregando para sempre.
- **O log do rodapé zerado a cada troca**, sendo que ele é justamente o
  histórico do que o motor compartilhado andou fazendo.
- **O rodízio de preços reiniciado**: cada volta à aba Watchlist gastava uma
  consulta ao site e recomeçava o cronômetro de um minuto do zero. Trocar de
  aba rápido consultava muito mais que uma vez por minuto — caminho curto
  para o `429`.

Como funciona: os links do menu são `href` comuns (sem JS, a navegação é a do
navegador e a página inteira chega do servidor). O `navegacao.js` intercepta o
clique, faz `history.pushState` e pede o corpo da página via `htmx.ajax`. O
servidor responde ao mesmo `GET /` ou `GET /estoque` com o documento inteiro
numa navegação comum e só com o corpo quando o cabeçalho `HX-Request` está
presente — um HTML só para os dois casos, então abrir a URL direto e chegar
pelo menu dão a mesma tela.

O menu mora no cabeçalho, fora do pedaço trocado, e volta junto da resposta
com `hx-swap-oob`: qual aba está aberta continua sendo decisão do servidor.
O histórico (`pushState`/`popstate`) é tocado à mão em vez de com
`hx-push-url` porque o histórico do htmx trabalha no `<body>` inteiro — no
"voltar" ele restauraria o body a partir de um snapshot e levaria junto a
barra de atividades.

Depois de cada troca, `religarCorpo()` redesenha o painel da watchlist a
partir do `localStorage` e reaplica o estado de suspensão: o corpo que chega
é DOM novo, sem linhas e sem ouvintes.

O cabeçalho (título, subtítulo, botão de tema e o menu) e o rodapé (barra de
atividades e versão) são os mesmos nas duas páginas: ficam em
`templates/layout.html.tmpl`, como `{{define}}` que cada página inclui. Não é
um layout que envolve o conteúdo porque `{{template}}` não aceita nome
dinâmico — cada página nova precisaria ser listada em um if/else dentro do
layout.

### Estoque

O outro lado do programa: a busca e a watchlist servem a quem **compra**, o
Estoque serve a quem **vende**. O usuário digita o nome de um item que anuncia,
aperta Enter, e ele entra na lista.

Como a watchlist, o estoque vive inteiro no `localStorage`
(`ro-market-tracker:estoque`) — não há conta de usuário nem persistência no
servidor, e a ordem do array é a ordem da tela. O servidor da loja é da tela
inteira (`ro-market-tracker:estoque-servidor`), porque a loja de alguém fica em
um servidor só, mas é gravado também em cada item para uma troca futura não
corromper os que já existem.

Cada card mostra:

- O nome — o que o usuário digitou até a validação acontecer, e o nome canônico
  depois dela. Preservar o digitado é o que deixa ele reconhecer a linha que
  precisa corrigir quando o nome está errado.
- Um selo de validação: **NÃO VALIDADO**, **VALIDADO** ou **INVÁLIDO** (este
  em vermelho, com o motivo logo abaixo — é o único estado que exige ação do
  usuário). Ver "Validar um item" abaixo.
- **Vendo por:**, o preço que o usuário está pedindo, editável clicando — mesmo
  gesto e mesma receita do "Alvo:" da watchlist.
- **Na loja / Fora da loja** e o **sino** do undercutting, ligados pelo
  usuário. O sino fica desabilitado enquanto o item está fora da loja: não há o
  que comparar com o mercado se você não está vendendo. Sair da loja desliga o
  sino junto, para ele não voltar a valer sozinho na próxima vez que o item
  entrar. Ver "Undercutting ativo" abaixo.
- A **janela do histórico** (último dia / 7 dias / 30 dias / todo o histórico),
  **por item**. Ela é por item, e não da tela, porque trocá-la custa uma consulta
  ao site: um seletor global cobraria isso de todos os itens de uma vez e
  estouraria o ritmo de uma requisição por minuto que o programa respeita.

Adicionar, editar o preço, ligar as flags, trocar a janela e excluir são só
`localStorage` + DOM, **sem nenhuma requisição ao GnJoy** — o que os testes
conferem com o contador de requisições ao upstream, porque "zero requisição"
aqui é requisito, não acaso. Falam com o site só os cliques em "Validar", "↻"
e na janela do histórico, e o rodízio, que reconsulta o mercado dos itens na
loja (ver "Undercutting ativo").

#### Validar um item

`GET /web/estoque/validar?server=…&item=…` responde quais itens do servidor
casam com o nome digitado. A ordem das duas consultas não é arbitrária:

1. **Mercado** (`SearchShops`) — os anúncios da concorrência agora. É o caminho
   comum, e o único que traz o nome com o sufixo de slots, o que separa
   "Selo de Loki" de "Selo de Loki [1]" (itens de catálogo diferentes, com
   preços muito diferentes).
2. **Histórico** (`SearchMarketPrice`, janela `ALL`) — só se ninguém no
   servidor inteiro estiver anunciando o item. "Ninguém está anunciando agora"
   não quer dizer "não existe", e é justamente o caso de quem vai colocar esse
   item à venda.

É a mesma cadeia que a busca da página principal já faz, com uma diferença: o
fallback chama `searchMarketPrice` direto em vez de `priceHistory`, que
consultaria DUAS janelas para montar uma tabela que esta rota não mostra.

Custo: **1 requisição** quando alguém anuncia o item, **2** quando não; zero em
qualquer repetição dentro de 30 s (as duas rotas passam por cache). Não existe
"validar todos" de propósito — é o atalho que mais convida ao `429`, e o botão
por item já limita o ritmo naturalmente.

Os três desfechos:

| Resultado | Estado do card |
| --- | --- |
| 1 candidato | **VALIDADO** na hora, fixando `itemId` e `svrId` |
| vários candidatos | um `<select>` com os candidatos e um botão de confirmar, dentro do card |
| nenhum candidato | **INVÁLIDO**, com o motivo |

E o caso que não é desfecho nenhum: **falha ao consultar não mexe no estado do
item.** Inválido é o que manda o usuário apagar o cadastro, e um timeout ou um
bloqueio do site não podem mandar isso — por isso essas situações respondem
`502` (ou `503` sob suspensão) em vez de uma lista vazia, e o card fica como
estava, com um toast explicando. Um item inválido também pode ser revalidado
pelo botão, que vira "Tentar de novo": custa uma requisição (zero dentro do
cache), então obrigar a recadastrar por causa de um tropeço seria gratuito.

A escolha é um dropdown, e não uma lista de botões, porque os cards dividem
uma grade: uma lista crescia o card em uma linha por candidato e desalinhava a
linha inteira. O dropdown tem altura fixa — dois candidatos ou dez, o card
mede o mesmo. Como dentro de um `<option>` não há formatação, o resumo
(itemId e faixa de preço) entra no próprio texto: sem ele, dois candidatos
vindos do histórico apareceriam com o mesmo nome e seriam indistinguíveis.

A lista de candidatos é persistida no `localStorage`: trocar de aba no meio da
escolha e voltar não pode custar outra consulta ao site.

#### O que o mercado está pedindo

`GET /web/estoque/mercado?server=…&itemId=…&item=…[&fresh=1]` devolve os
anúncios do item **agora**, do mais barato ao mais caro, cada um com preço,
quantidade, nome da loja e **nome do vendedor**. Custo: 1 requisição, ou zero
quando o cache de 4 minutos responde; `fresh=1` (o botão "↻" do card) o ignora.

A rota **não** chama `GetStoreDetail`. A da watchlist gasta uma requisição
extra por consulta só para obter o `/navi` da loja mais barata — quem tem o
item no estoque não vai a lugar nenhum, vai comparar preço, e o nome da loja
já vem de graça em cada linha da busca. Há um teste que trava isso.

A lista é cortada em 60 anúncios (`maxAnunciosPorItem`), e o card diz quando
cortou. Como ela vem ordenada, o corte só descarta o que ninguém ia olhar.

#### Meus personagens

Uma lista separada, no topo da tela (`ro-market-tracker:meus-personagens`),
com os nomes dos personagens que vendem na sua loja. Anúncios feitos por eles
não contam como concorrência — sem isso você apareceria competindo consigo
mesmo, e o aviso de undercutting dispararia por causa do seu próprio anúncio.

**A separação acontece no navegador, não no servidor.** É uma decisão de
custo: se o servidor fizesse o desconto, editar a lista obrigaria cada card a
reconsultar o site — vinte itens no estoque seriam vinte requisições e vinte
segundos de fila. Por isso a rota devolve o vendedor de cada anúncio e o
cliente filtra: mexer na lista recalcula a tela inteira **de graça**, o que os
testes conferem com o contador de requisições.

A comparação é por nome, insensível a caixa e com as pontas aparadas. Cadastre
todos os personagens: um nome faltando faz o desconto falhar em silêncio.

Com isso, o card mostra uma de três situações, que a interface não confunde:

| Situação | O que o card diz |
| --- | --- |
| ninguém anuncia | "Ninguém está anunciando este item." |
| só você anuncia | "Você é o único anunciando este item." (em verde) |
| há concorrência | o menor preço **de terceiros**, com unidades e anúncios |

E, quando algum anúncio é seu, uma linha a mais com o seu preço — em vermelho
quando alguém está abaixo dele.

#### Histórico de vendas e a janela

`GET /web/estoque/historico?itemId=…&svrId=…&janela=1|7|30|ALL[&fresh=1]`
devolve a série diária de vendas do item: um dia por linha, com mínimo, médio,
máximo e quantidade vendida. **Vai completa, sem corte** — o site só devolve
dias que tiveram venda, e são esses números que vão alimentar a fórmula de
preço sugerido.

**O seletor usa o `limit`, não o `period`.** O site pagina a série diária pelo
`limit` (comprovado em `docs/webtools-api-research.md`), enquanto o `period`
desta Server Action nunca foi observado — a captura original mandava o literal
`$undefined` do Next.js. Depender dele seria depender de comportamento que
ninguém confirmou.

A janela **é por item**, no card. Trocá-la custa uma requisição, e um seletor
global cobraria isso de todos os itens de uma vez.

A janela "todo o histórico" é a única sem um número fixo: quantos dias existem
só se descobre perguntando. Ela sonda com 30 dias, lê o `TotalCount` que vem
em cada linha e, se houver mais, busca o total — **1 ou 2 requisições, e só na
primeira vez do item**, porque depois o cache responde. Há um teto de 365 dias
(`maxDiasDoHistorico`): nem o tamanho que o site aceita nem o custo de uma
página enorme foram medidos contra o real, então "tudo" é melhor esforço.

O cache é novo (`priceHistoryCache`, validade de 10 minutos) e tem o `limit` na
chave, porque a resposta de 7 dias e a de 30 são valores diferentes do mesmo
item. Sem ele, alternar entre janelas e voltar custaria uma requisição a cada
ida e volta.

No card, o resumo da janela fica sempre visível numa linha e a **tabela de dias
fica recolhida**: a janela de 30 dias pode ter dezenas de linhas, e os cards
dividem uma grade — um card aberto esticaria a linha inteira. O resumo do
`<summary>` diz quantos dias vieram e quantos existem ("7 dias com venda de 32
registrados"), que é o que avisa que trocar para uma janela maior tem o que
mostrar.

Como a tela passou a usar a Server Action `price`, `Handler.Estoque` agora
aquece o action id, como a Watchlist já fazia.

#### Preço sugerido: faixa e cenários, não um número

A decisão de quem vende não é "qual é o preço justo" — é um trade-off: preço
alto pode não vender, preço baixo vende rápido e deixa dinheiro na mesa. Um
número sozinho esconderia exatamente a escolha. Então o card mostra uma
**faixa**, um **selo de confiança** e, dentro de um `<details>`, **três
estratégias**, cada uma com o tempo esperado até vender e a aposta que embute.

Tudo isso é calculado no navegador (`internal/web/static/sugestao.js`), em
funções puras sobre dados que o card já tem — por isso mexer no seu preço, na
lista de personagens ou na janela recalcula a tela inteira **sem requisição
nenhuma**.

##### O defeito do dado de origem, que manda em tudo

O histórico é indexado por `itemId`, mas **refino e encantamento são
propriedades da unidade**. Uma bota +0 e uma bota +9 com encantamento raro são
o mesmo `itemId`, então o histórico de um equipamento não é uma distribuição —
são várias empilhadas, e a média cai no vazio entre elas. O site não expõe o
que a unidade tinha quando foi vendida: isso não tem conserto, tem tratamento.

1. **`databaseType` separa quem sofre do problema** (`weapon`, `armor`) de quem
   não sofre (consumíveis, cartas, materiais).
2. **Medianas dos mínimos e dos máximos diários no lugar da média.** O mínimo
   diário é o mais perto que se chega da versão comum do item; o máximo, do que
   uma unidade boa alcança. Mediana e não média porque um único dia de 88kk
   destrói uma média e mal move uma mediana.
3. **Os anúncios de agora revelam as faixas.** Um vazio grande entre preços
   ordenados é o próprio mercado dizendo que ali há duas coisas diferentes à
   venda. Não afirmamos *por quê* — só que está partido. Custo zero.
4. **A confiança é medida e exibida com o motivo**, que é o que conta ao
   usuário que os números somam unidades diferentes da dele.

##### A precisão da resposta acompanha a confiança do dado

Um "~3 dias" calculado sobre dados contaminados é uma mentira com cara de
precisão. Por isso:

| Confiança | Tempo até vender | Cenários |
| --- | --- | --- |
| alta | `~3 dias` | sim |
| média | `entre 2 e 5 dias` | sim |
| baixa | `provavelmente semanas` | **não** — só a faixa e o motivo |
| nenhuma | — | não |

##### A sua posição é um fato, não uma estimativa

A linha do seu preço abre com dois números **exatos** — a colocação na fila e a
distância do concorrente mais barato — e só depois vem a estimativa de tempo:

```
Seu preço: 3º de 4 anúncios · 54% acima do mais barato · provavelmente rápido
```

A ordem importa. Os baldes grosseiros do tempo existem para não mentir sobre
precisão, mas sozinhos eles quase não se mexem quando o usuário muda o preço —
e o preço é justamente o que ele está decidindo. O "Sugerido", por definição,
também não depende do que você está pedindo. Sem a colocação, um equipamento
de confiança baixa dava a impressão de que o card estava inerte.

A colocação é contagem de anúncios: não depende de liquidez, de histórico nem
da confiança do dado, e por isso aparece **sempre**, inclusive nos itens em que
nenhum preço é recomendado.

##### Tempo até vender

```
vendas por dia     = quantidade vendida na janela ÷ dias com venda
fila na sua frente = unidades anunciadas mais baratas que a sua
dias até vender   ≈ fila ÷ vendas por dia
```

A suposição embutida é que se compra do mais barato para o mais caro, o que é
aproximadamente verdade no jogo. **A fila é recalculada a cada repintura**,
nunca guardada: o mercado é dinâmico e a fila de agora não é a de dez minutos
atrás.

Para equipamento, a liquidez também está contaminada — o volume é de todas as
unidades do `itemId`. A correção é escalá-la pela fatia do mercado que é
comparável: se 2 dos 10 anúncios estão na sua faixa, assume-se que ~20% das
vendas são dela. É uma suposição, e está dita na tela.

##### Régua de preços

Uma faixa horizontal com um tique por anúncio, posicionado pelo preço, e um
marcador destacado no seu. Um objeto só responde quatro perguntas que em texto
custariam quatro linhas: quantos concorrentes, a que preços, onde você está, e
se o mercado está partido. A escala é **logarítmica**: num item cujos anúncios
vão de 200k a 88kk, uma escala linear empilharia os baratos num pixel e
esconderia justamente a estrutura que a régua existe para mostrar.

#### Aviso de loja fora do ar

Quando **nenhum** dos itens marcados como "na loja" tem anúncio seu no mercado,
um aviso aparece no topo do Estoque. O caso que ele existe para pegar é a
desconexão silenciosa: a loja caiu e você não viu.

Ele **não afirma "loja offline"**. Uma loja fechada e um estoque que vendeu
tudo somem do mercado exatamente igual, e o site não distingue os dois —
cravar a causa mandaria o usuário conferir a loja à toa. O texto descreve o que
foi observado, diz que as duas causas são possíveis e informa de quando são as
checagens.

Só dispara com sinal forte: personagens cadastrados (sem eles, "nenhum anúncio
seu" não significa nada), ao menos um item validado e na loja, e evidência de
até 15 minutos — um item consultado há quarenta minutos não diz nada sobre
agora. Um único item com anúncio seu já basta para não alarmar. Custo: zero
requisição, a evidência já está no `lastResult` de cada card.

#### Resumo do estoque

Com nenhum item aberto, o painel da direita responde pelo estoque inteiro:
uma linha por situação (perdendo, empatado, na frente, na fila, sem dados),
só com as que têm itens. Clicar numa linha abre o primeiro item dela. As
pílulas do topo somem nesse estado, porque repetiriam o resumo ao lado dele.
O botão **← Resumo do estoque** do card volta para cá: cadastrar um item já o
abre, e sem esse caminho o resumo sumiria depois do primeiro cadastro.

Duas ações em lote:

- **Reprecificar os N**, na linha de quem está perdendo. O Reprecificar de um
  item grava o preço e o copia para a área de transferência, porque quem
  aplica o preço na loja é você, dentro do jogo. A área de transferência
  guarda um valor só, então o lote é uma **lista de conferência**: um clique
  por item, cada um gravando e copiando o seu preço, e a linha marcada quando
  é feita. Gravar os N de uma vez faria o programa acreditar em preços que
  ninguém colou em lugar nenhum. O preço de cada linha é o da tarja do item,
  calculado na hora, e não congelado ao abrir a lista. A lista vive em
  memória: abrir um item a encerra, e reabri-la é de graça.
- **Validar agora**, na linha de quem está sem dados. Ele passa pelo mesmo
  aviso de custo e pela mesma fila do "Validar tudo", mas só para os itens que
  validar resolve. Um item sem preço ou sem anúncios continuaria igual, e um
  inválido já ouviu das duas consultas que não existe.

Desenhar o resumo não custa requisição nenhuma, e ele acompanha o rodízio: um
item que muda de situação muda de linha sem ninguém clicar.

Durante uma suspensão, os controles do estoque que consultam o site (Validar,
↻, a janela do histórico, a escolha de candidato e o Validar tudo) ficam
travados. O painel é redesenhado a toda hora, então a trava é reaplicada a
cada redesenho. Editar o preço, pôr na loja, o sino e reprecificar são locais
e continuam livres.

#### Custo de validar um item, ponta a ponta

| Situação | Requisições |
| --- | --- |
| Item que alguém anuncia | **3** — validar + mercado + histórico |
| Item só no histórico | **3** — validar (duas consultas) + histórico |
| Item inexistente | **2** — as duas consultas da validação |

O item achado só no histórico não paga a terceira: a validação já provou que
ninguém está anunciando, então o resultado de mercado é semeado à mão em vez
de ser perguntado de novo.

Depois disso, um item fora da loja só volta a consultar quando o usuário aperta
"↻" (o mercado) ou troca a janela (o histórico). Um item **na loja** entra no
rodízio automático, descrito a seguir.

#### Undercutting ativo

Os itens validados e **na loja** entram no rodízio compartilhado da watchlist
(ver "Monitoramento e alertas"). É o mesmo relógio, com uma consulta por
minuto no total, e não uma a mais. Cada volta consulta só o mercado do item:
o histórico diário muda no máximo uma vez por dia, e consultá-lo a cada volta
dobraria o custo.

Com o **sino** ligado, o usuário é avisado pelos mesmos quatro canais da
watchlist (toast, som, Telegram e notificação nativa) quando alguém **passa a**
vender abaixo do preço dele:

- **Estritamente abaixo.** Empatar no menor preço não é ser cortado. A tabela
  já mostra isso como "empatado".
- **Os seus anúncios não contam.** O seu anúncio antigo, mais barato que um
  preço novo, não dispara aviso contra você mesmo.
- **Um aviso por cruzamento.** Enquanto o corte continuar, o rodízio não repete
  o aviso a cada volta. Quando o corte acaba e volta, sai um aviso novo.
- **O que a tela já mostra não é novidade.** Mudar o preço, ligar o sino, pôr
  na loja ou mexer na lista de personagens conta o estado daquele momento como
  sabido. Quem decide está olhando para o card. É isso que deixa a estratégia
  de segurar funcionar: anunciar acima do mercado de propósito não rende um
  aviso a cada edição.

Pôr um item na loja respeita o teto conjunto de 50 itens vigiados, e a
watchlist passa a respeitá-lo também ao adicionar. Um item novo na watchlist
entra desligado quando o teto está ocupado, em vez de estourá-lo.

Uma consulta de fundo que falha não vira toast, porque ninguém a pediu. A vez
do item avança mesmo assim, senão ele seria o escolhido a todo tick. A idade
do dado mostrado vem de `mercadoEm`, e não do `lastCheckedAt`, para uma falha
não fazer um dado velho parecer novo, inclusive como evidência do aviso de
loja fora do ar.

### Busca

Escolha o servidor (`NIDHOGG` ou `FREYA`, `NIDHOGG`
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
  GnJoy Americas devolve (`GetPriceHistory` com `Limit: 7`) — a média e o
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
"+ Watchlist" de uma seção de refino conhecido já nasce exigindo aquele refino
(como mínimo — ver "Watchlist").

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
  qualquer loja que estiver mais barata, passa a exigir um refino **mínimo**
  — o "menor preço atual" da linha vira o menor preço entre as lojas com
  refino igual ou maior (ex.: exigir "+10" numa arma que só é barata em +0
  passa a mostrar o preço da unidade +10, mesmo que ela não seja a mais
  barata no geral). Deixar o campo vazio e confirmar volta ao padrão
  (qualquer refino, o que estiver mais barato).

  É um piso e não um valor exato porque, no jogo, refino maior só melhora a
  unidade: quem acompanha "+7" quer ser avisado de um "+9" que apareça
  barato tanto quanto de um "+7". O badge mostra a exigência com uma seta
  (`+7↑`) justamente para não ser confundido com o refino ao vivo que ele
  mostra quando não há exigência nenhuma.
- O menor preço anunciado agora (respeitando o refino exigido, se houver) e,
  quando há exigência, o refino que a unidade encontrada tem de fato —
  `Atual: 158.000.000 z (+9)`. Sem isso, "+7↑" no badge não deixaria saber o
  que exatamente ficou barato.
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
  procura o primeiro que satisfaça o pedido. `refine` vale como **mínimo**
  (um anúncio +9 serve para quem pediu +7); o `refine` da resposta é o do
  anúncio encontrado, não o pedido. `bonus` é **repetido, uma vez por frase
  exigida** (`&bonus=CRIT+%2B4&bonus=ATQ+%2B3%25`) — as frases têm pontuação
  livre, e qualquer separador escolhido a dedo poderia aparecer dentro de
  uma delas. Todas as frases pedidas precisam estar no anúncio; bônus a mais
  nele não desqualificam, então preencher só um campo é um filtro mais
  frouxo.

  Os dois filtros são frouxos na mesma direção, de propósito: eles dizem "no
  mínimo isto", e uma unidade melhor que o pedido continua interessando.
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
`localStorage` + DOM, sem chamada ao servidor. Editar o refino exigido dispara
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

O rodízio é do **programa inteiro**, não da watchlist:
`internal/web/static/monitor.js` é o único relógio, e cada tela que vigia
preço se registra nele como uma *fonte*. A cada minuto (`MONITOR_TICK_MS`)
ele escolhe UMA entrada — a que está há mais tempo sem consulta, entre todas
as fontes — e consulta só ela. Nunca a lista inteira, e nunca uma por minuto
para cada tela: o teto que importa é o do site, e ele é do processo.

O escolhido é sempre o de `lastCheckedAt` mais antigo (nunca consultado conta
como o mais antigo de todos, empate desfeito pela ordem de exibição); isso faz
os itens revezarem sozinhos, sem um cursor/fila separado para acompanhar
adições e remoções — remover um item simplesmente tira-o da disputa, e um item
recém-adicionado entra na frente por nunca ter sido consultado. O intervalo
entre duas consultas do MESMO item é, portanto, ~N × 1 min, e é isso que o
teto conjunto `MONITOR_MAX_ITENS` (50 itens **vigiados**, somando as telas)
protege. Guardar itens desligados é livre: eles não disputam a vez.

**Uma regra que o motor não pode quebrar: uma fonte nunca desiste de uma
consulta porque a linha não está na tela.** Qual aba está aberta é escolha de
quem olha; o que é vigiado é escolha de quem configurou. Por isso
`fetchLivePrice` procura a linha só DEPOIS da resposta — antes, ela poderia
não existir (outra aba aberta) ou ainda não ter nascido (o tick sai antes de o
painel terminar de desenhar), e nos dois casos o resultado ficava guardado sem
nunca ser pintado. Pelo mesmo motivo `updateHitState` é dividido em
`pintarHit` (só com linha) e `avaliarHit` (sempre): é `avaliarHit` quem
dispara o aviso, e um item vigiado que só avisasse com a aba dele aberta seria
o oposto de vigiar.

Cada linha nasce com o último resultado conhecido — persistido na própria
entrada (`lastResult`, junto de `lastCheckedAt`) — pintado na hora, sem
nenhuma requisição; a única consulta de verdade do carregamento é a da entrada
mais atrasada, disparada imediatamente (sem esperar o primeiro minuto). O
botão "↻" de cada linha força uma consulta só daquele item a qualquer momento,
sem tocar no cronômetro nem no item que o tick escolheria a seguir.

O cronômetro é achado por atributo (`data-cronometro-do-rodizio`), e não por
id: o relógio é do rodízio, não de uma tela, e qualquer página pode mostrá-lo
sem o monitor conhecer os ids dela.

Se a condição que o item acompanha passar a valer, o usuário é avisado de
quatro formas, sempre nesta ordem: um **toast** no canto inferior direito
(sempre aparece, não depende de permissão nenhuma), um **som** curto, uma
mensagem no **Telegram** (se configurado) e uma **notificação nativa** do
sistema — cuja permissão só é pedida na hora em que ela faz falta (o primeiro
aviso), não no carregamento da página.

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

Como as listas vivem só no navegador, o monitoramento também só roda enquanto
uma aba com o programa estiver aberta — mas **qualquer** aba serve: trocar
entre Estoque e Watchlist não interrompe nada, porque o motor é o mesmo e não
depende de qual painel está na tela. Fechar a aba interrompe as checagens até
ela ser reaberta (quando o rodízio retoma de onde parou, graças ao
`lastCheckedAt` persistido, e a primeira consulta sai na hora). Várias abas
abertas rodam cada uma o próprio tick, mas como as listas moram no mesmo
`localStorage` compartilhado entre elas, tendem a escolher a mesma entrada por
vez — e o cache do servidor absorve essa sobreposição, sem coordenação
explícita entre abas.

O HTMX é vendorizado localmente em `internal/web/static/htmx.min.js`
(embutido no binário via `go:embed`) — não depende de CDN em runtime.

### Tema (claro/escuro)

Segue a preferência do sistema (`prefers-color-scheme`) por padrão. O botão
no topo da página força um dos dois independente do sistema, guardando a
escolha em `localStorage` (`internal/web/static/theme.js`); um script
inline em `layout.html.tmpl`, antes do `<link>` da folha de estilo, aplica
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
| `GNJOY_BASE_URL`                      | `https://ro.gnjoyamericas.com`         | Domínio base do site do GnJoy Americas               |
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

O site do GnJoy Americas tem um rate limiter próprio que responde `429 Too Many
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

### Desafio de navegador: o bloqueio que não passa sozinho

Além do `429`, o site pode responder um **desafio antibot** do Cloudflare — uma
página HTML de verificação, com status `403` ou `503`, no lugar do conteúdo. É
uma situação diferente do `429` em tudo que importa, e por isso tem tratamento
próprio (`gnjoy.ErrDesafioDeNavegador`).

**Por que ela precisou de um ramo só dela.** O `do` tratava qualquer status
que não fosse `429` como "o site está atendendo" — o que é razoável para `404`
ou `500`, e exatamente ao contrário para um desafio. Sem o ramo, um desafio:

1. **liberava** uma suspensão legítima, reabrindo a porta que um `429` tinha
   fechado;
2. não registrava calmaria nenhuma, então a requisição seguinte saía um segundo
   depois e levava outro desafio;
3. chegava à tela como "não foi possível consultar agora", que sugere tropeço
   passageiro.

Ou seja, o programa martelava o bloqueio no ritmo do rate limiter — que é
justamente o comportamento que o mantém ligado.

**O reconhecimento olha o conteúdo, não só o status.** `403` e `503` aparecem em
situações comuns, e suspender o programa inteiro por um `403` qualquer seria
pior que o problema. O que identifica o desafio são marcas estruturais da
página (`challenges.cloudflare.com`, `__cf_chl_`, `cf-browser-verification`) —
há teste afirmando que um `403` sem elas **não** suspende.

**A saída é diferente, e a tela diz isso.** Um `429` passa sozinho com o tempo;
um desafio não passa — o site decidiu não atender a clientes que não são
navegador, e nenhuma espera resolve. O motivo da suspensão viaja até o
navegador (`reason` no evento SSE) e o banner muda de texto: dizer "volta assim
que o site liberar" num desafio seria um conselho falso. Um desafio que chega
durante uma suspensão por `429` **promove** o motivo, porque é o mais grave dos
dois que o usuário precisa saber.

A sonda da suspensão continua sendo quem descobre a liberação, se ela vier.

**Nota de campo.** Em setembro de 2026 o site passou a bloquear clientes
automatizados por impressão digital de TLS: nenhum ajuste de cabeçalho, cookie
ou versão de HTTP atravessa, e um navegador automatizado também é barrado —
enquanto um navegador de verdade passa sem ver desafio nenhum. Não há cookie
`cf_clearance` a reaproveitar, porque ninguém chega a resolver um desafio. Este
tratamento não faz o programa voltar a funcionar nesse cenário; ele faz o
programa se comportar e dizer a verdade enquanto isso.

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