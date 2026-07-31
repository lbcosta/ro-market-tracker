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
  }
  await page.goto("/");
  await page.evaluate(() => localStorage.clear());
  await page.reload();
}

// buscar preenche o formulário e espera a tabela (ou a mensagem de vazio)
// aparecer, para nenhum teste precisar dormir esperando o HTMX.
async function buscar(page, item, { esperarResultados = true } = {}) {
  await page.fill('input[name="item"]', item);
  await page.click(".search-button");
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
async function falharProximasRequisicoes(request, { status = 500, times = 1 } = {}) {
  await request.post(`${MOCK_URL}/__mock/fail?status=${status}&times=${times}`);
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
  contarRequisicoesAoUpstream,
  zerarContagemDoUpstream,
  falharProximasRequisicoes,
  atrasarProximasRequisicoes,
};
