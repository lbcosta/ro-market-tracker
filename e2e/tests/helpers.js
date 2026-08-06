const { MOCK_URL } = require("../config");

// resetPage devolve mock e navegador ao estado inicial e abre a página.
//
// Os dois lados precisam ser zerados: a watchlist vive inteira em
// localStorage (senão um teste herda os itens do anterior) e o mock guarda
// falhas/atrasos enfileirados (uma falha que sobrou de um teste derrubaria o
// seguinte por um motivo sem relação nenhuma com ele).
async function resetPage(page, request) {
  if (request) {
    await request.post(`${MOCK_URL}/__mock/reset`);
    // O cache de consultas do servidor também guarda estado entre testes
    // (ver internal/web/cache.go): sem zerá-lo aqui, um teste enxergaria o
    // mercado que o teste anterior deixou cacheado em vez de consultar o
    // mock que ele mesmo acabou de configurar.
    await request.post("/web/cache/reset");
  }
  await page.goto("/");
  await page.evaluate(() => localStorage.clear());
  await page.reload();
}

// buscar preenche o formulário e espera a resposta desta busca (e a tabela,
// ou a mensagem de vazio, aparecer), para nenhum teste precisar dormir
// esperando o HTMX.
//
// Esperar a RESPOSTA, e não só o seletor, é o que torna a espera real na
// segunda busca de um teste: a tabela da primeira ainda está na tela, então o
// waitForSelector passaria de imediato e o teste seguiria — e terminaria —
// com a busca nova em voo. Uma busca que sobrevive ao teste que a disparou
// atrapalha o seguinte (o servidor não a cancela junto com a conexão, de
// propósito: ver cachedSearchShops).
//
// refino/bonus marcam os checkboxes de varredura antes de buscar. Eles custam
// uma consulta ao site por anúncio encontrado, então a busca demora
// visivelmente mais — o timeout padrão do waitForResponse dá conta das
// fixtures, que têm poucos anúncios.
async function buscar(page, item, { esperarResultados = true, refino = false, bonus = false } = {}) {
  await page.fill('input[name="item"]', item);
  if (refino) await page.check('input[name="refine"]');
  if (bonus) await page.check('input[name="bonus"]');

  const resposta = page.waitForResponse((r) => {
    const url = new URL(r.url());
    return url.pathname === "/web/search" && url.searchParams.get("item") === item;
  });
  await page.click(".search-button");
  await resposta;

  if (esperarResultados) {
    await page.waitForSelector(".results-table");
  } else {
    await page.waitForSelector("#results p");
  }
}

// --- plano de controle do mock (ver internal/gnjoytest/cmd/mockgnjoy) ---

async function contarRequisicoesAoUpstream(request) {
  const resp = await request.get(`${MOCK_URL}/__mock/requests`);
  const { count } = await resp.json();
  return count;
}

// zerarContagemDoUpstream devolve o mock ao estado inicial (o que inclui
// zerar o contador de requisições), para medir só o que vem a seguir.
async function zerarContagemDoUpstream(request) {
  await request.post(`${MOCK_URL}/__mock/reset`);
}

// falharProximasRequisicoes faz o site falso recusar as próximas n
// requisições, para exercitar como o frontend mostra uma falha do upstream.
//
// retryAfter vira o cabeçalho "Retry-After" da resposta. Importa nos testes de
// 429: sem ele, o client entra numa calmaria com backoff e até a sonda de
// recuperação fica esperando a vez, o que faria o teste medir a espera em vez
// do comportamento.
async function falharProximasRequisicoes(request, { status = 500, times = 1, retryAfter } = {}) {
  const q = new URLSearchParams({ status: String(status), times: String(times) });
  if (retryAfter !== undefined) q.set("retryAfter", String(retryAfter));
  await request.post(`${MOCK_URL}/__mock/fail?${q}`);
}

// clicarWatchlistDoItem clica o "+ Watchlist" da seção de itemName na tabela
// de resultados de busca. É preciso escopar pela seção porque a busca por
// palavra pode casar vários itens de nomes diferentes (cada um com sua
// própria seção e seu próprio botão) — ver internal/web/handlers.go
// (resultsGroup).
async function clicarWatchlistDoItem(page, itemName) {
  await page.locator(".item-group-row", { hasText: itemName }).locator(".watchlist-button").click();
}

// anunciarNoMercado faz um item que ninguém estava anunciando aparecer na
// busca do site falso — a transição que a watchlist em modo de
// disponibilidade existe para pegar.
async function anunciarNoMercado(request, { itemName, itemId, price }) {
  const q = new URLSearchParams({ itemName, itemId: String(itemId), price: String(price) });
  await request.post(`${MOCK_URL}/__mock/anunciar?${q}`);
}

// atrasarProximasRequisicoes segura as próximas n respostas do site falso sem
// falhá-las. É o que torna determinístico observar um estado transitório da
// interface (o spinner, o texto no gerúndio) em vez de torcer para acertar
// uma janela de milissegundos.
async function atrasarProximasRequisicoes(request, { ms, times = 1 }) {
  await request.post(`${MOCK_URL}/__mock/delay?ms=${ms}&times=${times}`);
}

module.exports = {
  resetPage,
  buscar,
  clicarWatchlistDoItem,
  contarRequisicoesAoUpstream,
  zerarContagemDoUpstream,
  falharProximasRequisicoes,
  atrasarProximasRequisicoes,
  anunciarNoMercado,
};
