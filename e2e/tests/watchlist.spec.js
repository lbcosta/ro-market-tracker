const { test, expect } = require("@playwright/test");
const {
  resetPage,
  buscar,
  clicarWatchlistDoItem,
  anunciarNoMercado,
  contarRequisicoesAoUpstream,
  zerarContagemDoUpstream,
} = require("./helpers");

// A consulta de preço da watchlist passa pelo rate limiter do servidor e pode
// custar várias chamadas (uma por candidato, quando há refino fixado), então
// as asserções de preço precisam de uma folga maior que o padrão.
const ESPERA_PRECO = { timeout: 15_000 };

/** Adiciona à watchlist o item da busca por "Espada" (a Espada Primordial). */
async function adicionarEspadaPrimordial(page) {
  await buscar(page, "Espada");
  await clicarWatchlistDoItem(page, "Espada Primordial");
  return page.locator(".watchlist-row").first();
}

test.beforeEach(async ({ page, request }) => {
  await resetPage(page, request);
});

/** Nomes das linhas da watchlist, de cima para baixo. */
function nomesDaWatchlist(page) {
  return page.locator(".watchlist-row .watchlist-name-text");
}

/** Adiciona os três itens que a busca por "Espada" casa, na ordem da tabela. */
async function adicionarTresItens(page) {
  await buscar(page, "Espada");
  for (const nome of ["Espada Primordial", "Espada Citadina", "Carta Peixe-Espada"]) {
    await clicarWatchlistDoItem(page, nome);
  }
  await expect(nomesDaWatchlist(page)).toHaveText([
    "Espada Primordial",
    "Espada Citadina",
    "Carta Peixe-Espada",
  ]);
}

// O teclado, além de ser a única forma de reordenar para quem não usa mouse, é
// o caminho determinístico: nada de sintetizar movimento de ponteiro.
test("as setas reordenam a watchlist e a ordem sobrevive a recarregar", async ({ page }) => {
  await adicionarTresItens(page);

  const handleDoTerceiro = page.locator(".watchlist-row").nth(2).locator(".watchlist-drag");
  await handleDoTerceiro.focus();
  await page.keyboard.press("ArrowUp");
  await page.keyboard.press("ArrowUp");

  const esperada = ["Carta Peixe-Espada", "Espada Primordial", "Espada Citadina"];
  await expect(nomesDaWatchlist(page)).toHaveText(esperada);

  await page.reload();
  await expect(nomesDaWatchlist(page)).toHaveText(esperada);
});

test("arrastar uma linha reordena a watchlist", async ({ page }) => {
  await adicionarTresItens(page);

  const handleDoPrimeiro = page.locator(".watchlist-row").nth(0).locator(".watchlist-drag");
  const ultimaLinha = page.locator(".watchlist-row").nth(2);

  const origem = await handleDoPrimeiro.boundingBox();
  const destino = await ultimaLinha.boundingBox();
  await page.mouse.move(origem.x + origem.width / 2, origem.y + origem.height / 2);
  await page.mouse.down();
  // steps: o movimento precisa passar pelos centros dos vizinhos para a linha
  // ser reinserida — um salto único não gera os pointermove do caminho.
  await page.mouse.move(destino.x + destino.width / 2, destino.y + destino.height, { steps: 12 });
  await page.mouse.up();

  const esperada = ["Espada Citadina", "Carta Peixe-Espada", "Espada Primordial"];
  await expect(nomesDaWatchlist(page)).toHaveText(esperada);

  await page.reload();
  await expect(nomesDaWatchlist(page)).toHaveText(esperada);
});

// Reordenar mexe no nó que já está na tela. Se em vez disso o painel fosse
// reconstruído, cada arrastar custaria uma consulta ao site POR ITEM da
// watchlist — é este teste que trava essa armadilha.
test("reordenar não custa nenhuma consulta ao site", async ({ page, request }) => {
  await adicionarTresItens(page);
  // As TRÊS consultas de preço têm de ter voltado antes de zerar a contagem.
  // Esperar só a primeira linha deixa as outras duas em voo — elas saem
  // espaçadas pelo rate limiter — e a que chegasse ao site depois do
  // zeramento entraria na conta como se fosse custo da reordenação.
  await expect(page.locator(".watchlist-row .watchlist-current"))
    .toContainText(["z", "z", "z"], ESPERA_PRECO);

  await zerarContagemDoUpstream(request);

  const handle = page.locator(".watchlist-row").nth(2).locator(".watchlist-drag");
  await handle.focus();
  await page.keyboard.press("ArrowUp");
  await expect(nomesDaWatchlist(page).first()).toHaveText("Espada Primordial");

  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

// Sem nenhuma busca ainda, não há por que reservar a coluna de resultados —
// ela está vazia. A watchlist aparece expandida por padrão, sem que ninguém
// precise clicar em nada.
test("sem busca ainda, a watchlist aparece expandida por padrão", async ({ page }) => {
  await expect(page.locator("html")).toHaveAttribute("data-watchlist-expandida", "1");
  await expect(page.locator(".results-column")).toBeHidden();
  await expect(page.locator("#watchlist-expand")).toHaveAttribute("aria-expanded", "true");
});

// "Não trava nele": o padrão nunca é gravado. Buscar recolhe a watchlist para
// mostrar o resultado, e um NOVO carregamento (sem localStorage) volta ao
// padrão expandido de novo — não veio a virar uma preferência permanente.
test("buscar recolhe a watchlist expandida por padrão, e o padrão não gruda", async ({ page }) => {
  await expect(page.locator("html")).toHaveAttribute("data-watchlist-expandida", "1");

  await buscar(page, "Espada Primordial");
  await expect(page.locator("html")).not.toHaveAttribute("data-watchlist-expandida", "1");
  await expect(page.locator(".results-table")).toBeVisible();

  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-watchlist-expandida", "1");
});

// Uma preferência explícita (o usuário já clicou no botão) vale sobre o
// padrão de "sem resultados ainda" — diferente do padrão, ela sobrevive a
// qualquer recarregamento, resultado ou não.
test("recolher manualmente persiste mesmo sem nenhuma busca", async ({ page }) => {
  await expect(page.locator("html")).toHaveAttribute("data-watchlist-expandida", "1");

  await page.click("#watchlist-expand");
  await expect(page.locator("html")).not.toHaveAttribute("data-watchlist-expandida", "1");

  await page.reload();
  await expect(page.locator("html")).not.toHaveAttribute("data-watchlist-expandida", "1");
});

// Quem acompanha muitos itens tem a coluna de 300px como gargalo. Expandir dá
// a área de conteúdo inteira à watchlist, e os resultados saem do caminho.
test("expandir a watchlist esconde os resultados e sobrevive a recarregar", async ({ page }) => {
  await adicionarEspadaPrimordial(page);
  await expect(page.locator(".results-table")).toBeVisible();

  await page.click("#watchlist-expand");
  await expect(page.locator("html")).toHaveAttribute("data-watchlist-expandida", "1");
  await expect(page.locator(".results-column")).toBeHidden();
  await expect(page.locator("#watchlist-expand")).toHaveAttribute("aria-expanded", "true");
  await expect(page.locator(".watchlist-row")).toBeVisible();

  // O estado é aplicado antes da primeira pintura (script no <head>), então
  // recarregar não mostra o layout de duas colunas nem por um instante.
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-watchlist-expandida", "1");
  await expect(page.locator(".results-column")).toBeHidden();

  await page.click("#watchlist-expand");
  await expect(page.locator("html")).not.toHaveAttribute("data-watchlist-expandida", "1");
  await expect(page.locator("#watchlist-expand")).toHaveAttribute("aria-expanded", "false");
});

// grid-template-areas não pode ser interpolado por nenhum navegador — a troca
// de layout é sempre um corte seco. A animação é um fade nos painéis,
// disparado por uma classe transitória em .page; sem isso alternar rápido dá
// a impressão de "pulo" na tela.
test("expandir/recolher dispara a animação de troca e limpa a classe sozinha", async ({ page }) => {
  await adicionarEspadaPrimordial(page); // buscar() já recolhe: termina com a watchlist comprimida

  const pagina = page.locator(".page");
  const html = page.locator("html");
  await expect(pagina).not.toHaveClass(/watchlist-layout-mudando/);
  await expect(html).not.toHaveAttribute("data-watchlist-expandida", "1");

  // Expandir manualmente dispara a animação...
  await page.click("#watchlist-expand");
  await expect(pagina).toHaveClass(/watchlist-layout-mudando/);
  // ...e ela some sozinha quando termina (ver o "animationend" em
  // watchlist.js), sem precisar de mais nenhuma interação.
  await expect(pagina).not.toHaveClass(/watchlist-layout-mudando/, { timeout: 2_000 });
  await expect(html).toHaveAttribute("data-watchlist-expandida", "1");

  // Com a watchlist expandida, uma nova busca a recolhe automaticamente — e
  // essa troca também anima. A checagem vem ANTES de esperar a resposta
  // chegar: a viagem até o mock (mesmo local) já é mais longa que os 180ms da
  // animação, e por essa altura a classe já teria sumido sozinha.
  await page.fill('input[name="item"]', "Espada Primordial");
  await page.click(".search-button");
  await expect(pagina).toHaveClass(/watchlist-layout-mudando/);

  await page.waitForSelector(".results-table");
  await expect(pagina).not.toHaveClass(/watchlist-layout-mudando/, { timeout: 2_000 });
  await expect(html).not.toHaveAttribute("data-watchlist-expandida", "1");
});

// Quem pediu menos movimento ao sistema operacional não pediu para ver menos
// informação — só para não ver ela se mexendo.
test("a animação de troca respeita prefers-reduced-motion", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await adicionarEspadaPrimordial(page);

  await page.click("#watchlist-expand");
  const nomeDaAnimacao = await page
    .locator(".watchlist-panel")
    .evaluate((el) => getComputedStyle(el).animationName);
  expect(nomeDaAnimacao).toBe("none");
});

// A watchlist expandida dispõe as linhas em grade, e é por isso que o alvo do
// arraste é escolhido pelo centro mais próximo nos dois eixos — só pelo eixo
// Y, duas linhas lado a lado seriam indistinguíveis.
test("arrastar continua funcionando com a watchlist expandida", async ({ page }) => {
  await adicionarTresItens(page);
  await page.click("#watchlist-expand");
  await expect(page.locator(".results-column")).toBeHidden();

  const handle = page.locator(".watchlist-row").nth(2).locator(".watchlist-drag");
  await handle.focus();
  await page.keyboard.press("ArrowUp");

  await expect(nomesDaWatchlist(page)).toHaveText([
    "Espada Primordial",
    "Carta Peixe-Espada",
    "Espada Citadina",
  ]);
});

// semearWatchlistCheia grava N entradas sintéticas direto no localStorage e
// monta as linhas na tela à mão — não há 50 itens de catálogo distintos nas
// fixtures do mock para chegar nesse estado clicando de verdade em cada um,
// e recarregar a página faria renderWatchlist() sair buscando o preço das N
// entradas sintéticas (uma por uma, no ritmo do rate limiter do ambiente de
// teste), levando dezenas de segundos só para montar o cenário. O teste
// quer exercitar o TETO, não o preço de itens que não existem.
async function semearWatchlistCheia(page, quantidade) {
  await page.evaluate((n) => {
    const entradas = Array.from({ length: n }, (_, i) => ({
      id: "NIDHOGG:" + (900000 + i),
      server: "NIDHOGG",
      itemId: 900000 + i,
      itemName: "Item ficticio " + i,
      searchName: "Item ficticio " + i,
      mode: "price",
      targetPrice: null,
      refineFilter: null,
      monitoring: false,
      notified: false,
    }));
    localStorage.setItem("ro-market-tracker:watchlist", JSON.stringify(entradas));

    const container = document.getElementById("watchlist-list");
    container.innerHTML = "";
    for (const entrada of entradas) container.appendChild(buildWatchlistRow(entrada));
    updateEmptyState();
  }, quantidade);
}

// O teto existe para a watchlist nunca virar, sozinha, o consumo dominante da
// cota de consultas ao site — ver WATCHLIST_MAX_ITEMS em watchlist.js.
test("a watchlist recusa passar de 50 itens, com aviso", async ({ page }) => {
  await semearWatchlistCheia(page, 50);
  await expect(page.locator(".watchlist-row")).toHaveCount(50);

  await buscar(page, "Espada Primordial");
  await clicarWatchlistDoItem(page, "Espada Primordial");

  await expect(page.locator(".toast")).toContainText("máximo de 50 itens");
  await expect(page.locator(".watchlist-row")).toHaveCount(50);
});

// Um item a menos que o teto: ainda cabe, e o quinquagésimo entra sem aviso
// nenhum — a mensagem é só para quem esbarra no limite de verdade.
test("o quinquagésimo item entra normalmente, sem aviso", async ({ page }) => {
  await semearWatchlistCheia(page, 49);
  await expect(page.locator(".watchlist-row")).toHaveCount(49);

  await buscar(page, "Espada Primordial");
  await clicarWatchlistDoItem(page, "Espada Primordial");

  await expect(page.locator(".watchlist-row")).toHaveCount(50);
  await expect(page.locator(".toast")).toHaveCount(0);
});

test("adicionar um item mostra o menor preço anunciado", async ({ page }) => {
  const linha = await adicionarEspadaPrimordial(page);

  await expect(page.locator(".watchlist-row")).toHaveCount(1);
  await expect(page.locator("#watchlist-empty")).not.toBeVisible();
  await expect(linha.locator(".watchlist-name")).toContainText("Espada Primordial");
  await expect(linha.locator(".watchlist-target")).toHaveText("Alvo: —");

  // Dos três anúncios da Espada Primordial, o mais barato.
  await expect(linha.locator(".watchlist-current")).toHaveText("Atual: 129.999.999 z", ESPERA_PRECO);
  // ...que por acaso é o sem refino.
  await expect(linha.locator(".watchlist-refine")).toHaveText("+0", ESPERA_PRECO);
});

test("adicionar o mesmo item duas vezes não duplica a linha", async ({ page }) => {
  await adicionarEspadaPrimordial(page);
  await clicarWatchlistDoItem(page, "Espada Primordial");

  await expect(page.locator(".watchlist-row")).toHaveCount(1);
});

test("a watchlist sobrevive a recarregar a página", async ({ page }) => {
  await adicionarEspadaPrimordial(page);
  await expect(page.locator(".watchlist-current")).toHaveText("Atual: 129.999.999 z", ESPERA_PRECO);

  await page.reload();

  // A lista vive em localStorage; o preço é reconsultado ao carregar.
  await expect(page.locator(".watchlist-row")).toHaveCount(1);
  await expect(page.locator(".watchlist-name")).toContainText("Espada Primordial");
  await expect(page.locator(".watchlist-current")).toHaveText("Atual: 129.999.999 z", ESPERA_PRECO);
});

test("o preço alvo é editável no lugar", async ({ page }) => {
  const linha = await adicionarEspadaPrimordial(page);
  await expect(linha.locator(".watchlist-current")).toHaveText("Atual: 129.999.999 z", ESPERA_PRECO);

  await linha.locator(".watchlist-target").click();
  await linha.locator(".watchlist-target-input").fill("100000000");
  await linha.locator(".watchlist-target-input").press("Enter");

  await expect(linha.locator(".watchlist-target")).toHaveText("Alvo: 100.000.000 z");

  // Alvo abaixo do preço atual: ainda não foi atingido.
  await expect(linha.locator(".watchlist-hit-badge")).not.toBeVisible();
  await expect(linha).not.toHaveClass(/target-hit/);
});

test("Esc descarta a edição do alvo", async ({ page }) => {
  const linha = await adicionarEspadaPrimordial(page);

  await linha.locator(".watchlist-target").click();
  await linha.locator(".watchlist-target-input").fill("123");
  await linha.locator(".watchlist-target-input").press("Escape");

  await expect(linha.locator(".watchlist-target")).toHaveText("Alvo: —");
});

test("esvaziar o campo remove o alvo", async ({ page }) => {
  const linha = await adicionarEspadaPrimordial(page);

  await linha.locator(".watchlist-target").click();
  await linha.locator(".watchlist-target-input").fill("100000000");
  await linha.locator(".watchlist-target-input").press("Enter");
  await expect(linha.locator(".watchlist-target")).toHaveText("Alvo: 100.000.000 z");

  await linha.locator(".watchlist-target").click();
  await linha.locator(".watchlist-target-input").fill("");
  await linha.locator(".watchlist-target-input").press("Enter");

  await expect(linha.locator(".watchlist-target")).toHaveText("Alvo: —");
});

// O ponto da watchlist: avisar quando o preço chega no alvo.
test("um alvo acima do preço atual dispara o aviso", async ({ page }) => {
  const linha = await adicionarEspadaPrimordial(page);
  await expect(linha.locator(".watchlist-current")).toHaveText("Atual: 129.999.999 z", ESPERA_PRECO);

  await linha.locator(".watchlist-target").click();
  await linha.locator(".watchlist-target-input").fill("200000000");
  await linha.locator(".watchlist-target-input").press("Enter");

  await expect(linha.locator(".watchlist-hit-badge")).toBeVisible();
  await expect(linha.locator(".watchlist-hit-badge")).toHaveText("🎯 Alvo atingido");
  await expect(linha).toHaveClass(/target-hit/);

  // O toast aparece sempre, sem depender de permissão do navegador.
  const toast = page.locator(".toast").first();
  await expect(toast).toBeVisible();
  await expect(toast).toContainText("Espada Primordial");
  await expect(toast).toContainText("129.999.999 z");
});

// Fixar um refino muda o que a linha acompanha: passa a ser o menor preço
// NAQUELE refino, mesmo que não seja o anúncio mais barato do item.
test("fixar um refino passa a acompanhar o preço daquele refino", async ({ page }) => {
  const linha = await adicionarEspadaPrimordial(page);
  await expect(linha.locator(".watchlist-current")).toHaveText("Atual: 129.999.999 z", ESPERA_PRECO);

  await linha.locator(".watchlist-refine").click();
  await linha.locator(".watchlist-refine-input").fill("10");
  await linha.locator(".watchlist-refine-input").press("Enter");

  await expect(linha.locator(".watchlist-refine")).toHaveText("+10");
  // O anúncio +10 é o mais caro dos três — é justamente o caso que o filtro
  // de refino existe para resolver.
  await expect(linha.locator(".watchlist-current")).toHaveText("Atual: 299.999.999 z", ESPERA_PRECO);
});

test("desfixar o refino volta a acompanhar o mais barato", async ({ page }) => {
  const linha = await adicionarEspadaPrimordial(page);
  await expect(linha.locator(".watchlist-current")).toHaveText("Atual: 129.999.999 z", ESPERA_PRECO);

  await linha.locator(".watchlist-refine").click();
  await linha.locator(".watchlist-refine-input").fill("10");
  await linha.locator(".watchlist-refine-input").press("Enter");
  await expect(linha.locator(".watchlist-current")).toHaveText("Atual: 299.999.999 z", ESPERA_PRECO);

  await linha.locator(".watchlist-refine").click();
  await linha.locator(".watchlist-refine-input").fill("");
  await linha.locator(".watchlist-refine-input").press("Enter");

  await expect(linha.locator(".watchlist-current")).toHaveText("Atual: 129.999.999 z", ESPERA_PRECO);
});

test("um refino que ninguém anuncia avisa que não há anúncios", async ({ page }) => {
  const linha = await adicionarEspadaPrimordial(page);
  await expect(linha.locator(".watchlist-current")).toHaveText("Atual: 129.999.999 z", ESPERA_PRECO);

  await linha.locator(".watchlist-refine").click();
  await linha.locator(".watchlist-refine-input").fill("3");
  await linha.locator(".watchlist-refine-input").press("Enter");

  await expect(linha.locator(".watchlist-current")).toHaveText("Sem anúncios", ESPERA_PRECO);
});

test("a luz liga e desliga o monitoramento do item", async ({ page }) => {
  const linha = await adicionarEspadaPrimordial(page);
  const luz = linha.locator(".status-light");

  await expect(luz).toHaveClass(/on/);

  await linha.locator(".status-toggle").click();
  await expect(luz).toHaveClass(/off/);
  await expect(linha.locator(".status-toggle")).toHaveAttribute("aria-label", "Ativar monitoramento");

  await linha.locator(".status-toggle").click();
  await expect(luz).toHaveClass(/on/);
  await expect(linha.locator(".status-toggle")).toHaveAttribute("aria-label", "Desativar monitoramento");
});

test("o estado de monitoramento sobrevive a recarregar", async ({ page }) => {
  const linha = await adicionarEspadaPrimordial(page);
  await linha.locator(".status-toggle").click();
  await expect(linha.locator(".status-light")).toHaveClass(/off/);

  await page.reload();

  await expect(page.locator(".watchlist-row .status-light")).toHaveClass(/off/);
});

test("remover tira o item da lista", async ({ page }) => {
  const linha = await adicionarEspadaPrimordial(page);

  await linha.locator(".watchlist-remove").click();

  await expect(page.locator(".watchlist-row")).toHaveCount(0);
  await expect(page.locator("#watchlist-empty")).toBeVisible();

  // E não volta ao recarregar.
  await page.reload();
  await expect(page.locator(".watchlist-row")).toHaveCount(0);
});

// --- itens fora do mercado: acompanhar disponibilidade, não preço ---

/**
 * Adiciona à watchlist o "Módulo de S-Rapidez" pela tabela de histórico —
 * um item que ninguém está anunciando (ver as fixtures do mock).
 */
async function adicionarModuloForaDoMercado(page) {
  await buscar(page, "Rapidez");
  await page.locator(".history-row", { hasText: "Módulo de S-Rapidez" }).locator(".watchlist-button").click();
  return page.locator(".watchlist-row").first();
}

test("um item fora do mercado entra na watchlist esperando ele aparecer", async ({ page }) => {
  const linha = await adicionarModuloForaDoMercado(page);

  await expect(linha.locator(".watchlist-name")).toContainText("Módulo de S-Rapidez");

  // Não há preço a esperar, então também não há preço alvo a definir.
  await expect(linha.locator(".watchlist-target")).toHaveCount(0);
  await expect(linha.locator(".watchlist-current")).toHaveText("Nenhum anúncio", ESPERA_PRECO);
  await expect(linha.locator(".watchlist-hit-badge")).not.toBeVisible();
});

// O ponto do modo de disponibilidade: avisar no primeiro anúncio que
// aparecer, seja qual for o preço.
test("quando o item aparece no mercado, a watchlist avisa", async ({ page, request }) => {
  const linha = await adicionarModuloForaDoMercado(page);
  await expect(linha.locator(".watchlist-current")).toHaveText("Nenhum anúncio", ESPERA_PRECO);

  await anunciarNoMercado(request, {
    itemName: "Módulo de S-Rapidez",
    itemId: 25690,
    price: 6000000,
  });
  await page.click("#watchlist-refresh-now");

  await expect(linha.locator(".watchlist-current")).toHaveText("Produto encontrado por 6.000.000 z", ESPERA_PRECO);
  await expect(linha.locator(".watchlist-hit-badge")).toBeVisible();
  await expect(linha.locator(".watchlist-hit-badge")).toHaveText("🎯 Disponível");
  await expect(linha).toHaveClass(/target-hit/);

  const toast = page.locator(".toast").first();
  await expect(toast).toBeVisible();
  await expect(toast).toContainText("Módulo de S-Rapidez");
  await expect(toast).toContainText("6.000.000 z");
});

// O modo de acompanhamento é da entrada, não da tela: precisa sobreviver ao
// recarregamento junto com o resto da watchlist.
test("o modo de disponibilidade sobrevive a recarregar a página", async ({ page }) => {
  await adicionarModuloForaDoMercado(page);
  await page.reload();

  const linha = page.locator(".watchlist-row").first();
  await expect(linha.locator(".watchlist-target")).toHaveCount(0);
  await expect(linha.locator(".watchlist-current")).toHaveText("Nenhum anúncio", ESPERA_PRECO);
});

// Um item adicionado pela tabela de resultados continua no modo de preço: as
// duas tabelas alimentam a mesma watchlist, com condições diferentes.
test("um item adicionado pela busca continua acompanhando preço", async ({ page }) => {
  const linha = await adicionarEspadaPrimordial(page);

  await expect(linha.locator(".watchlist-target")).toHaveText("Alvo: —");
  await expect(linha.locator(".watchlist-current")).toHaveText("Atual: 129.999.999 z", ESPERA_PRECO);
});

test("o cronômetro conta e o botão de atualizar agora o reinicia", async ({ page }) => {
  const cronometro = page.locator("#watchlist-countdown");

  // O ciclo de monitoramento é de 5 minutos.
  await expect(cronometro).toHaveText("05:00");

  // Espera o suficiente para o cronômetro sair de 05:00.
  await expect(cronometro).not.toHaveText("05:00", { timeout: 5_000 });

  await page.click("#watchlist-refresh-now");

  await expect(cronometro).toHaveText("05:00");
});
