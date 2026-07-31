const { test, expect } = require("@playwright/test");
const { resetPage, buscar } = require("./helpers");

// A consulta de preço da watchlist passa pelo rate limiter do servidor e pode
// custar várias chamadas (uma por candidato, quando há refino fixado), então
// as asserções de preço precisam de uma folga maior que o padrão.
const ESPERA_PRECO = { timeout: 15_000 };

/** Adiciona à watchlist o item da busca por "Espada" (a Espada Primordial). */
async function adicionarEspadaPrimordial(page) {
  await buscar(page, "Espada");
  await page.click(".watchlist-button");
  return page.locator(".watchlist-row").first();
}

test.beforeEach(async ({ page, request }) => {
  await resetPage(page, request);
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
  await page.click(".watchlist-button");

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

test("o cronômetro conta e o botão de atualizar agora o reinicia", async ({ page }) => {
  const cronometro = page.locator("#watchlist-countdown");

  // O ciclo de monitoramento é de 5 minutos.
  await expect(cronometro).toHaveText("05:00");

  // Espera o suficiente para o cronômetro sair de 05:00.
  await expect(cronometro).not.toHaveText("05:00", { timeout: 5_000 });

  await page.click("#watchlist-refresh-now");

  await expect(cronometro).toHaveText("05:00");
});
