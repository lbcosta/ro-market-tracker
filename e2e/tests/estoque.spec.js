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

  // Escolher em si não custa nada — o itemId já tinha vindo na validação. E
  // como estes candidatos vieram do histórico (ninguém anuncia "Rapidez"), a
  // consulta de mercado também não sai: a validação já provou que não há
  // anúncio nenhum, e o resultado é semeado à mão.
  //
  // Sobra UMA requisição, a da série diária do item escolhido. Esperar por ela
  // antes de contar não é detalhe do teste: sem isso a asserção corre contra a
  // rede e passa por acaso, que foi o que aconteceu até este comentário ser
  // escrito.
  await expect(card(page, "Rapidez").locator(".estoque-historico-resumo")).toBeVisible();
  expect(await contarRequisicoesAoUpstream(request)).toBe(1);

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
  // Esperar o histórico, e não só o selo: validar dispara mercado e histórico
  // em seguida, e o selo fica verde antes de os dois responderem. Sem esta
  // espera, um teste que zera o contador de requisições logo depois conta as
  // que ainda estavam em voo.
  await expect(card(page, nome).locator(".estoque-historico-resumo")).toBeVisible();
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

// ---------------------------------------------------------------------------
// Histórico de vendas e a janela
// ---------------------------------------------------------------------------
//
// "Elixir do Mercador" (itemId 700001) tem 32 dias de histórico nas fixtures —
// é o item que existe justamente para o seletor de janela ter o que recortar.

const historico = (page, nome) => card(page, nome).locator(".estoque-historico-resumo");
const diasDoHistorico = (page, nome) => card(page, nome).locator(".estoque-dias tbody tr");
const janela = (page, nome) => card(page, nome).locator(".estoque-janela");

test("validar traz o histórico junto, na janela padrão de 7 dias", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");

  await expect(janela(page, "Elixir do Mercador")).toHaveValue("7");
  await expect(historico(page, "Elixir do Mercador")).toContainText("Vendido entre");
  // A tabela nasce recolhida para não esticar o card.
  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-dias")).not.toHaveAttribute("open", "");

  await card(page, "Elixir do Mercador").locator(".estoque-historico-dias summary").click();
  await expect(diasDoHistorico(page, "Elixir do Mercador")).toHaveCount(7);
});

// O card diz quantos dias vieram E quantos existem — é isso que avisa que
// trocar para uma janela maior tem o que mostrar.
test("o card mostra quantos dias existem além da janela atual", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");

  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-dias summary")).toContainText(
    "7 dias com venda de 32 registrados",
  );
});

test("trocar a janela reconsulta só aquele item", async ({ page, request }) => {
  await validarItem(page, "Elixir do Mercador");
  await validarItem(page, "Espada Primordial");
  await zerarContagemDoUpstream(request);

  await janela(page, "Elixir do Mercador").selectOption("30");

  // Esperar o resumo antes de abrir a tabela: clicar no <summary> ALTERNA o
  // <details>, então repetir o clique enquanto se espera (dentro de um poll,
  // por exemplo) fecharia o que acabou de abrir.
  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-dias summary")).toContainText(
    "30 dias com venda",
  );
  await card(page, "Elixir do Mercador").locator(".estoque-historico-dias summary").click();
  await expect(diasDoHistorico(page, "Elixir do Mercador")).toHaveCount(30);

  // Uma requisição, e só do item que mudou. Um seletor global cobraria isso de
  // todos os itens de uma vez.
  expect(await contarRequisicoesAoUpstream(request)).toBe(1);
});

test("a janela de um dia traz um dia só", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");

  await janela(page, "Elixir do Mercador").selectOption("1");
  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-dias summary")).toContainText(
    "1 dia com venda",
  );
});

test("todo o histórico traz os 32 dias", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");

  await janela(page, "Elixir do Mercador").selectOption("ALL");

  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-dias summary")).toContainText(
    "32 dias com venda",
  );
  await card(page, "Elixir do Mercador").locator(".estoque-historico-dias summary").click();
  await expect(diasDoHistorico(page, "Elixir do Mercador")).toHaveCount(32);
});

test("voltar para uma janela já consultada não custa requisição", async ({ page, request }) => {
  await validarItem(page, "Elixir do Mercador");
  await janela(page, "Elixir do Mercador").selectOption("30");
  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-dias summary")).toContainText(
    "30 dias com venda",
  );

  await zerarContagemDoUpstream(request);
  await janela(page, "Elixir do Mercador").selectOption("7");
  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-dias summary")).toContainText(
    "7 dias com venda",
  );

  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

test("a janela escolhida e o histórico sobrevivem à troca de aba", async ({ page, request }) => {
  await validarItem(page, "Elixir do Mercador");
  await janela(page, "Elixir do Mercador").selectOption("30");
  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-dias summary")).toContainText(
    "30 dias com venda",
  );

  await zerarContagemDoUpstream(request);
  await page.getByRole("link", { name: "Watchlist" }).click();
  await expect(page.locator(".search-form")).toBeVisible();
  await page.getByRole("link", { name: "Estoque" }).click();

  await expect(janela(page, "Elixir do Mercador")).toHaveValue("30");
  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-dias summary")).toContainText(
    "30 dias com venda",
  );
  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

test("um item sem vendas registradas diz isso", async ({ page }) => {
  // A Carta Poring Noel está anunciada mas tem histórico vazio nas fixtures.
  await validarItem(page, "Carta Poring Noel");

  await expect(historico(page, "Carta Poring Noel")).toContainText("Sem vendas registradas");
});

// ---------------------------------------------------------------------------
// Preço sugerido
// ---------------------------------------------------------------------------
//
// A regra que manda em tudo aqui: a PRECISÃO DA RESPOSTA ACOMPANHA A CONFIANÇA
// DO DADO. O histórico do site é indexado por itemId, mas refino e
// encantamento são da unidade — para equipamento, a série soma coisas
// diferentes, e recomendar um preço exato em cima disso seria afirmar o que
// não se sabe.

const sugestao = (page, nome) => card(page, nome).locator(".estoque-sugestao-faixa");
const confianca = (page, nome) => card(page, nome).locator(".estoque-confianca");
const cenarios = (page, nome) => card(page, nome).locator(".estoque-cenario");

test("um item homogêneo ganha faixa, confiança alta e os três cenários", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");

  await expect(confianca(page, "Elixir do Mercador")).toHaveText("confiança alta");
  await expect(sugestao(page, "Elixir do Mercador")).toContainText("Sugerido:");

  await card(page, "Elixir do Mercador").locator(".estoque-cenarios summary").click();
  await expect(cenarios(page, "Elixir do Mercador")).toHaveCount(3);
  await expect(cenarios(page, "Elixir do Mercador").first()).toContainText("Vender hoje");
});

// O concorrente mais barato do Elixir é 1.200 z; dez por cento abaixo é 1.080.
test("o cenário de vender hoje fica 10% abaixo do concorrente mais barato", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");
  await card(page, "Elixir do Mercador").locator(".estoque-cenarios summary").click();

  await expect(cenarios(page, "Elixir do Mercador").first()).toContainText("1.080 z");
});

// A aposta do "Segurar" precisa estar escrita em algum lugar: é a única
// estratégia que depende de uma previsão do usuário sobre o jogo.
test("o cenário de segurar explica a aposta que embute", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");
  await card(page, "Elixir do Mercador").locator(".estoque-cenarios summary").click();

  const segurar = cenarios(page, "Elixir do Mercador").filter({ hasText: "Segurar" });
  await expect(segurar).toContainText("apostando em alta");
  await expect(segurar).toContainText("atualização do jogo");
  await expect(segurar).toContainText("ficar parado");
});

// A Espada Primordial vendeu entre 500 z e 5.000 z nas fixtures — dispersão
// alta num item "weapon", que é exatamente o caso contaminado.
test("um equipamento disperso fica com confiança baixa e SEM cenários", async ({ page }) => {
  await validarItem(page, "Espada Primordial");

  await expect(confianca(page, "Espada Primordial")).toHaveText("confiança baixa");
  await expect(card(page, "Espada Primordial").locator(".estoque-confianca-motivo")).toContainText(
    "não distingue refino nem encantamento",
  );
  // A faixa aparece; o preço recomendado, não.
  await expect(sugestao(page, "Espada Primordial")).toContainText("Sugerido:");
  await expect(card(page, "Espada Primordial").locator(".estoque-cenarios")).toHaveCount(0);
});

test("um item sem vendas registradas não sugere preço nenhum", async ({ page }) => {
  await validarItem(page, "Carta Poring Noel");

  await expect(sugestao(page, "Carta Poring Noel")).toContainText("Sem preço sugerido");
  await expect(confianca(page, "Carta Poring Noel")).toHaveText("sem dados suficientes");
  await expect(card(page, "Carta Poring Noel").locator(".estoque-cenarios")).toHaveCount(0);
});

// A colocação na fila é a resposta imediata a quem mexe no próprio preço, e é
// um FATO exato — contagem de anúncios, sem depender de liquidez nem da
// confiança do dado. Foi a falta dela que fazia o card parecer inerte num
// equipamento: os baldes grosseiros do tempo ("provavelmente dias") quase não
// se mexem, e o "Sugerido" não depende do que você está pedindo.
test("mudar o preço muda a colocação e a distância, exatamente", async ({ page, request }) => {
  await validarItem(page, "Espada Primordial");
  const c = card(page, "Espada Primordial");
  const linha = c.locator(".estoque-sugestao-seu");
  await zerarContagemDoUpstream(request);

  // Anúncios: 129.999.999 / 158.000.000 / 299.999.999.
  const casos = [
    ["100000000", "1º de 4 anúncios", "23% abaixo do mais barato"],
    ["140000000", "2º de 4 anúncios", "8% acima do mais barato"],
    ["200000000", "3º de 4 anúncios", "54% acima do mais barato"],
    ["500000000", "4º de 4 anúncios", "285% acima do mais barato"],
  ];
  for (const [preco, colocacao, distancia] of casos) {
    await c.locator(".estoque-preco-venda").click();
    await c.locator("input").fill(preco);
    await c.locator("input").press("Enter");
    await expect(linha).toContainText(colocacao);
    await expect(linha).toContainText(distancia);
  }

  // Estar em primeiro é o estado que quem vende persegue, e é destacado.
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("100000000");
  await c.locator("input").press("Enter");
  await expect(linha).toHaveClass(/is-primeiro/);

  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

// Mesmo num item onde NENHUM preço é recomendado, a colocação aparece: ela não
// é estimativa, é contagem.
test("a colocação aparece mesmo com confiança baixa", async ({ page }) => {
  await validarItem(page, "Espada Primordial");
  const c = card(page, "Espada Primordial");
  await expect(c.locator(".estoque-cenarios")).toHaveCount(0);

  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("200000000");
  await c.locator("input").press("Enter");

  await expect(c.locator(".estoque-sugestao-seu")).toContainText("3º de 4 anúncios");
});

// O mercado é dinâmico: a fila muda a cada atualização, e o seu preço é insumo
// dela. Editar o preço tem que refazer a conta na hora, sem requisição.
test("editar o preço recalcula o tempo até vender, de graça", async ({ page, request }) => {
  await validarItem(page, "Elixir do Mercador");
  const c = card(page, "Elixir do Mercador");
  await zerarContagemDoUpstream(request);

  // Abaixo dos dois concorrentes (1.200 e 1.500): ninguém na frente. O total
  // é 3 porque conta você junto.
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("1000");
  await c.locator("input").press("Enter");
  await expect(c.locator(".estoque-sugestao-seu")).toContainText("1º de 3 anúncios");

  // Acima dos dois: a fila inteira na frente.
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("2000");
  await c.locator("input").press("Enter");
  await expect(c.locator(".estoque-sugestao-seu")).toContainText("3º de 3 anúncios");

  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

// Mexer na lista de personagens muda quem é concorrência, e com isso a fila —
// para o estoque inteiro, e sem custo nenhum.
test("um personagem seu sai da fila que está na sua frente", async ({ page, request }) => {
  await validarItem(page, "Elixir do Mercador");
  const c = card(page, "Elixir do Mercador");
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("1400");
  await c.locator("input").press("Enter");
  // Entre 1.200 e 1.500: segundo de três.
  await expect(c.locator(".estoque-sugestao-seu")).toContainText("2º de 3 anúncios");

  await zerarContagemDoUpstream(request);
  // O anúncio de 1.200 z é do "Vendedor elixir-a". Reconhecido como seu, ele
  // sai da concorrência — e você passa a ser o primeiro dos dois que restam.
  await adicionarPersonagem(page, "Vendedor elixir-a");

  await expect(card(page, "Elixir do Mercador").locator(".estoque-sugestao-seu")).toContainText(
    "1º de 2 anúncios",
  );
  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

// ---------------------------------------------------------------------------
// Loja offline
// ---------------------------------------------------------------------------
//
// O caso que isto existe para pegar é o desconexão silenciosa: a loja caiu e o
// usuário não viu. O sinal é indireto — nenhum anúncio seu aparece entre os
// itens que você marcou como "na loja" —, e por isso o aviso descreve o que
// foi observado em vez de cravar a causa: uma loja fechada e um estoque que
// vendeu tudo somem do mercado exatamente igual.

const avisoDeLoja = (page) => page.locator("#estoque-loja-aviso");

async function porNaLoja(page, nome) {
  await card(page, nome).locator(".estoque-toggle-loja").click();
  await expect(card(page, nome).locator(".estoque-toggle-loja")).toHaveText("Na loja");
}

test("sem personagens cadastrados o aviso nunca aparece", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");
  await porNaLoja(page, "Elixir do Mercador");

  // Sem saber quais anúncios são seus, "nenhum anúncio seu" não significa nada.
  await expect(avisoDeLoja(page)).toBeHidden();
});

test("com anúncio seu no mercado, nenhum aviso", async ({ page }) => {
  await adicionarPersonagem(page, "Vendedor elixir-a");
  await validarItem(page, "Elixir do Mercador");
  await porNaLoja(page, "Elixir do Mercador");

  await expect(avisoDeLoja(page)).toBeHidden();
});

test("sem nenhum anúncio seu, o aviso aparece e explica a ambiguidade", async ({ page }) => {
  await adicionarPersonagem(page, "Personagem Que Nao Vende");
  await validarItem(page, "Elixir do Mercador");
  await porNaLoja(page, "Elixir do Mercador");

  await expect(avisoDeLoja(page)).toBeVisible();
  await expect(avisoDeLoja(page)).toContainText("não está aparecendo no mercado");
  // A ambiguidade é dita, não escondida.
  await expect(avisoDeLoja(page)).toContainText("pode ter caído, ou tudo pode ter sido vendido");
  // E de quando é a evidência.
  await expect(avisoDeLoja(page)).toContainText("Checagem");
});

test("itens fora da loja não contam para o aviso", async ({ page }) => {
  await adicionarPersonagem(page, "Personagem Que Nao Vende");
  await validarItem(page, "Elixir do Mercador");

  // Validado, mas não marcado como "na loja": você não está vendendo isto,
  // então a ausência dele no mercado não diz nada sobre a sua loja.
  await expect(avisoDeLoja(page)).toBeHidden();
});

test("tirar o item da loja esconde o aviso de novo", async ({ page }) => {
  await adicionarPersonagem(page, "Personagem Que Nao Vende");
  await validarItem(page, "Elixir do Mercador");
  await porNaLoja(page, "Elixir do Mercador");
  await expect(avisoDeLoja(page)).toBeVisible();

  await card(page, "Elixir do Mercador").locator(".estoque-toggle-loja").click();

  await expect(avisoDeLoja(page)).toBeHidden();
});

// O aviso é sobre o conjunto: basta UM item com anúncio seu para não haver
// motivo de alarme.
test("um item com anúncio seu basta para não alarmar", async ({ page }) => {
  await adicionarPersonagem(page, "Vendedor elixir-a");
  await validarItem(page, "Elixir do Mercador");
  await porNaLoja(page, "Elixir do Mercador");
  await validarItem(page, "Espada Primordial");
  await porNaLoja(page, "Espada Primordial");

  await expect(avisoDeLoja(page)).toBeHidden();
});

test("o aviso sobrevive à troca de aba", async ({ page }) => {
  await adicionarPersonagem(page, "Personagem Que Nao Vende");
  await validarItem(page, "Elixir do Mercador");
  await porNaLoja(page, "Elixir do Mercador");
  await expect(avisoDeLoja(page)).toBeVisible();

  await page.getByRole("link", { name: "Watchlist" }).click();
  await expect(page.locator(".search-form")).toBeVisible();
  await page.getByRole("link", { name: "Estoque" }).click();

  await expect(avisoDeLoja(page)).toBeVisible();
});

// ---------------------------------------------------------------------------
// Régua de preços
// ---------------------------------------------------------------------------

const regua = (page, nome) => card(page, nome).locator(".estoque-regua");
const tiques = (page, nome) => card(page, nome).locator(".estoque-regua-tique");

test("a régua mostra um tique por anúncio e destaca o seu preço", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");
  const c = card(page, "Elixir do Mercador");
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("1400");
  await c.locator("input").press("Enter");

  // Dois concorrentes mais o seu preço.
  await expect(tiques(page, "Elixir do Mercador")).toHaveCount(3);
  await expect(c.locator(".estoque-regua-tique.is-seu-preco")).toHaveCount(1);
  await expect(regua(page, "Elixir do Mercador")).toHaveAttribute("role", "img");
});

test("a régua marca os seus anúncios separados dos concorrentes", async ({ page }) => {
  await adicionarPersonagem(page, "Vendedor elixir-a");
  await validarItem(page, "Elixir do Mercador");

  await expect(card(page, "Elixir do Mercador").locator(".estoque-regua-tique.is-meu")).toHaveCount(1);
});

// Escala logarítmica: num item cujos anúncios vão de 200k a 88kk, uma escala
// linear empilharia os baratos num pixel e esconderia justamente a estrutura
// que a régua existe para mostrar.
test("a posição dos tiques usa escala logarítmica", async ({ page }) => {
  await validarItem(page, "Espada Primordial");

  const posicoes = await page.evaluate(() =>
    [...document.querySelectorAll(".estoque-card .estoque-regua-tique")].map((el) =>
      parseFloat(el.style.left),
    ),
  );
  expect(posicoes.length).toBeGreaterThanOrEqual(2);
  // As pontas ancoram em 0% e 100%.
  expect(Math.min(...posicoes)).toBeCloseTo(0, 1);
  expect(Math.max(...posicoes)).toBeCloseTo(100, 1);
});

test("sem concorrência suficiente a régua não aparece", async ({ page }) => {
  // A Carta Poring Noel tem um anúncio só: não há o que comparar.
  await validarItem(page, "Carta Poring Noel");

  await expect(regua(page, "Carta Poring Noel")).toHaveCount(0);
});
