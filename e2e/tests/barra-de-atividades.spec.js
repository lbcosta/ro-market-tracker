const { test, expect } = require("@playwright/test");
const {
  resetPage,
  buscar,
  clicarWatchlistDoItem,
  falharProximasRequisicoes,
  atrasarProximasRequisicoes,
} = require("./helpers");

const ESPERA_ATIVIDADE = { timeout: 15_000 };

test.beforeEach(async ({ page, request }) => {
  await resetPage(page, request);
});

test("a barra recolhida mostra a atividade mais recente", async ({ page }) => {
  const linhaAtual = page.locator("#activity-current-label");

  await buscar(page, "Espada");

  await expect(linhaAtual).toContainText("Espada", ESPERA_ATIVIDADE);
  await expect(linhaAtual).toContainText("[OK]", ESPERA_ATIVIDADE);
  await expect(linhaAtual).toHaveClass(/activity-status-success/);
});

// A barra existe para o usuário entender o que o servidor está fazendo por
// baixo dos panos. O atraso injetado no mock segura a resposta o suficiente
// para o estado "em andamento" ser observável de forma determinística.
test("uma chamada em andamento aparece no gerúndio e com spinner", async ({ page, request }) => {
  await atrasarProximasRequisicoes(request, { ms: 3000, times: 1 });

  const linhaAtual = page.locator("#activity-current-label");
  const spinner = page.locator("#activity-spinner");

  await page.fill('input[name="item"]', "Espada");
  await page.click(".search-button");

  await expect(linhaAtual).toContainText("Buscando", ESPERA_ATIVIDADE);
  await expect(linhaAtual).toHaveClass(/activity-status-(waiting|running)/);
  await expect(spinner).toBeVisible();

  // E ao terminar, o texto passa a estar conjugado no tempo certo.
  await expect(linhaAtual).toContainText("concluída [OK]", ESPERA_ATIVIDADE);
  await expect(linhaAtual).toHaveClass(/activity-status-success/);
  await expect(spinner).not.toBeVisible();
});

test("clicar na barra expande e recolhe o histórico", async ({ page }) => {
  const barra = page.locator("#activity-bar");
  const historico = page.locator("#activity-history");

  await buscar(page, "Espada");
  await expect(page.locator("#activity-current-label")).toContainText("[OK]", ESPERA_ATIVIDADE);

  await barra.click();
  await expect(barra).toHaveAttribute("aria-expanded", "true");
  await expect(historico).toBeVisible();

  await barra.click();
  await expect(barra).toHaveAttribute("aria-expanded", "false");
  await expect(historico).not.toBeVisible();
});

// O histórico é do servidor e acumula desde que ele subiu, então a asserção é
// sobre a ordem (a chamada anterior fica logo acima da atual), não sobre a
// quantidade de linhas.
test("a chamada anterior fica logo acima da atual no histórico", async ({ page }) => {
  await buscar(page, "Espada");
  await expect(page.locator("#activity-current-label")).toContainText("Espada", ESPERA_ATIVIDADE);

  await buscar(page, "Poring");
  await expect(page.locator("#activity-current-label")).toContainText("Poring", ESPERA_ATIVIDADE);

  await page.locator("#activity-bar").click();

  const ultimaDoHistorico = page.locator("#activity-history li").last();
  await expect(ultimaDoHistorico).toContainText("Espada");
  await expect(ultimaDoHistorico).toHaveClass(/activity-status-success/);
});

// A hora ajuda a entender QUANDO cada chamada aconteceu, não só o quê — fica
// sempre à esquerda da linha (tanto na atual quanto no histórico), entre
// colchetes e menos destacada que a mensagem ao lado.
test("cada linha mostra o horário em que a requisição foi feita", async ({ page }) => {
  await buscar(page, "Espada");

  const horaAtual = page.locator("#activity-current-time");
  await expect(horaAtual).toHaveText(/^\[\d{2}:\d{2}:\d{2}\]$/, ESPERA_ATIVIDADE);

  await buscar(page, "Poring");
  await expect(page.locator("#activity-current-label")).toContainText("Poring", ESPERA_ATIVIDADE);
  await page.locator("#activity-bar").click();

  const linhaAnterior = page.locator("#activity-history li").last();
  await expect(linhaAnterior).toContainText("Espada");
  await expect(linhaAnterior.locator(".activity-time")).toHaveText(/^\[\d{2}:\d{2}:\d{2}\]$/);
});

test("uma chamada que falha aparece em vermelho com [Erro]", async ({ page, request }) => {
  // Uma busca que leva 500 não é repetida (só 429 é), então falha em uma
  // única requisição.
  await falharProximasRequisicoes(request, { status: 500, times: 1 });

  await page.fill('input[name="item"]', "Espada");
  await page.click(".search-button");

  const linhaAtual = page.locator("#activity-current-label");
  await expect(linhaAtual).toContainText("Falha ao buscar", ESPERA_ATIVIDADE);
  await expect(linhaAtual).toContainText("[Erro]", ESPERA_ATIVIDADE);
  await expect(linhaAtual).toHaveClass(/activity-status-error/);
});

// Com o rate limiter a 2 req/s e burst 1, uma chamada disparada logo após
// outra precisa esperar meio segundo — e a barra tem de dizer isso ao
// usuário, com o cronômetro, em vez de parecer travada.
test("uma chamada esperando na fila mostra o cronômetro", async ({ page }) => {
  await buscar(page, "Espada");

  // Expandir dispara três chamadas seguidas (loja, item e histórico), então
  // as duas últimas necessariamente enfrentam a fila do rate limiter.
  await page.locator(".item-row").first().click();

  const linhaAtual = page.locator("#activity-current-label");
  await expect(linhaAtual).toHaveText(/\d+s$/, ESPERA_ATIVIDADE);
  await expect(linhaAtual).toHaveClass(/activity-status-waiting/);

  // E o card carrega no fim, apesar da espera.
  await expect(page.locator(".detail-card").first()).toBeVisible(ESPERA_ATIVIDADE);
});

test("a atividade reflete o que a watchlist consulta, não só a busca", async ({ page }) => {
  await buscar(page, "Espada");
  await clicarWatchlistDoItem(page, "Espada Primordial");

  // A watchlist consulta pelo nome canônico do item.
  await expect(page.locator("#activity-current-label")).toContainText(
    "Espada Primordial",
    ESPERA_ATIVIDADE
  );
});
