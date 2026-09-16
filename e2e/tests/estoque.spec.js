const { test, expect } = require("@playwright/test");
const {
  resetPage,
  contarRequisicoesAoUpstream,
  zerarContagemDoUpstream,
  falharProximasRequisicoes,
  atrasarProximasRequisicoes,
} = require("./helpers");

// Primeira etapa da tela de Estoque: só o cadastro local. NADA aqui fala com
// o site da GnJoy — validar, buscar preços e avisar sobre undercutting entram
// nas etapas seguintes. Vários testes conferem isso com o contador de
// requisições ao upstream, porque "zero requisição" é requisito, não acaso.

const campo = (page) => page.locator("#estoque-item");
const cards = (page) => page.locator(".estoque-card");
const card = (page, nome) => page.locator(".estoque-card").filter({ hasText: nome });

async function adicionar(page, nome) {
  await campo(page).fill(nome);
  await campo(page).press("Enter");
  await expect(card(page, nome)).toBeVisible();
}

test.beforeEach(async ({ page, request }) => {
  await resetPage(page, request);
  await page.getByRole("link", { name: "Estoque" }).click();
  await expect(page.locator("#estoque-form")).toBeVisible();
});

test("a tela começa vazia e explica o que fazer", async ({ page }) => {
  await expect(page.locator("#estoque-empty")).toBeVisible();
  await expect(page.locator("#estoque-empty")).toContainText("Nenhum item no estoque");
  await expect(cards(page)).toHaveCount(0);
});

test("Enter adiciona o item como não validado, sem consultar o site", async ({ page, request }) => {
  await zerarContagemDoUpstream(request);

  await adicionar(page, "Elmo Ancestral");

  await expect(card(page, "Elmo Ancestral").locator(".estoque-status")).toHaveText("Não validado");
  await expect(page.locator("#estoque-empty")).toBeHidden();
  // O campo esvazia e mantém o foco: quem cadastra estoque cadastra vários
  // seguidos.
  await expect(campo(page)).toHaveValue("");
  await expect(campo(page)).toBeFocused();

  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

test("nome em branco não adiciona nada", async ({ page }) => {
  await campo(page).fill("   ");
  await campo(page).press("Enter");
  await expect(cards(page)).toHaveCount(0);
});

test("o item sobrevive a recarregar a página", async ({ page }) => {
  await adicionar(page, "Elmo Ancestral");
  await page.reload();
  await expect(card(page, "Elmo Ancestral")).toBeVisible();
});

// O documento não recarrega ao trocar de aba, então o corpo que volta é DOM
// novo: sem religar (ver religarCorpo em navegacao.js), o estoque voltaria
// vazio.
test("o item sobrevive a ir para a Watchlist e voltar", async ({ page, request }) => {
  await adicionar(page, "Elmo Ancestral");
  await zerarContagemDoUpstream(request);

  await page.getByRole("link", { name: "Watchlist" }).click();
  await expect(page.locator(".search-form")).toBeVisible();
  await page.getByRole("link", { name: "Estoque" }).click();

  await expect(card(page, "Elmo Ancestral")).toBeVisible();
  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

test("excluir tira o item e traz de volta a mensagem de vazio", async ({ page }) => {
  await adicionar(page, "Elmo Ancestral");
  await adicionar(page, "Capa do Corvo");
  await expect(cards(page)).toHaveCount(2);

  await card(page, "Elmo Ancestral").locator(".estoque-remover").click();

  await expect(cards(page)).toHaveCount(1);
  await expect(card(page, "Capa do Corvo")).toBeVisible();
  await expect(page.locator("#estoque-empty")).toBeHidden();

  await card(page, "Capa do Corvo").locator(".estoque-remover").click();
  await expect(page.locator("#estoque-empty")).toBeVisible();
});

test("o preço de venda é editável clicando, e persiste", async ({ page, request }) => {
  await adicionar(page, "Elmo Ancestral");
  await zerarContagemDoUpstream(request);
  const preco = card(page, "Elmo Ancestral").locator(".estoque-preco-venda");
  await expect(preco).toHaveText("Vendo por: —");

  await preco.click();
  await preco.locator("input").fill("150000");
  await preco.locator("input").press("Enter");

  await expect(preco).toHaveText("Vendo por: 150.000 z");
  expect(await contarRequisicoesAoUpstream(request)).toBe(0);

  await page.reload();
  await expect(card(page, "Elmo Ancestral").locator(".estoque-preco-venda")).toHaveText("Vendo por: 150.000 z");
});

test("Esc descarta a edição do preço de venda", async ({ page }) => {
  await adicionar(page, "Elmo Ancestral");
  const preco = card(page, "Elmo Ancestral").locator(".estoque-preco-venda");
  await preco.click();
  await preco.locator("input").fill("999");
  await preco.locator("input").press("Escape");

  await expect(preco).toHaveText("Vendo por: —");
});

test("esvaziar o campo e confirmar remove o preço de venda", async ({ page }) => {
  await adicionar(page, "Elmo Ancestral");
  const preco = card(page, "Elmo Ancestral").locator(".estoque-preco-venda");
  await preco.click();
  await preco.locator("input").fill("150000");
  await preco.locator("input").press("Enter");
  await expect(preco).toHaveText("Vendo por: 150.000 z");

  await preco.click();
  await preco.locator("input").fill("");
  await preco.locator("input").press("Enter");

  await expect(preco).toHaveText("Vendo por: —");
});

test("na loja e undercutting ligam e desligam", async ({ page }) => {
  await adicionar(page, "Elmo Ancestral");
  const c = card(page, "Elmo Ancestral");
  const loja = c.locator(".estoque-toggle-loja");
  const undercut = c.locator(".estoque-toggle-undercut");

  // O undercutting não faz sentido fora da loja: não há o que comparar com o
  // mercado se você não está vendendo.
  await expect(loja).toHaveText("Fora da loja");
  await expect(undercut).toBeDisabled();

  await loja.click();
  await expect(loja).toHaveText("Na loja");
  await expect(loja).toHaveAttribute("aria-pressed", "true");
  await expect(undercut).toBeEnabled();

  await undercut.click();
  await expect(undercut).toHaveText("Undercutting ligado");
  await expect(undercut).toHaveAttribute("aria-pressed", "true");

  await page.reload();
  await expect(card(page, "Elmo Ancestral").locator(".estoque-toggle-loja")).toHaveText("Na loja");
  await expect(card(page, "Elmo Ancestral").locator(".estoque-toggle-undercut")).toHaveText("Undercutting ligado");
});

// Deixar a flag ligada guardaria uma intenção que não vale para nada e que
// voltaria a valer sozinha na próxima vez que o item entrasse na loja.
test("sair da loja desliga o undercutting junto", async ({ page }) => {
  await adicionar(page, "Elmo Ancestral");
  const c = card(page, "Elmo Ancestral");
  await c.locator(".estoque-toggle-loja").click();
  await c.locator(".estoque-toggle-undercut").click();
  await expect(c.locator(".estoque-toggle-undercut")).toHaveText("Undercutting ligado");

  await c.locator(".estoque-toggle-loja").click();

  await expect(c.locator(".estoque-toggle-undercut")).toHaveText("Undercutting desligado");
  await expect(c.locator(".estoque-toggle-undercut")).toBeDisabled();
});

// Regressão de contraste: a regra de :hover dos toggles (0,3,0) vencia a do
// .is-on (0,2,0) e pintava texto --accent sobre fundo --accent — o botão
// ligado ficava com o rótulo invisível assim que o mouse encostava nele.
test("o toggle ligado continua legível sob o mouse", async ({ page }) => {
  await adicionar(page, "Elmo Ancestral");
  const loja = card(page, "Elmo Ancestral").locator(".estoque-toggle-loja");
  await loja.click();
  await loja.hover();

  const cores = await loja.evaluate((el) => {
    const estilo = getComputedStyle(el);
    return { texto: estilo.color, fundo: estilo.backgroundColor };
  });
  expect(cores.texto).not.toBe(cores.fundo);
});

test("a janela do histórico é por item e persiste", async ({ page }) => {
  await adicionar(page, "Elmo Ancestral");
  await adicionar(page, "Capa do Corvo");

  await expect(card(page, "Elmo Ancestral").locator(".estoque-janela")).toHaveValue("7");
  await card(page, "Elmo Ancestral").locator(".estoque-janela").selectOption("30");

  // Trocar a janela de um item não mexe na do outro.
  await expect(card(page, "Capa do Corvo").locator(".estoque-janela")).toHaveValue("7");

  await page.reload();
  await expect(card(page, "Elmo Ancestral").locator(".estoque-janela")).toHaveValue("30");
  await expect(card(page, "Capa do Corvo").locator(".estoque-janela")).toHaveValue("7");
});

test("o servidor escolhido persiste e é gravado no item", async ({ page }) => {
  await page.locator("#estoque-servidor").selectOption("FREYA");
  await adicionar(page, "Elmo Ancestral");

  const server = await page.evaluate(
    () => JSON.parse(localStorage.getItem("ro-market-tracker:estoque"))[0].server,
  );
  expect(server).toBe("FREYA");

  await page.reload();
  await expect(page.locator("#estoque-servidor")).toHaveValue("FREYA");
});

test("a ordem de cadastro é a ordem da tela", async ({ page }) => {
  await adicionar(page, "Primeiro");
  await adicionar(page, "Segundo");
  await adicionar(page, "Terceiro");

  await expect(cards(page).locator(".estoque-nome")).toHaveText(["Primeiro", "Segundo", "Terceiro"]);
});


// ---------------------------------------------------------------------------
// Validação
// ---------------------------------------------------------------------------
//
// O servidor procura o item primeiro nos anúncios de agora e, só se ninguém
// estiver vendendo, no histórico de vendas. Os testes abaixo cobrem os três
// desfechos — e o quarto, que não é desfecho nenhum: falhar a consulta não
// pode mexer no estado do item.

const selo = (page, nome) => card(page, nome).locator(".estoque-status");
const botaoValidar = (page, nome) => card(page, nome).locator(".estoque-validar");

test("validar um item anunciado fixa o item e marca como validado", async ({ page, request }) => {
  await adicionar(page, "Bota do Andarilho");
  await zerarContagemDoUpstream(request);

  await botaoValidar(page, "Bota do Andarilho").click();

  await expect(selo(page, "Bota do Andarilho")).toHaveText("Validado");

  // O itemId e o svrId são o que todas as consultas seguintes vão usar: sem
  // eles, validar não serviu para nada.
  const gravado = await page.evaluate(
    () => JSON.parse(localStorage.getItem("ro-market-tracker:estoque"))[0],
  );
  expect(gravado.itemId).toBe(610003);
  expect(gravado.svrId).toBeGreaterThan(0);
  expect(gravado.validacao).toBe("validado");

  // "Bota do Andarilho" não está anunciada: o servidor cai no histórico, e
  // isso são duas requisições.
  expect(await contarRequisicoesAoUpstream(request)).toBe(2);
});

test("um item que alguém está anunciando custa só uma requisição", async ({ page, request }) => {
  await adicionar(page, "Carta Poring Noel");
  await zerarContagemDoUpstream(request);

  await botaoValidar(page, "Carta Poring Noel").click();
  await expect(selo(page, "Carta Poring Noel")).toHaveText("Validado");

  // Com o item no mercado, o histórico nem chega a ser consultado.
  expect(await contarRequisicoesAoUpstream(request)).toBe(1);
});

test("um nome ambíguo pede a escolha, e escolher não custa requisição", async ({ page, request }) => {
  await adicionar(page, "Rapidez");
  await botaoValidar(page, "Rapidez").click();

  const candidatos = card(page, "Rapidez").locator(".estoque-candidato");
  await expect(candidatos).toHaveCount(2);
  await expect(selo(page, "Rapidez")).toHaveText("Não validado");

  await zerarContagemDoUpstream(request);
  await candidatos.filter({ hasText: "Módulo de S-Rapidez" }).first().click();

  await expect(selo(page, "Rapidez")).toHaveText("Validado");
  await expect(card(page, "Rapidez").locator(".estoque-candidato")).toHaveCount(0);
  expect(await contarRequisicoesAoUpstream(request)).toBe(0);

  const gravado = await page.evaluate(
    () => JSON.parse(localStorage.getItem("ro-market-tracker:estoque"))[0],
  );
  expect(gravado.itemId).toBe(25690);
  expect(gravado.candidatos).toBe(null);
});

// Os candidatos são persistidos justamente para isto: trocar de aba no meio
// da escolha e voltar não pode custar outra consulta ao site.
test("a lista de candidatos sobrevive à troca de aba", async ({ page, request }) => {
  await adicionar(page, "Rapidez");
  await botaoValidar(page, "Rapidez").click();
  await expect(card(page, "Rapidez").locator(".estoque-candidato")).toHaveCount(2);

  await zerarContagemDoUpstream(request);
  await page.getByRole("link", { name: "Watchlist" }).click();
  await expect(page.locator(".search-form")).toBeVisible();
  await page.getByRole("link", { name: "Estoque" }).click();

  await expect(card(page, "Rapidez").locator(".estoque-candidato")).toHaveCount(2);
  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

test("um item que nunca existiu fica inválido e explica o motivo", async ({ page }) => {
  await adicionar(page, "Item Que Nao Existe");

  await botaoValidar(page, "Item Que Nao Existe").click();

  await expect(selo(page, "Item Que Nao Existe")).toHaveText("Inválido");
  await expect(card(page, "Item Que Nao Existe").locator(".estoque-motivo")).toContainText(
    "Confira o nome",
  );
  // O caminho que o usuário tem para sair daqui.
  await expect(botaoValidar(page, "Item Que Nao Existe")).toHaveText("Tentar de novo");
  await expect(card(page, "Item Que Nao Existe").locator(".estoque-remover")).toBeVisible();
});

// A asserção mais importante da etapa. Inválido é o estado que manda o
// usuário apagar o cadastro — um tropeço do site não pode mandar isso.
test("falha ao consultar não invalida o item", async ({ page, request }) => {
  await adicionar(page, "Bota do Andarilho");
  await falharProximasRequisicoes(request, { status: 500, times: 10 });

  await botaoValidar(page, "Bota do Andarilho").click();

  await expect(selo(page, "Bota do Andarilho")).toHaveText("Não validado");
  await expect(card(page, "Bota do Andarilho").locator(".estoque-motivo")).toHaveCount(0);
  await expect(page.locator(".toast")).toBeVisible();
});

// Sem segurar a resposta, esta asserção seria uma corrida com a rede e
// passaria por acaso. O atraso no site falso é o que torna o estado em voo
// observável de verdade.
test("o botão desabilita enquanto a consulta está em voo", async ({ page, request }) => {
  await adicionar(page, "Bota do Andarilho");
  await atrasarProximasRequisicoes(request, { ms: 1500, times: 1 });

  const botao = botaoValidar(page, "Bota do Andarilho");
  await botao.click();

  await expect(botao).toBeDisabled();
  await expect(botao).toHaveText("Validando…");

  await expect(selo(page, "Bota do Andarilho")).toHaveText("Validado");
  await expect(botao).toBeEnabled();
});

test("revalidar um item corrigido sai do estado inválido", async ({ page }) => {
  await adicionar(page, "Item Que Nao Existe");
  await botaoValidar(page, "Item Que Nao Existe").click();
  await expect(selo(page, "Item Que Nao Existe")).toHaveText("Inválido");

  await card(page, "Item Que Nao Existe").locator(".estoque-remover").click();
  await adicionar(page, "Bota do Andarilho");
  await botaoValidar(page, "Bota do Andarilho").click();

  await expect(selo(page, "Bota do Andarilho")).toHaveText("Validado");
});

test("o estado de validação sobrevive a recarregar", async ({ page }) => {
  await adicionar(page, "Bota do Andarilho");
  await botaoValidar(page, "Bota do Andarilho").click();
  await expect(selo(page, "Bota do Andarilho")).toHaveText("Validado");

  await page.reload();

  await expect(selo(page, "Bota do Andarilho")).toHaveText("Validado");
});
