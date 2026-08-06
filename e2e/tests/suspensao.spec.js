const { test, expect } = require("@playwright/test");
const {
  resetPage,
  buscar,
  contarRequisicoesAoUpstream,
  zerarContagemDoUpstream,
  falharProximasRequisicoes,
} = require("./helpers");

// O 429 do GnJoy não é um tropeço a insistir: é o site dizendo que a cota
// acabou. O programa para de falar com ele, avisa na tela, e volta sozinho —
// ver internal/gnjoy/suspend.go.
//
// retryAfter=0 em todos os testes: com um valor maior o client entra numa
// calmaria com backoff e até a sonda de recuperação espera a vez, e o teste
// passaria a medir a espera em vez do comportamento.

const banner = "#suspension-banner";

test.beforeEach(async ({ page, request }) => {
  await resetPage(page, request);
});

// A suspensão é global ao processo, e a suíte compartilha um servidor: sem
// esperar a liberação aqui, um teste deste arquivo deixaria os seguintes —
// inclusive de outros arquivos — batendo numa porta fechada. O intervalo da
// sonda está encurtado no global-setup justamente para isto ser rápido.
test.afterEach(async ({ page }) => {
  await expect(page.locator(banner)).toBeHidden({ timeout: 15_000 });
});

test("um 429 mostra o aviso e trava a busca", async ({ page, request }) => {
  await expect(page.locator(banner)).toBeHidden();

  await falharProximasRequisicoes(request, { status: 429, times: 1, retryAfter: 0 });
  await buscar(page, "Espada Primordial", { esperarResultados: false });

  await expect(page.locator(banner)).toBeVisible();
  await expect(page.locator(banner)).toContainText("O site limitou as consultas");

  // Os controles que falariam com o site ficam desligados — nada de continuar
  // clicando em algo que não vai funcionar.
  await expect(page.locator('input[name="item"]')).toBeDisabled();
  await expect(page.locator(".search-button")).toBeDisabled();
  await expect(page.locator("#watchlist-refresh-now")).toBeDisabled();
});

test("enquanto suspenso, nenhuma busca chega ao site", async ({ page, request }) => {
  await falharProximasRequisicoes(request, { status: 429, times: 1, retryAfter: 0 });
  await buscar(page, "Espada Primordial", { esperarResultados: false });
  await expect(page.locator(banner)).toBeVisible();

  await zerarContagemDoUpstream(request);
  // O formulário está travado, então a busca vai direto pela URL do HTMX — é
  // o servidor que precisa recusar, não só a tela.
  const resp = await page.request.get("/web/search?server=NIDHOGG&item=Selo%20de%20Loki");
  expect(await resp.text()).toContain("As consultas estão suspensas");

  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

// O cronômetro contando normalmente durante uma suspensão dava a impressão de
// que a checagem automática continuava rodando — quando na verdade cada
// disparo dela era recusado pelo servidor sem custar nada, e o cronômetro só
// se reagendava para outro ciclo igualmente inútil.
test("o cronômetro da watchlist pausa durante a suspensão e retoma depois", async ({ page, request }) => {
  const contador = page.locator("#watchlist-countdown");
  await expect(contador).not.toHaveText("--:--");

  // Mais de uma falha: a sonda de recuperação já roda a 1s de intervalo neste
  // ambiente de teste (ver GNJOY_SUSPENSION_PROBE_INTERVAL no global-setup) —
  // com só uma falha ela se recuperaria rápido demais para o teste observar a
  // pausa continuando "pausada" e não só num instante de transição.
  await falharProximasRequisicoes(request, { status: 429, times: 4, retryAfter: 0 });
  await buscar(page, "Espada Primordial", { esperarResultados: false });
  await expect(page.locator(banner)).toBeVisible();

  await expect(contador).toHaveText("--:--");
  // Não é só um instante de transição: continua parado.
  await page.waitForTimeout(1_500);
  await expect(contador).toHaveText("--:--");

  await expect(page.locator(banner)).toBeHidden({ timeout: 15_000 });
  await expect(contador).not.toHaveText("--:--");
});

test("o aviso some sozinho quando o site volta a responder", async ({ page, request }) => {
  await falharProximasRequisicoes(request, { status: 429, times: 1, retryAfter: 0 });
  await buscar(page, "Espada Primordial", { esperarResultados: false });
  await expect(page.locator(banner)).toBeVisible();

  // Ninguém aperta nada: a sonda periódica descobre que o site voltou e libera.
  await expect(page.locator(banner)).toBeHidden({ timeout: 15_000 });
  await expect(page.locator('input[name="item"]')).toBeEnabled();

  // E a busca volta a funcionar de verdade.
  await buscar(page, "Espada Primordial");
  await expect(page.locator(".item-group-row")).toHaveCount(1);
});
