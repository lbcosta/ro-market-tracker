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

test("buscar um item mostra a tabela de resultados", async ({ page }) => {
  await buscar(page, "Espada");

  // O título usa o nome canônico do primeiro resultado, não o termo digitado.
  await expect(page.locator(".results-title")).toHaveText("Resultados de Espada Primordial");

  const linhas = page.locator(".item-row");
  await expect(linhas).toHaveCount(5);
  await expect(linhas.first()).toContainText("Espada Primordial");
  await expect(linhas.first()).toContainText("129.999.999 z");
  await expect(linhas.first()).toContainText("Vendinha do Zé");

  // Toda linha se anuncia como expansível.
  await expect(linhas.first().locator(".expand-icon")).toHaveText("▸");
});

test("uma busca sem resultados avisa em vez de mostrar uma tabela vazia", async ({ page }) => {
  await buscar(page, "item que ninguém anuncia", { esperarResultados: false });

  await expect(page.locator("#results")).toContainText("Nenhum resultado encontrado.");
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

  // A segunda linha é o anúncio +7 (ver as fixtures do mock).
  const linha = page.locator(".item-row").nth(1);
  await linha.click();

  const card = page.locator(".detail-card").first();
  await expect(card).toBeVisible();

  await expect(card.locator(".refine-badge")).toHaveText("+7");
  await expect(card).toContainText("158.000.000 z");
  await expect(card).toContainText("Wololol");
  await expect(card).toContainText("mercatung");

  // O comando /navi sai sem a extensão .gat do nome do mapa.
  await expect(card.locator(".navi-copy")).toHaveText("/navi prt_mk/120/150");

  // Estatísticas calculadas a partir dos agregados diários do histórico.
  await expect(card).toContainText("Últimos 3 dias");
  await expect(card).toContainText("1.750 z"); // média ponderada
  await expect(card).toContainText("829 z"); // desvio padrão

  await expect(linha.locator(".expand-icon")).toHaveText("▾");
});

test("um item sem refino não ganha badge de refino", async ({ page }) => {
  await buscar(page, "Espada");

  // A última linha é a carta — não é equipamento.
  const linha = page.locator(".item-row").nth(4);
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
