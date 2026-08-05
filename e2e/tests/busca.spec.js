const { test, expect } = require("@playwright/test");
const {
  resetPage,
  buscar,
  contarRequisicoesAoUpstream,
  zerarContagemDoUpstream,
  falharProximasRequisicoes,
} = require("./helpers");

test.beforeEach(async ({ page, request }) => {
  await resetPage(page, request);
});

// A consulta de preço da watchlist passa pelo rate limiter do client e pode
// custar mais de uma chamada — ver watchlist.spec.js, que usa o mesmo valor.
const ESPERA_PRECO = { timeout: 15_000 };

test("a página abre com busca, watchlist e barra de atividades", async ({ page }) => {
  await expect(page.locator("h1")).toHaveText("RO Market Tracker");
  await expect(page.locator('input[name="item"]')).toBeVisible();
  await expect(page.locator('select[name="server"]')).toHaveValue("NIDHOGG");

  await expect(page.locator("#watchlist-empty")).toBeVisible();
  await expect(page.locator("#watchlist-empty")).toHaveText("Nenhum item na watchlist.");

  // A barra de atividades começa recolhida: só o cabeçalho aparece.
  await expect(page.locator("#activity-bar")).toHaveAttribute("aria-expanded", "false");
  await expect(page.locator("#activity-history")).not.toBeVisible();
});

test("o botão de tema alterna entre claro e escuro e sobrevive a recarregar", async ({ page }) => {
  // Sem escolha manual, a página segue a preferência do sistema — nenhum
  // data-theme explícito no <html> (ver o script inline em index.html.tmpl).
  await expect(page.locator("html")).not.toHaveAttribute("data-theme");

  await page.click("#theme-toggle");
  const primeiraEscolha = await page.locator("html").getAttribute("data-theme");
  expect(["light", "dark"]).toContain(primeiraEscolha);

  // A escolha é lida do localStorage antes da primeira pintura, então
  // sobrevive a um recarregamento sem piscar o tema errado.
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", primeiraEscolha);

  await page.click("#theme-toggle");
  await expect(page.locator("html")).toHaveAttribute(
    "data-theme",
    primeiraEscolha === "dark" ? "light" : "dark"
  );
});

test("buscar um item mostra a tabela de resultados", async ({ page }) => {
  await buscar(page, "Espada");

  // O título mostra o termo digitado — a busca por palavra pode casar itens
  // de nomes diferentes, então não há "o" nome canônico do resultado para
  // usar aqui (cada seção mostra o seu, ver .item-group-name).
  await expect(page.locator(".results-title")).toHaveText("Resultados de Espada");

  // A tabela já vem ordenada por preço crescente por padrão, então a
  // primeira linha é o anúncio mais barato entre os que casaram — não
  // necessariamente o item do título.
  const linhas = page.locator(".item-row");
  await expect(linhas).toHaveCount(5);
  await expect(linhas.first()).toContainText("Espada Citadina");
  await expect(linhas.first()).toContainText("59.000 z");
  await expect(linhas.first()).toContainText("sk");

  // Toda linha se anuncia como expansível.
  await expect(linhas.first().locator(".expand-icon")).toHaveText("▸");
});

// O domínio das fixtures não resolve de propósito, e o ícone se remove sozinho
// quando não carrega — então servir a imagem aqui não é conveniência, é a
// única forma de exercitar o caminho em que ela carrega. O caso contrário tem
// teste próprio logo abaixo.
const PNG_1X1 = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==",
  "base64",
);

test("o cabeçalho de cada seção mostra o ícone do item", async ({ page }) => {
  await page.route("**/assets.example.invalid/**", (route) =>
    route.fulfill({ contentType: "image/png", body: PNG_1X1 }),
  );

  await buscar(page, "Espada Primordial");

  const icone = page.locator(".item-group-row .item-icon");
  await expect(icone).toBeVisible();
  await expect(icone).toHaveAttribute("src", /assets\.example\.invalid/);
});

// Um ícone quebrado empurraria o nome do item de lado em toda seção da tabela
// — e a URL vem do site, então basta a CDN cair para isso acontecer de verdade.
test("o ícone que não carrega some em vez de quebrar o cabeçalho", async ({ page }) => {
  await page.route("**/assets.example.invalid/**", (route) => route.abort());

  await buscar(page, "Espada Primordial");

  await expect(page.locator(".item-group-name").first()).toHaveText("Espada Primordial");
  await expect(page.locator(".item-icon")).toHaveCount(0);
});

// Os três anúncios da Espada Primordial custam de 129 a 299 milhões porque são
// +0, +7 e +10 — o que a tabela de sempre não tem como mostrar, já que a busca
// do site não traz refino.
test("verificar refino separa os anúncios do mesmo item em seções", async ({ page }) => {
  await buscar(page, "Espada Primordial", { refino: true });

  await expect(page.locator(".item-group-row")).toHaveCount(3);
  // A +0 não ganha etiqueta: o site não distingue "+0" de "não refinável", e
  // escrever "+0" afirmaria o que não se sabe.
  await expect(page.locator(".item-group-row .refine-badge")).toHaveText(["+7", "+10"]);
});

// A +7 tem "CRIT +4" e "ATQ +3%"; a +10 tem só "CRIT +4". Compartilhar um bônus
// não pode juntá-las — é a diferença entre "uma unidade com exatamente estes
// bônus" e "qualquer unidade com este bônus".
test("verificar bônus separa as unidades com combinações diferentes", async ({ page }) => {
  await buscar(page, "Espada Primordial", { bonus: true });

  await expect(page.locator(".item-group-row")).toHaveCount(3);
  await expect(page.locator(".item-group-row").nth(0).locator(".group-chip")).toHaveText("sem bônus");
  await expect(page.locator(".item-group-row").nth(1).locator(".bonus-chip")).toHaveText(["CRIT +4", "ATQ +3%"]);
  await expect(page.locator(".item-group-row").nth(2).locator(".bonus-chip")).toHaveText(["CRIT +4"]);
});

// O que a varredura descobre nunca muda para um mesmo anúncio, então reordenar
// não pode custar nada — sem isso, cada clique num cabeçalho de coluna
// repetiria a varredura inteira e a feature seria inutilizável.
test("reordenar depois de verificar não volta ao site", async ({ page, request }) => {
  await buscar(page, "Espada Primordial", { refino: true, bonus: true });

  await zerarContagemDoUpstream(request);
  await page.click(".sort-link >> nth=0");
  await page.waitForSelector(".results-table");
  await expect(page.locator(".item-group-row")).toHaveCount(3);

  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

// Com as seções separadas por refino, cada uma acompanha uma unidade
// diferente: são duas linhas legítimas do mesmo item, e o refino já vem
// fixado sem o usuário precisar digitá-lo.
test("a watchlist nasce com o refino da seção já fixado", async ({ page }) => {
  await buscar(page, "Espada Primordial", { refino: true });

  const secoes = page.locator(".item-group-row");
  await secoes.filter({ has: page.locator(".refine-badge", { hasText: "+7" }) }).locator(".watchlist-button").click();

  const linhas = page.locator(".watchlist-row");
  await expect(linhas).toHaveCount(1);
  await expect(linhas.first().locator(".watchlist-refine")).toHaveText("+7");
  await expect(linhas.first().locator(".watchlist-current")).toHaveText("Atual: 158.000.000 z", ESPERA_PRECO);

  // A seção +10 é outra unidade: vira uma segunda linha, não um clique sem efeito.
  await secoes.filter({ has: page.locator(".refine-badge", { hasText: "+10" }) }).locator(".watchlist-button").click();
  await expect(linhas).toHaveCount(2);
  await expect(linhas.nth(1).locator(".watchlist-refine")).toHaveText("+10");
});

// As duas versões chegam do site com o mesmo itemName e só se distinguem pelo
// campo de slots — sem juntá-los, a tabela mostrava "Selo de Loki" duas vezes,
// sem nada que explicasse a diferença de preço entre as seções.
test("as versões com e sem slot aparecem com nomes diferentes", async ({ page }) => {
  await buscar(page, "Selo de Loki");

  const nomes = page.locator(".item-group-name");
  await expect(nomes).toHaveCount(2);
  await expect(nomes.nth(0)).toHaveText("Selo de Loki");
  await expect(nomes.nth(1)).toHaveText("Selo de Loki [1]");

  // Na watchlist, a versão com slot continua se identificando como tal.
  await page.locator(".item-group-row", { hasText: "Selo de Loki [1]" }).locator(".watchlist-button").click();
  const linha = page.locator(".watchlist-row");
  await expect(linha.locator(".watchlist-name-text")).toHaveText("Selo de Loki [1]");
  // E é acompanhada de verdade: o preço consultado é o da versão com slot.
  await expect(linha.locator(".watchlist-current")).toHaveText("Atual: 250.000.000 z");
});

// A busca acha a "+7" e para por aí: as outras versões da caixa existem no
// servidor, mas ninguém as anuncia agora — e é justamente uma delas que quem
// procurou pode querer acompanhar.
test("as outras versões do item ficam a um clique de distância", async ({ page }) => {
  await buscar(page, "Caixa de Armadura");

  await expect(page.locator(".item-row")).toHaveCount(1);
  await page.click(".variants-button");

  const linhas = page.locator("#variants .history-row");
  await expect(linhas).toHaveCount(2);
  await expect(page.locator("#variants")).toContainText("Caixa de Armadura +13");
  // A versão que já está na tabela acima não se repete aqui.
  await expect(page.locator("#variants")).not.toContainText("Caixa de Armadura +7");

  // E dá para colocá-la na watchlist esperando ela aparecer no mercado.
  await page.locator("#variants .history-row", { hasText: "+13" }).locator(".watchlist-button").click();
  await expect(page.locator(".watchlist-row")).toContainText("Caixa de Armadura +13");
  await expect(page.locator(".watchlist-row")).toContainText("Nenhum anúncio");
});

test("uma busca sem resultados avisa em vez de mostrar uma tabela vazia", async ({ page }) => {
  await buscar(page, "item que ninguém anuncia", { esperarResultados: false });

  await expect(page.locator("#results")).toContainText("nunca foi vendido no servidor NIDHOGG");
  await expect(page.locator(".results-table")).toHaveCount(0);
});

// Um item fora do mercado atual é justamente o que alguém quer rastrear: em
// vez de parar em "nenhum resultado", a busca mostra por quanto cada item que
// casou com o termo vinha sendo vendido e deixa colocá-los na watchlist.
test("um item fora do mercado mostra os preços praticados nos últimos dias", async ({ page }) => {
  await buscar(page, "Rapidez");

  await expect(page.locator(".results-title")).toHaveText("Histórico de Rapidez");
  await expect(page.locator("#results")).toContainText("Vendas dos últimos 7 dias no servidor NIDHOGG");

  // Uma linha por item que casou com o termo (ver as fixtures do mock).
  const linhas = page.locator(".history-row");
  await expect(linhas).toHaveCount(2);
  await expect(linhas.first()).toContainText("Automódulo de M-Rapidez");
  await expect(linhas.first()).toContainText("5.000.000 z"); // mínimo
  await expect(linhas.first()).toContainText("8.666.666 z"); // médio
  await expect(linhas.first()).toContainText("12.000.000 z"); // máximo
  await expect(linhas.nth(1)).toContainText("Módulo de S-Rapidez");

  // Nenhum anúncio para expandir — não há loja vendendo o item agora.
  await expect(page.locator(".item-row")).toHaveCount(0);

  // Cada linha tem seu botão: só quem procurou sabe qual item quer rastrear.
  await linhas.nth(1).locator(".watchlist-button").click();
  await expect(page.locator(".watchlist-row")).toHaveCount(1);
  await expect(page.locator(".watchlist-row")).toContainText("Módulo de S-Rapidez");
});

// Um item que não vendeu na última semana não pode sumir da tabela só por
// isso: ele existe, tem histórico, e quem procurou precisa saber disso.
test("um item sem venda recente ainda aparece, com o período da linha", async ({ page }) => {
  await buscar(page, "Reformador Primordial");

  const linhas = page.locator(".history-row");
  await expect(linhas).toHaveCount(2);

  // O III vendeu na última semana; o II, não.
  await expect(linhas.first()).toContainText("Reformador Primordial III");
  await expect(linhas.first().locator(".history-period")).toHaveText("7 dias");
  await expect(linhas.nth(1)).toContainText("Reformador Primordial II");
  await expect(linhas.nth(1).locator(".history-period")).toHaveText("todo o histórico");

  // E dá para rastrear o item que não vendeu na semana, que é justamente o
  // que alguém procurando por ele quer.
  await linhas.nth(1).locator(".watchlist-button").click();
  await expect(page.locator(".watchlist-row")).toContainText("Reformador Primordial II");
});

test("um item sem vendas na última semana mostra o histórico completo", async ({ page }) => {
  await buscar(page, "Bota do Andarilho");

  await expect(page.locator("#results")).toContainText("nenhuma venda nos últimos 7 dias");
  await expect(page.locator("#results")).toContainText("todo o histórico de vendas no servidor NIDHOGG");
  await expect(page.locator(".history-row")).toHaveCount(1);
  await expect(page.locator(".history-row")).toContainText("1.250.000 z");
});

test("um item que nunca foi vendido no servidor avisa em vez de mostrar preços", async ({ page }) => {
  await buscar(page, "Elmo Ancestral", { esperarResultados: false });

  await expect(page.locator("#results")).toContainText("nunca foi vendido no servidor NIDHOGG");
  await expect(page.locator(".results-table")).toHaveCount(0);
});

test("uma falha do mercado vira uma mensagem, não uma página quebrada", async ({ page, request }) => {
  // Uma busca que leva 500 não é repetida pelo client (só 429 é), então uma
  // única falha basta para derrubá-la.
  await falharProximasRequisicoes(request, { status: 500, times: 1 });

  await buscar(page, "Espada", { esperarResultados: false });

  await expect(page.locator("#results .error")).toContainText("Não foi possível buscar no mercado agora");
});

test("expandir uma linha mostra refino, localização e estatísticas", async ({ page }) => {
  await buscar(page, "Espada");

  // Com a ordenação padrão por preço crescente, o anúncio +7 (158.000.000 z)
  // é o quarto da lista (ver as fixtures do mock).
  const linha = page.locator(".item-row").nth(3);
  await linha.click();

  const card = page.locator(".detail-card").first();
  await expect(card).toBeVisible();

  await expect(card.locator(".refine-badge")).toHaveText("+7");
  await expect(card).toContainText("158.000.000 z");
  await expect(card).toContainText("Wololol");
  await expect(card).toContainText("mercatung");

  // O comando /navi mantém a extensão .gat do nome do mapa.
  await expect(card.locator(".navi-copy")).toHaveText("/navi prt_mk.gat 120/150");

  // Estatísticas calculadas a partir dos agregados diários do histórico.
  await expect(card).toContainText("Últimos 3 dias");
  await expect(card).toContainText("1.750 z"); // média ponderada
  await expect(card).toContainText("829 z"); // desvio padrão

  await expect(linha.locator(".expand-icon")).toHaveText("▾");
});

// Os bônus aleatórios explicam boa parte da diferença de preço entre dois
// anúncios do mesmo equipamento, e mostrá-los aqui não custa requisição
// nenhuma: o card já consulta o detalhe do item para se montar.
test("o card de detalhe mostra os bônus da unidade anunciada", async ({ page }) => {
  await buscar(page, "Espada Primordial");

  // Ordenada por preço crescente, a unidade +7 (158 milhões) é a segunda.
  await page.locator(".item-row").nth(1).click();

  const card = page.locator(".detail-card").first();
  await expect(card).toContainText("Bônus aleatórios");
  await expect(card.locator(".bonus-list li")).toHaveText(["CRIT +4", "ATQ +3%"]);
});

test("um item sem refino não ganha badge de refino", async ({ page }) => {
  await buscar(page, "Espada");

  // Com a ordenação padrão por preço crescente, a carta (4.999.999 z) é a
  // segunda mais barata da lista — não é equipamento.
  const linha = page.locator(".item-row").nth(1);
  await linha.click();

  const card = page.locator(".detail-card").first();
  await expect(card).toContainText("Carta Peixe-Espada");
  await expect(card.locator(".refine-badge")).toHaveCount(0);
});

// Esse é o comportamento que protege o rate limiter do site: recolher e
// reexpandir uma linha não pode custar uma nova consulta ao mercado.
test("recolher e reexpandir não refaz a consulta ao mercado", async ({ page, request }) => {
  await buscar(page, "Espada");

  const linha = page.locator(".item-row").first();
  await linha.click();
  await expect(page.locator(".detail-card").first()).toBeVisible();

  await zerarContagemDoUpstream(request);

  await linha.click(); // recolhe
  await expect(page.locator(".detail-card").first()).not.toBeVisible();
  await expect(linha.locator(".expand-icon")).toHaveText("▸");

  await linha.click(); // reexpande
  await expect(page.locator(".detail-card").first()).toBeVisible();
  await expect(linha.locator(".expand-icon")).toHaveText("▾");

  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

test("o botão de localização copia o comando /navi", async ({ page }) => {
  await buscar(page, "Espada");
  await page.locator(".item-row").first().click();

  const botao = page.locator(".navi-copy").first();
  const comando = await botao.textContent();
  await botao.click();

  await expect(botao).toHaveText("Copiado!");
  const areaDeTransferencia = await page.evaluate(() => navigator.clipboard.readText());
  expect(areaDeTransferencia).toBe(comando);

  // O rótulo volta ao comando depois do aviso.
  await expect(botao).toHaveText(comando, { timeout: 3000 });
});
