const { test, expect } = require("@playwright/test");
const {
  resetPage,
  buscar,
  clicarWatchlistDoItem,
  contarRequisicoesAoUpstream,
  zerarContagemDoUpstream,
} = require("./helpers");

// Trocar de aba NÃO recarrega o documento: troca só o miolo (#page-content).
// O que está em jogo é o motor compartilhado do lado do servidor — a barra de
// atividades é a janela para ele e o rodízio da watchlist é um relógio que
// não pode reiniciar a cada clique no menu. Ver static/navegacao.js.

const abaEstoque = (page) => page.getByRole("link", { name: "Estoque" });
const abaWatchlist = (page) => page.getByRole("link", { name: "Watchlist" });

// A aba ativa é marcada duas vezes (ver o comentário no template): .is-active
// para os olhos e aria-current="page" para o leitor de tela. Os testes checam
// o aria-current — se só o visual for marcado, quem usa leitor de tela não
// tem como saber em que página está.
async function esperarAba(page, aberta) {
  const [ativa, inativa] =
    aberta === "estoque" ? [abaEstoque(page), abaWatchlist(page)] : [abaWatchlist(page), abaEstoque(page)];
  await expect(ativa).toHaveAttribute("aria-current", "page");
  await expect(inativa).not.toHaveAttribute("aria-current", "page");
}

test.beforeEach(async ({ page, request }) => {
  await resetPage(page, request);
});

test("o menu troca de aba e marca a aberta", async ({ page }) => {
  await esperarAba(page, "watchlist");

  await abaEstoque(page).click();
  await expect(page).toHaveURL(/\/estoque$/);
  await expect(page.locator("#estoque-form")).toBeVisible();
  await esperarAba(page, "estoque");

  await abaWatchlist(page).click();
  await expect(page).toHaveURL(/:\d+\/$/);
  await expect(page.locator(".watchlist-panel")).toBeVisible();
  await expect(page.locator(".search-form")).toBeVisible();
  await esperarAba(page, "watchlist");
});

test("cabeçalho, subtítulo e rodapé continuam em todas as páginas", async ({ page }) => {
  for (const ir of [() => abaEstoque(page).click(), () => abaWatchlist(page).click()]) {
    await ir();
    await expect(page.getByRole("heading", { name: "RO Market Tracker", level: 1 })).toBeVisible();
    await expect(page.locator(".subtitle")).toHaveText(
      "Preços do mercado de comércio do Ragnarok Online LATAM",
    );
    await expect(page.locator("#theme-toggle")).toBeVisible();
    await expect(page.locator("#activity-bar")).toBeVisible();
    await expect(page.locator("#version-badge")).toBeVisible();
  }
});

test("a página do Estoque não traz nada da Watchlist", async ({ page }) => {
  await abaEstoque(page).click();

  await expect(page.locator("#estoque-form")).toBeVisible();
  await expect(page.locator("#estoque-list")).toBeAttached();
  await expect(page.locator("#watchlist-list")).toHaveCount(0);
  await expect(page.locator(".search-form")).toHaveCount(0);
});

// O ponto de tudo isto. O log é o histórico do motor compartilhado: ele não
// pode ser re-renderizado nem perder a conexão ao trocar de aba.
test("o log do rodapé sobrevive à troca de aba, sem reconectar", async ({ page }) => {
  await buscar(page, "Espada");
  const antes = await page.locator("#activity-current-label").textContent();
  expect(antes).not.toBe("Nenhuma atividade ainda.");

  // Marca o nó da barra: se ele for recriado, a marca some junto.
  await page.evaluate(() => (document.getElementById("activity-bar").dataset.marca = "1"));

  let conexoes = 0;
  page.on("request", (r) => {
    if (r.url().includes("/web/activity/stream")) conexoes++;
  });

  for (let i = 0; i < 5; i++) {
    await abaEstoque(page).click();
    await expect(page.locator("#estoque-form")).toBeVisible();
    await abaWatchlist(page).click();
    await expect(page.locator(".search-form")).toBeVisible();
  }

  expect(conexoes).toBe(0);
  await expect(page.locator("#activity-bar")).toHaveAttribute("data-marca", "1");
  await expect(page.locator("#activity-current-label")).toHaveText(antes);
});

// Cada abertura da Watchlist chegava a gastar uma consulta ao site e a
// reiniciar o cronômetro de um minuto do zero: trocar de aba rápido consultava
// muito mais que uma vez por minuto, que é o caminho curto para o 429.
test("trocar de aba não dispara consulta ao mercado", async ({ page }) => {
  await buscar(page, "Espada");
  await clicarWatchlistDoItem(page, "Espada Primordial");
  await page.waitForTimeout(1500);

  let consultas = 0;
  page.on("request", (r) => {
    if (r.url().includes("/web/watchlist/price") || r.url().includes("/web/search")) consultas++;
  });

  for (let i = 0; i < 5; i++) {
    await abaEstoque(page).click();
    await expect(page.locator("#estoque-form")).toBeVisible();
    await abaWatchlist(page).click();
    await expect(page.locator(".search-form")).toBeVisible();
  }

  expect(consultas).toBe(0);
});

// O documento não recarrega, então o painel que volta é DOM novo: sem religar
// (ver religarCorpo em navegacao.js), a watchlist voltaria vazia.
test("a watchlist volta preenchida ao voltar para a aba", async ({ page }) => {
  await buscar(page, "Espada");
  await clicarWatchlistDoItem(page, "Espada Primordial");
  await expect(page.locator(".watchlist-row")).toHaveCount(1);

  await abaEstoque(page).click();
  await expect(page.locator("#estoque-form")).toBeVisible();
  await abaWatchlist(page).click();

  await expect(page.locator(".watchlist-row")).toHaveCount(1);
  await expect(page.locator(".watchlist-row")).toContainText("Espada Primordial");
});

// Regressão: o rodízio é do programa, não da aba aberta. fetchLivePrice fazia
// findRow() e DESISTIA antes do fetch quando a linha não estava na tela — com
// o Estoque aberto, cada tick virava um nada: sem consulta, sem avançar o
// lastCheckedAt e sem nunca disparar o aviso no Telegram, apesar de o
// watchlist.js estar carregado nas duas abas.
test("o rodízio continua consultando com a aba Estoque aberta", async ({ page, request }) => {
  await buscar(page, "Espada");
  await clicarWatchlistDoItem(page, "Espada Primordial");
  await page.waitForTimeout(1200);

  await abaEstoque(page).click();
  await expect(page.locator("#estoque-form")).toBeVisible();

  const relogio = () =>
    page.evaluate(() => JSON.parse(localStorage.getItem("ro-market-tracker:watchlist"))[0].lastCheckedAt);
  const antes = await relogio();
  await zerarContagemDoUpstream(request);

  // fresh: o que está em teste é se a consulta SAI, não se o servidor a
  // responde do cache dele.
  await page.evaluate(() => rodarTick(true));
  await expect.poll(relogio).toBeGreaterThan(antes);

  expect(await contarRequisicoesAoUpstream(request)).toBeGreaterThan(0);
});

test("recarregar mantém a aba do Estoque aberta", async ({ page }) => {
  await abaEstoque(page).click();
  await expect(page).toHaveURL(/\/estoque$/);

  await page.reload();

  await expect(page.locator("#estoque-form")).toBeVisible();
  await esperarAba(page, "estoque");
});

test("o voltar e o avançar do navegador trocam a aba", async ({ page }) => {
  await abaEstoque(page).click();
  await expect(page).toHaveURL(/\/estoque$/);

  await page.goBack();
  await expect(page).toHaveURL(/:\d+\/$/);
  await expect(page.locator(".search-form")).toBeVisible();
  await esperarAba(page, "watchlist");

  await page.goForward();
  await expect(page).toHaveURL(/\/estoque$/);
  await expect(page.locator("#estoque-form")).toBeVisible();
  await esperarAba(page, "estoque");
});

// Sem JS o href continua sendo um href: a navegação vira a comum do
// navegador, e a página inteira chega do servidor.
test("os links do menu funcionam como links comuns", async ({ page }) => {
  await expect(abaEstoque(page)).toHaveAttribute("href", "/estoque");
  await expect(abaWatchlist(page)).toHaveAttribute("href", "/");
});
