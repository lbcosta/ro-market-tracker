const { test, expect } = require("@playwright/test");
const { resetPage, contarRequisicoesAoUpstream, zerarContagemDoUpstream } = require("./helpers");

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
