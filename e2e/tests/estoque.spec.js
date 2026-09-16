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

  // Dropdown, e não uma lista de botões: os cards dividem uma grade, e uma
  // lista crescia o card proporcionalmente ao número de candidatos.
  const seletor = card(page, "Rapidez").locator(".estoque-candidatos-select");
  await expect(seletor.locator("option")).toHaveCount(2);
  await expect(selo(page, "Rapidez")).toHaveText("Não validado");

  await zerarContagemDoUpstream(request);
  await seletor.selectOption("25690");
  await card(page, "Rapidez").locator(".estoque-escolha-ok").click();

  await expect(selo(page, "Rapidez")).toHaveText("Validado");
  await expect(card(page, "Rapidez").locator(".estoque-escolha")).toHaveCount(0);

  // Escolher em si não custa nada — o itemId já tinha vindo na validação. A
  // requisição que sai é a primeira consulta de mercado do card, que o
  // usuário não deveria precisar pedir de novo no "↻" depois de já ter
  // mandado validar. Como estes candidatos vieram do histórico (ninguém
  // anuncia "Rapidez"), nem essa sai: a validação já provou que não há
  // anúncio nenhum.
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
  const opcoes = card(page, "Rapidez").locator(".estoque-candidatos-select option");
  await expect(opcoes).toHaveCount(2);

  await zerarContagemDoUpstream(request);
  await page.getByRole("link", { name: "Watchlist" }).click();
  await expect(page.locator(".search-form")).toBeVisible();
  await page.getByRole("link", { name: "Estoque" }).click();

  await expect(card(page, "Rapidez").locator(".estoque-candidatos-select option")).toHaveCount(2);
  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

// O motivo de o dropdown existir: a lista antiga crescia o card em uma linha
// por candidato, e como os cards dividem uma grade, um item ambíguo
// desalinhava a linha inteira.
test("o card com escolha não fica muito mais alto que os outros", async ({ page }) => {
  await adicionar(page, "Rapidez");
  await adicionar(page, "Bota do Andarilho");
  const alturaSimples = (await card(page, "Bota do Andarilho").boundingBox()).height;

  await botaoValidar(page, "Rapidez").click();
  await expect(card(page, "Rapidez").locator(".estoque-candidatos-select")).toBeVisible();

  const alturaComEscolha = (await card(page, "Rapidez").boundingBox()).height;
  // Uma linha de título mais uma de seletor, independentemente de quantos
  // candidatos houver — que é o ponto da mudança.
  expect(alturaComEscolha - alturaSimples).toBeLessThan(80);
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

// ---------------------------------------------------------------------------
// Dados de mercado e "meus personagens"
// ---------------------------------------------------------------------------
//
// Os três anúncios da Espada Primordial nas fixtures, do mais barato ao mais
// caro, com o vendedor de cada um:
//
//   129.999.999  Vendedor s-primordial-129
//   158.000.000  Vendedor s-primordial-158
//   299.999.999  Vendedor s-primordial-299

const mercado = (page, nome) => card(page, nome).locator(".estoque-mercado-linha");
const meuAnuncio = (page, nome) => card(page, nome).locator(".estoque-mercado-seu");

async function validarItem(page, nome) {
  await adicionar(page, nome);
  await botaoValidar(page, nome).click();
  await expect(selo(page, nome)).toHaveText("Validado");
}

async function adicionarPersonagem(page, nome) {
  // Clicar no summary ALTERNA o <details>: chamar isto duas vezes fecharia o
  // bloco e o campo sumiria.
  await page.locator("#estoque-personagens").evaluate((el) => {
    el.open = true;
  });
  await page.fill("#estoque-personagem", nome);
  await page.press("#estoque-personagem", "Enter");
  await expect(page.locator(".estoque-personagem").filter({ hasText: nome })).toBeVisible();
}

test("validar preenche o bloco de mercado do item", async ({ page }) => {
  await validarItem(page, "Espada Primordial");

  await expect(mercado(page, "Espada Primordial")).toContainText("129.999.999 z");
  await expect(mercado(page, "Espada Primordial")).toContainText("3 anúncios");
  // Sem personagens cadastrados, nenhum anúncio é reconhecido como seu.
  await expect(meuAnuncio(page, "Espada Primordial")).toHaveCount(0);
});

// O ponto central da etapa: sem isto, você competiria consigo mesmo.
test("um personagem seu deixa de contar como concorrência", async ({ page, request }) => {
  await validarItem(page, "Espada Primordial");
  await expect(mercado(page, "Espada Primordial")).toContainText("129.999.999 z");

  await zerarContagemDoUpstream(request);
  await adicionarPersonagem(page, "Vendedor s-primordial-129");

  // O anúncio mais barato virou o SEU, então a concorrência agora começa no
  // segundo.
  await expect(mercado(page, "Espada Primordial")).toContainText("158.000.000 z");
  await expect(mercado(page, "Espada Primordial")).toContainText("2 anúncios");
  await expect(meuAnuncio(page, "Espada Primordial")).toContainText("129.999.999 z");

  // E o recálculo é de graça: se ele custasse uma requisição por item, um
  // estoque de vinte itens levaria vinte segundos de fila.
  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

test("quando todos os anúncios são seus, você está sozinho no mercado", async ({ page }) => {
  await validarItem(page, "Espada Primordial");

  for (const nome of ["Vendedor s-primordial-129", "Vendedor s-primordial-158", "Vendedor s-primordial-299"]) {
    await adicionarPersonagem(page, nome);
  }

  await expect(mercado(page, "Espada Primordial")).toContainText("Você é o único anunciando");
});

// O aviso que interessa a quem vende. (O alerta ativo — toast, som, Telegram
// — é a etapa seguinte; aqui é só o que o card mostra.)
test("o card avisa quando alguém está vendendo mais barato que você", async ({ page }) => {
  await validarItem(page, "Espada Primordial");
  await adicionarPersonagem(page, "Vendedor s-primordial-299");

  await expect(meuAnuncio(page, "Espada Primordial")).toContainText("299.999.999 z");
  await expect(meuAnuncio(page, "Espada Primordial")).toContainText("estão vendendo mais barato");
  await expect(meuAnuncio(page, "Espada Primordial")).toHaveClass(/estoque-mercado-cortado/);
});

test("um item que ninguém anuncia diz isso, em vez de parecer vazio", async ({ page }) => {
  await validarItem(page, "Bota do Andarilho");

  await expect(mercado(page, "Bota do Andarilho")).toContainText("Ninguém está anunciando");
});

test("o botão de atualizar consulta o mercado de novo", async ({ page, request }) => {
  await validarItem(page, "Espada Primordial");
  await zerarContagemDoUpstream(request);

  await card(page, "Espada Primordial").locator(".estoque-atualizar").click();

  await expect
    .poll(() => contarRequisicoesAoUpstream(request))
    .toBe(1);
  await expect(mercado(page, "Espada Primordial")).toContainText("129.999.999 z");
});

test("os dados de mercado sobrevivem à troca de aba sem reconsultar", async ({ page, request }) => {
  await validarItem(page, "Espada Primordial");
  await zerarContagemDoUpstream(request);

  await page.getByRole("link", { name: "Watchlist" }).click();
  await expect(page.locator(".search-form")).toBeVisible();
  await page.getByRole("link", { name: "Estoque" }).click();

  await expect(mercado(page, "Espada Primordial")).toContainText("129.999.999 z");
  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

test("a lista de personagens persiste e pode ser esvaziada", async ({ page }) => {
  await adicionarPersonagem(page, "Vendedor s-primordial-129");
  await expect(page.locator("#estoque-personagens-contagem")).toHaveText("1");

  await page.reload();
  await page.locator("#estoque-personagens").evaluate((el) => {
    el.open = true;
  });
  await expect(page.locator(".estoque-personagem")).toHaveCount(1);

  await page.locator(".estoque-personagem-remover").first().click();
  await expect(page.locator(".estoque-personagem")).toHaveCount(0);
  await expect(page.locator("#estoque-personagens-contagem")).toHaveText("0");
});

test("o mesmo personagem não entra duas vezes", async ({ page }) => {
  await adicionarPersonagem(page, "Vendedor s-primordial-129");

  await page.fill("#estoque-personagem", "vendedor S-PRIMORDIAL-129");
  await page.press("#estoque-personagem", "Enter");

  await expect(page.locator(".estoque-personagem")).toHaveCount(1);
  await expect(page.locator(".toast")).toBeVisible();
});
