const { test, expect } = require("@playwright/test");
const {
  resetPage,
  contarRequisicoesAoUpstream,
  zerarContagemDoUpstream,
  falharProximasRequisicoes,
  atrasarProximasRequisicoes,
} = require("./helpers");

// A tela é mestre-detalhe: a tabela da esquerda responde "qual item precisa de
// mim?" e o painel da direita, "o que faço com este?". Só UM card existe por
// vez — o do item aberto —, então todo teste que mexe num item precisa
// abri-lo antes. É o que abrir() faz.
//
// Vários testes conferem o contador de requisições ao upstream, porque "zero
// requisição" em boa parte desta tela é requisito, não acaso.

const campo = (page) => page.locator("#estoque-item");
const linhas = (page) => page.locator(".estoque-linha");
const linha = (page, nome) => page.locator(".estoque-linha").filter({ hasText: nome });
const card = (page, nome) => page.locator("#estoque-detalhe .estoque-card").filter({ hasText: nome });

// esperarHistorico espera pelo DADO, e não por um elemento.
//
// O painel desenha os tiles assim que o mercado responde, com a faixa ainda
// vazia — então "o tile está visível" NÃO prova que o histórico chegou. Um
// teste que zere o contador de requisições confiando no elemento conta, na
// verdade, a requisição do histórico que ainda estava em voo.
function esperarHistorico(page) {
  return expect
    .poll(() =>
      page.evaluate(() => {
        const lista = JSON.parse(localStorage.getItem("ro-market-tracker:estoque") || "[]");
        const id = localStorage.getItem("ro-market-tracker:estoque-selecionado");
        const item = lista.find((e) => e.id === id);
        return Boolean(item && item.historico);
      }),
    )
    .toBe(true);
}

async function abrirAba(page, rotulo) {
  await page.locator(".estoque-aba").filter({ hasText: rotulo }).click();
}

async function abrir(page, nome) {
  await linha(page, nome).click();
  await expect(card(page, nome)).toBeVisible();
}

async function adicionar(page, nome) {
  await campo(page).fill(nome);
  await campo(page).press("Enter");
  await expect(linha(page, nome)).toBeVisible();
  // Cadastrar abre o item: o passo seguinte é sempre validá-lo, e o botão
  // está no painel.
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
  await expect(linhas(page)).toHaveCount(0);
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
  await expect(linhas(page)).toHaveCount(0);
});

test("o item sobrevive a recarregar a página", async ({ page }) => {
  await adicionar(page, "Elmo Ancestral");
  await page.reload();
  await expect(linha(page, "Elmo Ancestral")).toBeVisible();
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
  await expect(linhas(page)).toHaveCount(2);

  await abrir(page, "Elmo Ancestral");
  await card(page, "Elmo Ancestral").locator(".estoque-remover").click();

  await expect(linhas(page)).toHaveCount(1);
  await expect(linha(page, "Capa do Corvo")).toBeVisible();
  await expect(page.locator("#estoque-empty")).toBeHidden();
  // Remover o item ABERTO esvazia o painel: uma seleção apontando para o que
  // já não existe deixaria a direita presa num vazio sem correspondência.
  await expect(page.locator(".estoque-detalhe-vazio")).toBeVisible();

  await abrir(page, "Capa do Corvo");
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
  await abrir(page, "Elmo Ancestral");
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
  const undercut = c.locator(".estoque-sino");

  // O undercutting não faz sentido fora da loja: não há o que comparar com o
  // mercado se você não está vendendo.
  await expect(loja).toHaveText("Fora da loja");
  await expect(undercut).toBeDisabled();

  await loja.click();
  await expect(loja).toHaveText("Na loja");
  await expect(loja).toHaveAttribute("aria-pressed", "true");
  await expect(undercut).toBeEnabled();

  await undercut.click();
  await expect(undercut).toHaveAttribute("aria-pressed", "true");
  await expect(undercut).toHaveAttribute("aria-pressed", "true");

  await page.reload();
  await abrir(page, "Elmo Ancestral");
  await expect(card(page, "Elmo Ancestral").locator(".estoque-toggle-loja")).toHaveText("Na loja");
  await expect(card(page, "Elmo Ancestral").locator(".estoque-sino")).toHaveAttribute("aria-pressed", "true");
});

// Deixar a flag ligada guardaria uma intenção que não vale para nada e que
// voltaria a valer sozinha na próxima vez que o item entrasse na loja.
test("sair da loja desliga o undercutting junto", async ({ page }) => {
  await adicionar(page, "Elmo Ancestral");
  const c = card(page, "Elmo Ancestral");
  await c.locator(".estoque-toggle-loja").click();
  await c.locator(".estoque-sino").click();
  await expect(c.locator(".estoque-sino")).toHaveAttribute("aria-pressed", "true");

  await c.locator(".estoque-toggle-loja").click();

  await expect(c.locator(".estoque-sino")).toHaveAttribute("aria-pressed", "false");
  await expect(c.locator(".estoque-sino")).toBeDisabled();
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

  await abrir(page, "Elmo Ancestral");
  await expect(card(page, "Elmo Ancestral").locator(".estoque-janela")).toHaveValue("7");
  await card(page, "Elmo Ancestral").locator(".estoque-janela").selectOption("30");

  // Trocar a janela de um item não mexe na do outro.
  await abrir(page, "Capa do Corvo");
  await expect(card(page, "Capa do Corvo").locator(".estoque-janela")).toHaveValue("7");

  await page.reload();
  await abrir(page, "Elmo Ancestral");
  await expect(card(page, "Elmo Ancestral").locator(".estoque-janela")).toHaveValue("30");
  await abrir(page, "Capa do Corvo");
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

  await expect(linhas(page).locator(".estoque-linha-nome")).toHaveText(["Primeiro", "Segundo", "Terceiro"]);
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
  await esperarHistorico(page);
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
  await abrir(page, "Rapidez");

  await expect(card(page, "Rapidez").locator(".estoque-candidatos-select option")).toHaveCount(2);
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
  await abrir(page, "Bota do Andarilho");

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

const tarja = (page, nome) => card(page, nome).locator(".estoque-tarja-texto");
const tileMaisBarato = (page, nome) => card(page, nome).locator(".estoque-tile-mais-barato");

async function validarItem(page, nome) {
  await adicionar(page, nome);
  await botaoValidar(page, nome).click();
  await expect(selo(page, nome)).toHaveText("Validado");
  // Esperar os tiles, e não só o selo: validar dispara mercado e histórico em
  // seguida, e o selo fica verde antes de os dois responderem. Sem esta
  // espera, um teste que zera o contador de requisições logo depois conta as
  // que ainda estavam em voo.
  //
  await expect(card(page, nome).locator(".estoque-tarja")).toBeVisible();
  await esperarHistorico(page);
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

test("validar preenche os números de mercado do item", async ({ page }) => {
  await validarItem(page, "Espada Primordial");

  await expect(tileMaisBarato(page, "Espada Primordial")).toContainText("129.999.999 z");
  await expect(card(page, "Espada Primordial").locator(".estoque-tile-oferta")).toContainText("3 anúncios");
  // Sem personagens cadastrados, nenhum anúncio é reconhecido como seu.
  await expect(tileMaisBarato(page, "Espada Primordial")).not.toContainText("você");
});

// O ponto central da etapa: sem isto, você competiria consigo mesmo.
test("um personagem seu deixa de contar como concorrência", async ({ page, request }) => {
  await validarItem(page, "Espada Primordial");
  await expect(tileMaisBarato(page, "Espada Primordial")).toContainText("129.999.999 z");

  await zerarContagemDoUpstream(request);
  await adicionarPersonagem(page, "Vendedor s-primordial-129");

  // O anúncio mais barato virou o SEU, então a concorrência agora começa no
  // segundo.
  await expect(tileMaisBarato(page, "Espada Primordial")).toContainText("158.000.000 z");
  await expect(card(page, "Espada Primordial").locator(".estoque-tile-oferta")).toContainText("2 anúncios");

  // E o recálculo é de graça: se ele custasse uma requisição por item, um
  // estoque de vinte itens levaria vinte segundos de fila.
  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

test("quando todos os anúncios são seus, você está sozinho no mercado", async ({ page }) => {
  await validarItem(page, "Espada Primordial");

  for (const nome of ["Vendedor s-primordial-129", "Vendedor s-primordial-158", "Vendedor s-primordial-299"]) {
    await adicionarPersonagem(page, nome);
  }

  await expect(tarja(page, "Espada Primordial")).toContainText("único anunciando");
});

// O aviso que interessa a quem vende. (O alerta ativo — toast, som, Telegram
// — é a etapa seguinte; aqui é só o que o card mostra.)
test("o card avisa quando alguém está vendendo mais barato que você", async ({ page }) => {
  await validarItem(page, "Espada Primordial");
  const c = card(page, "Espada Primordial");
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("299999999");
  await c.locator("input").press("Enter");
  await adicionarPersonagem(page, "Vendedor s-primordial-299");

  await expect(tarja(page, "Espada Primordial")).toContainText("Estão vendendo mais barato");
  await expect(card(page, "Espada Primordial").locator(".estoque-tarja")).toHaveClass(/estoque-tarja-perdendo/);
});

test("um item que ninguém anuncia diz isso, em vez de parecer vazio", async ({ page }) => {
  await validarItem(page, "Bota do Andarilho");

  await expect(tarja(page, "Bota do Andarilho")).toContainText("Sem anúncios no mercado");
});

test("o botão de atualizar consulta o mercado de novo", async ({ page, request }) => {
  await validarItem(page, "Espada Primordial");
  await zerarContagemDoUpstream(request);

  await card(page, "Espada Primordial").locator(".estoque-atualizar").click();

  await expect
    .poll(() => contarRequisicoesAoUpstream(request))
    .toBe(1);
  await expect(tileMaisBarato(page, "Espada Primordial")).toContainText("129.999.999 z");
});

test("os dados de mercado sobrevivem à troca de aba sem reconsultar", async ({ page, request }) => {
  await validarItem(page, "Espada Primordial");
  await zerarContagemDoUpstream(request);

  await page.getByRole("link", { name: "Watchlist" }).click();
  await expect(page.locator(".search-form")).toBeVisible();
  await page.getByRole("link", { name: "Estoque" }).click();
  await abrir(page, "Espada Primordial");

  await expect(tileMaisBarato(page, "Espada Primordial")).toContainText("129.999.999 z");
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
const vendaEstimada = (page, nome) => card(page, nome).locator(".estoque-venda-valor");
const diasDoHistorico = (page, nome) => card(page, nome).locator(".estoque-dias tbody tr");
const janela = (page, nome) => card(page, nome).locator(".estoque-janela");

test("validar traz o histórico junto, na janela padrão de 7 dias", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");

  await expect(janela(page, "Elixir do Mercador")).toHaveValue("7");

  // Abrir a aba já mostra a tabela: nada de expandir depois. A aba É a
  // divulgação, e um clique a mais só escondia o que se foi ver.
  // Os agregados do período viram tiles lado a lado; a tabela logo abaixo é o
  // dia a dia. Espremidos numa frase só eles se atropelavam.
  await abrirAba(page, "Histórico");
  const c = card(page, "Elixir do Mercador");
  await expect(c.locator(".estoque-tile-hist-min .estoque-tile-valor")).toHaveText("900 z");
  await expect(c.locator(".estoque-tile-hist-max .estoque-tile-valor")).toHaveText("1.160 z");
  await expect(c.locator(".estoque-tile-hist-qtd .estoque-tile-valor")).toHaveText("7 un.");
  await expect(diasDoHistorico(page, "Elixir do Mercador")).toHaveCount(7);
});

// O card diz quantos dias vieram E quantos existem — é isso que avisa que
// trocar para uma janela maior tem o que mostrar.
test("o card mostra quantos dias existem além da janela atual", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");

  await abrirAba(page, "Histórico");
  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-legenda")).toContainText(
    "7 dias com venda de 32 registrados",
  );
});

test("trocar a janela reconsulta só aquele item", async ({ page, request }) => {
  await validarItem(page, "Elixir do Mercador");
  await validarItem(page, "Espada Primordial");
  await abrir(page, "Elixir do Mercador");
  await zerarContagemDoUpstream(request);

  await janela(page, "Elixir do Mercador").selectOption("30");

  // Esperar o resumo antes de abrir a tabela: clicar no <summary> ALTERNA o
  // <details>, então repetir o clique enquanto se espera (dentro de um poll,
  // por exemplo) fecharia o que acabou de abrir.
  await abrirAba(page, "Histórico");
  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-legenda")).toContainText(
    "30 dias com venda",
  );
  await abrirAba(page, "Histórico");
  await expect(diasDoHistorico(page, "Elixir do Mercador")).toHaveCount(30);

  // Uma requisição, e só do item que mudou. Um seletor global cobraria isso de
  // todos os itens de uma vez.
  expect(await contarRequisicoesAoUpstream(request)).toBe(1);
});

test("a janela de um dia traz um dia só", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");

  await janela(page, "Elixir do Mercador").selectOption("1");
  await abrirAba(page, "Histórico");
  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-legenda")).toContainText(
    "1 dia com venda",
  );
});

test("todo o histórico traz os 32 dias", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");

  await janela(page, "Elixir do Mercador").selectOption("ALL");

  await abrirAba(page, "Histórico");
  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-legenda")).toContainText(
    "32 dias com venda",
  );
  await abrirAba(page, "Histórico");
  await expect(diasDoHistorico(page, "Elixir do Mercador")).toHaveCount(32);
});

test("voltar para uma janela já consultada não custa requisição", async ({ page, request }) => {
  await validarItem(page, "Elixir do Mercador");
  await janela(page, "Elixir do Mercador").selectOption("30");
  await abrirAba(page, "Histórico");
  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-legenda")).toContainText(
    "30 dias com venda",
  );

  await zerarContagemDoUpstream(request);
  await janela(page, "Elixir do Mercador").selectOption("7");
  await abrirAba(page, "Histórico");
  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-legenda")).toContainText(
    "7 dias com venda",
  );

  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

test("a janela escolhida e o histórico sobrevivem à troca de aba", async ({ page, request }) => {
  await validarItem(page, "Elixir do Mercador");
  await janela(page, "Elixir do Mercador").selectOption("30");
  await abrirAba(page, "Histórico");
  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-legenda")).toContainText(
    "30 dias com venda",
  );

  await zerarContagemDoUpstream(request);
  await page.getByRole("link", { name: "Watchlist" }).click();
  await expect(page.locator(".search-form")).toBeVisible();
  await page.getByRole("link", { name: "Estoque" }).click();
  await abrir(page, "Elixir do Mercador");

  await expect(janela(page, "Elixir do Mercador")).toHaveValue("30");
  await abrirAba(page, "Histórico");
  await expect(card(page, "Elixir do Mercador").locator(".estoque-historico-legenda")).toContainText(
    "30 dias com venda",
  );
  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

test("um item sem vendas registradas diz isso", async ({ page }) => {
  // A Carta Poring Noel está anunciada mas tem histórico vazio nas fixtures.
  await validarItem(page, "Carta Poring Noel");

  await abrirAba(page, "Histórico");
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

const sugestao = (page, nome) => card(page, nome).locator(".estoque-tile-faixa .estoque-tile-valor");
const confianca = (page, nome) => card(page, nome).locator(".estoque-tile-faixa .estoque-tile-detalhe");
const cenarios = (page, nome) => card(page, nome).locator(".estoque-cenario");

test("um item homogêneo ganha faixa, confiança alta e os três cenários", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");

  await expect(confianca(page, "Elixir do Mercador")).toHaveText("confiança alta");
  await expect(sugestao(page, "Elixir do Mercador")).toContainText("1.080 z");

  await abrirAba(page, "Estratégias");
  await abrirAba(page, "Estratégias");
  await expect(cenarios(page, "Elixir do Mercador")).toHaveCount(3);
  await abrirAba(page, "Estratégias");
  await expect(cenarios(page, "Elixir do Mercador").first()).toContainText("Vender hoje");
});

// O concorrente mais barato do Elixir é 1.200 z; dez por cento abaixo é 1.080.
test("o cenário de vender hoje fica 10% abaixo do concorrente mais barato", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");
  await abrirAba(page, "Estratégias");

  await abrirAba(page, "Estratégias");
  await expect(cenarios(page, "Elixir do Mercador").first()).toContainText("1.080 z");
});

// A aposta do "Segurar" precisa estar escrita em algum lugar: é a única
// estratégia que depende de uma previsão do usuário sobre o jogo.
test("o cenário de segurar explica a aposta que embute", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");
  await abrirAba(page, "Estratégias");

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

  // O motivo fica na aba de ressalvas: o texto é longo demais para caber ao
  // lado dos números, e a contagem na aba avisa que há o que ler.
  await abrirAba(page, "Ressalvas");
  await expect(card(page, "Espada Primordial").locator(".estoque-ressalvas")).toContainText(
    "não distingue refino nem encantamento",
  );

  // A faixa aparece; o preço recomendado, não.
  await abrirAba(page, "Estratégias");
  await expect(card(page, "Espada Primordial").locator(".estoque-cenario")).toHaveCount(0);
});

test("um item sem vendas registradas não sugere preço nenhum", async ({ page }) => {
  await validarItem(page, "Carta Poring Noel");

  await expect(sugestao(page, "Carta Poring Noel")).toHaveText("—");
  await expect(confianca(page, "Carta Poring Noel")).toHaveText("sem dados");
  await abrirAba(page, "Estratégias");
  await expect(card(page, "Carta Poring Noel").locator(".estoque-cenario")).toHaveCount(0);
});

// A colocação na fila é a resposta imediata a quem mexe no próprio preço, e é
// um FATO exato — contagem de anúncios, sem depender de liquidez nem da
// confiança do dado. Foi a falta dela que fazia o card parecer inerte num
// equipamento: os baldes grosseiros do tempo ("provavelmente dias") quase não
// se mexem, e o "Sugerido" não depende do que você está pedindo.
test("mudar o preço muda a colocação e a distância, exatamente", async ({ page, request }) => {
  await validarItem(page, "Espada Primordial");
  const c = card(page, "Espada Primordial");
  const linhaVenda = c.locator(".estoque-venda-valor");
  await zerarContagemDoUpstream(request);

  // Anúncios: 129.999.999 / 158.000.000 / 299.999.999.
  const casos = [
    ["100000000", "1º de 4", "23% abaixo do mais barato"],
    ["140000000", "2º de 4", "8% acima do mais barato"],
    ["200000000", "3º de 4", "54% acima do mais barato"],
    ["500000000", "4º de 4", "285% acima do mais barato"],
  ];
  for (const [preco, colocacao, distancia] of casos) {
    await c.locator(".estoque-preco-venda").click();
    await c.locator("input").fill(preco);
    await c.locator("input").press("Enter");
    await expect(linhaVenda).toContainText(colocacao);
    await expect(linhaVenda).toContainText(distancia);
  }

  // Estar em primeiro é o estado que quem vende persegue, e é destacado.
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("100000000");
  await c.locator("input").press("Enter");
  await expect(linhaVenda).toContainText("1º de 4");

  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

// Mesmo num item onde NENHUM preço é recomendado, a colocação aparece: ela não
// é estimativa, é contagem.
test("a colocação aparece mesmo com confiança baixa", async ({ page }) => {
  await validarItem(page, "Espada Primordial");
  const c = card(page, "Espada Primordial");
  await expect(c.locator(".estoque-cenario")).toHaveCount(0);

  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("200000000");
  await c.locator("input").press("Enter");

  await expect(c.locator(".estoque-venda-valor")).toContainText("3º de 4");
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
  await expect(c.locator(".estoque-venda-valor")).toContainText("1º de 3");

  // Acima dos dois: a fila inteira na frente.
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("2000");
  await c.locator("input").press("Enter");
  await expect(c.locator(".estoque-venda-valor")).toContainText("3º de 3");

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
  await expect(c.locator(".estoque-venda-valor")).toContainText("2º de 3");

  await zerarContagemDoUpstream(request);
  // O anúncio de 1.200 z é do "Vendedor elixir-a". Reconhecido como seu, ele
  // sai da concorrência — e você passa a ser o primeiro dos dois que restam.
  await adicionarPersonagem(page, "Vendedor elixir-a");

  await expect(card(page, "Elixir do Mercador").locator(".estoque-venda-valor")).toContainText(
    "1º de 2",
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
  await abrir(page, "Elixir do Mercador");

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
// Tabela, bolinhas e seleção
// ---------------------------------------------------------------------------
//
// A tabela existe para responder "qual item precisa de mim?" numa olhada. É a
// bolinha e a frase da direita que carregam isso — os números crus não
// carregavam.

const bolinhaDe = (page, nome) => linha(page, nome).locator(".estoque-bolinha");
const contraOMercado = (page, nome) => linha(page, nome).locator(".estoque-col-mercado");
const pilulas = (page) => page.locator(".estoque-pilula");

test("um item sem validar nasce com a bolinha oca", async ({ page }) => {
  await adicionar(page, "Elmo Ancestral");

  await expect(bolinhaDe(page, "Elmo Ancestral")).toHaveClass(/estoque-bolinha-sem-dados/);
  await expect(contraOMercado(page, "Elmo Ancestral")).toHaveText("sem validar");
});

// Seu preço acima do mais barato por mais que a margem de empate.
test("perder a venda pinta a bolinha de vermelho e diz de quem", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");
  const c = card(page, "Elixir do Mercador");
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("2000");
  await c.locator("input").press("Enter");

  await expect(bolinhaDe(page, "Elixir do Mercador")).toHaveClass(/estoque-bolinha-perdendo/);
  // A frase diz o que fazer; "menor preço: 1.200 z" não diria.
  await expect(contraOMercado(page, "Elixir do Mercador")).toHaveText("2 anúncios a 1.200 z");
});

// Estar 2% acima do mais barato é a mesma disputa; estar 60% acima não é.
test("uma diferença pequena conta como empate, não como derrota", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");
  const c = card(page, "Elixir do Mercador");
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("1224");
  await c.locator("input").press("Enter");

  await expect(bolinhaDe(page, "Elixir do Mercador")).toHaveClass(/estoque-bolinha-empatado/);
  await expect(contraOMercado(page, "Elixir do Mercador")).toContainText("2% acima do mais barato");
});

test("ser o mais barato pinta de verde e diz a folga", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");
  const c = card(page, "Elixir do Mercador");
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("1000");
  await c.locator("input").press("Enter");

  await expect(bolinhaDe(page, "Elixir do Mercador")).toHaveClass(/estoque-bolinha-na-frente/);
  await expect(contraOMercado(page, "Elixir do Mercador")).toContainText("mais barato");
});

// Sem anúncio nenhum não há posição a ocupar: é ausência de mercado, não um
// estado dele — e ausência é bolinha oca.
test("um item que ninguém anuncia fica cinza, não verde", async ({ page }) => {
  await validarItem(page, "Bota do Andarilho");

  await expect(bolinhaDe(page, "Bota do Andarilho")).toHaveClass(/estoque-bolinha-sem-dados/);
  await expect(contraOMercado(page, "Bota do Andarilho")).toHaveText("sem anúncios");
});

// Verde por falta de concorrência é outra coisa: HÁ anúncio, e ele é seu.
test("ser o único anunciante fica verde", async ({ page }) => {
  await adicionarPersonagem(page, "Vendedor p-carta-noel");
  await validarItem(page, "Carta Poring Noel");

  await expect(bolinhaDe(page, "Carta Poring Noel")).toHaveClass(/estoque-bolinha-na-frente/);
  await expect(contraOMercado(page, "Carta Poring Noel")).toHaveText("único anúncio no mercado");
});

// Validado mas sem preço definido não dá para comparar com nada — e a frase
// diz o que falta.
test("um item validado sem preço pede o preço", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");

  await expect(bolinhaDe(page, "Elixir do Mercador")).toHaveClass(/estoque-bolinha-sem-dados/);
  await expect(contraOMercado(page, "Elixir do Mercador")).toHaveText("defina o seu preço");
});

test("as pílulas contam os grupos e levam ao primeiro item de cada um", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");
  const c = card(page, "Elixir do Mercador");
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("2000");
  await c.locator("input").press("Enter");
  await adicionar(page, "Elmo Ancestral");

  await expect(pilulas(page).filter({ hasText: "1 perdendo" })).toBeVisible();
  await expect(pilulas(page).filter({ hasText: "1 sem dados" })).toBeVisible();

  // Clicar leva ao primeiro item do grupo — o gesto de quem viu "1 perdendo".
  await pilulas(page).filter({ hasText: "1 perdendo" }).click();
  await expect(linha(page, "Elixir do Mercador")).toHaveClass(/is-selecionada/);
});

test("só um item fica aberto por vez", async ({ page }) => {
  await adicionar(page, "Elmo Ancestral");
  await adicionar(page, "Capa do Corvo");

  await abrir(page, "Elmo Ancestral");
  await expect(linha(page, "Elmo Ancestral")).toHaveClass(/is-selecionada/);
  await expect(linha(page, "Capa do Corvo")).not.toHaveClass(/is-selecionada/);
  await expect(page.locator("#estoque-detalhe .estoque-card")).toHaveCount(1);
});

// O usuário trabalha um item de cada vez; perder a seleção a cada ida à
// Watchlist seria hostil.
test("a seleção sobrevive à troca de aba e ao recarregar", async ({ page }) => {
  await adicionar(page, "Elmo Ancestral");
  await adicionar(page, "Capa do Corvo");
  await abrir(page, "Elmo Ancestral");

  await page.getByRole("link", { name: "Watchlist" }).click();
  await expect(page.locator(".search-form")).toBeVisible();
  await page.getByRole("link", { name: "Estoque" }).click();
  await expect(linha(page, "Elmo Ancestral")).toHaveClass(/is-selecionada/);

  await page.reload();
  await expect(linha(page, "Elmo Ancestral")).toHaveClass(/is-selecionada/);
});

test("sem seleção, o painel convida a escolher um item", async ({ page }) => {
  await expect(page.locator(".estoque-detalhe-vazio")).toContainText("Cadastre um item");
});

// Validar tudo é o botão mais caro do programa: sete itens são até quatorze
// idas ao site, a uma por segundo. O custo é dito antes, e nada sai sem
// confirmação.
test("validar tudo avisa o custo antes e respeita a recusa", async ({ page, request }) => {
  await adicionar(page, "Elixir do Mercador");
  await adicionar(page, "Bota do Andarilho");
  await zerarContagemDoUpstream(request);

  let texto = "";
  page.once("dialog", (d) => {
    texto = d.message();
    d.dismiss();
  });
  await page.locator("#estoque-validar-tudo").click();

  expect(texto).toContain("2 itens");
  expect(texto).toContain("consultar o site");
  expect(await contarRequisicoesAoUpstream(request)).toBe(0);
});

test("validar tudo valida os pendentes em série", async ({ page }) => {
  await adicionar(page, "Elixir do Mercador");
  await adicionar(page, "Bota do Andarilho");

  page.once("dialog", (d) => d.accept());
  await page.locator("#estoque-validar-tudo").click();

  // Sem preço definido eles continuam com a bolinha oca — o que muda é a
  // frase, que deixa de ser "sem validar".
  await expect(contraOMercado(page, "Elixir do Mercador")).toHaveText("defina o seu preço", { timeout: 20000 });
  await expect(contraOMercado(page, "Bota do Andarilho")).toHaveText("sem anúncios", { timeout: 20000 });
});

// ---------------------------------------------------------------------------
// Histograma
// ---------------------------------------------------------------------------

const barras = (page, nome) => card(page, nome).locator(".estoque-barra");

test("o histograma mostra a distribuição e marca onde você está", async ({ page }) => {
  await validarItem(page, "Espada Primordial");
  const c = card(page, "Espada Primordial");
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("140000000");
  await c.locator("input").press("Enter");

  await expect(barras(page, "Espada Primordial")).toHaveCount(8);
  await expect(c.locator(".estoque-barra.is-seu")).toHaveCount(1);
  await expect(c.locator(".estoque-barra-voce")).toHaveText("você");
});

// A barra do seu preço acompanha a cor do status: é a mesma informação da
// bolinha da tabela, no lugar onde se está olhando.
test("a barra do seu preço acompanha a cor do status", async ({ page }) => {
  await validarItem(page, "Espada Primordial");
  const c = card(page, "Espada Primordial");
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("200000000");
  await c.locator("input").press("Enter");

  await expect(c.locator(".estoque-barra.is-seu")).toHaveClass(/estoque-barra-perdendo/);
});

test("sem concorrência suficiente o histograma não aparece", async ({ page }) => {
  // A Carta Poring Noel tem um anúncio só: não há distribuição a mostrar.
  await validarItem(page, "Carta Poring Noel");

  await expect(card(page, "Carta Poring Noel").locator(".estoque-histograma")).toHaveCount(0);
});

// ---------------------------------------------------------------------------
// Painel: tarja, ação e abas
// ---------------------------------------------------------------------------

const tarjaDe = (page, nome) => card(page, nome).locator(".estoque-tarja");
const acao = (page, nome) => card(page, nome).locator(".estoque-acao");

test("a tarja diz a situação e oferece a ação que cabe", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");
  const c = card(page, "Elixir do Mercador");
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("2000");
  await c.locator("input").press("Enter");

  await expect(tarjaDe(page, "Elixir do Mercador")).toHaveClass(/estoque-tarja-perdendo/);
  await expect(tarja(page, "Elixir do Mercador")).toContainText("Estão vendendo mais barato");
  // Um zeny abaixo do concorrente mais barato (1.200 z).
  await expect(acao(page, "Elixir do Mercador")).toHaveText("Reprecificar 1.199 z");
});

// Sendo o mais barato não há o que fazer — e um botão que não faz nada seria
// pior que botão nenhum.
test("sendo o mais barato, a tarja não oferece ação", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");
  const c = card(page, "Elixir do Mercador");
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("1000");
  await c.locator("input").press("Enter");

  await expect(tarjaDe(page, "Elixir do Mercador")).toHaveClass(/estoque-tarja-na-frente/);
  await expect(tarja(page, "Elixir do Mercador")).toContainText("Nada a fazer agora");
  await expect(acao(page, "Elixir do Mercador")).toHaveCount(0);
});

// O programa não consegue mexer na sua loja dentro do jogo: quem aplica o
// preço é você. Por isso o clique grava E copia, e o toast diz o que falta.
test("reprecificar grava o preço, copia o valor e avisa o que falta", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await validarItem(page, "Elixir do Mercador");
  const c = card(page, "Elixir do Mercador");
  await c.locator(".estoque-preco-venda").click();
  await c.locator("input").fill("2000");
  await c.locator("input").press("Enter");

  await acao(page, "Elixir do Mercador").click();

  await expect(c.locator(".estoque-preco-venda")).toHaveText("Vendo por: 1.199 z");
  await expect(page.locator(".toast")).toContainText("Aplique o preço na sua loja");
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("1199");

  // E o item passa a ser o mais barato.
  await expect(bolinhaDe(page, "Elixir do Mercador")).toHaveClass(/estoque-bolinha-na-frente/);
});

// Sem collapse em lugar nenhum das abas: a aba já é a divulgação, e um clique
// a mais só esconde o que se foi ver.
test("nenhuma aba esconde o conteúdo atrás de um segundo clique", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");

  await abrirAba(page, "Histórico");
  await expect(diasDoHistorico(page, "Elixir do Mercador")).toHaveCount(7);

  await abrirAba(page, "Estratégias");
  await expect(cenarios(page, "Elixir do Mercador")).toHaveCount(3);
  await expect(card(page, "Elixir do Mercador").locator(".estoque-cenario-aposta").first()).toBeVisible();

  // E nenhum <details> sobrou no painel.
  await expect(card(page, "Elixir do Mercador").locator("details")).toHaveCount(0);
});

test("as abas trocam o conteúdo e a escolha persiste", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");

  await expect(card(page, "Elixir do Mercador").locator(".estoque-tile-mais-barato")).toBeVisible();
  await abrirAba(page, "Histórico");
  // As duas abas usam tiles; o que distingue é QUAIS.
  await expect(card(page, "Elixir do Mercador").locator(".estoque-tile-hist-min")).toBeVisible();
  await expect(card(page, "Elixir do Mercador").locator(".estoque-tile-mais-barato")).toHaveCount(0);

  // Quem está comparando histórico entre itens não quer voltar para "Mercado"
  // a cada troca de seleção.
  await validarItem(page, "Espada Primordial");
  await expect(
    page.locator(".estoque-aba").filter({ hasText: "Histórico" }),
  ).toHaveClass(/is-ativa/);
});

// A contagem na aba avisa que há o que ler ali sem ocupar espaço na tela.
test("as abas contam estratégias e ressalvas", async ({ page }) => {
  await validarItem(page, "Elixir do Mercador");
  // Item homogêneo: três estratégias e nenhuma ressalva a fazer.
  await expect(page.locator(".estoque-aba").filter({ hasText: "Estratégias" })).toContainText("3");
  await expect(page.locator(".estoque-aba-conta")).toHaveCount(1);

  // Um equipamento disperso é o oposto: nenhuma estratégia recomendada e
  // várias ressalvas.
  await validarItem(page, "Espada Primordial");
  const contaRessalvas = page.locator(".estoque-aba").filter({ hasText: "Ressalvas" }).locator(".estoque-aba-conta");
  await expect(contaRessalvas).toBeVisible();
  await expect(page.locator(".estoque-aba").filter({ hasText: "Estratégias" })).toHaveText("Estratégias");
});

test("um equipamento disperso ganha a ressalva sobre refino e encantamento", async ({ page }) => {
  await validarItem(page, "Espada Primordial");
  await abrirAba(page, "Ressalvas");

  const ressalvas = card(page, "Espada Primordial").locator(".estoque-ressalva");
  await expect(ressalvas.filter({ hasText: "Sobre a confiança" })).toContainText(
    "não distingue refino nem encantamento",
  );
  await expect(ressalvas.filter({ hasText: "velocidade de venda" })).toContainText("aproximação");
});

test("um item sem validar mostra a tarja pedindo validação", async ({ page }) => {
  await adicionar(page, "Elmo Ancestral");

  // Sem validar não há tarja nem abas: não há o que comparar nem medir.
  await expect(card(page, "Elmo Ancestral").locator(".estoque-tarja")).toHaveCount(0);
  await expect(card(page, "Elmo Ancestral").locator(".estoque-abas")).toHaveCount(0);
  await expect(card(page, "Elmo Ancestral").locator(".estoque-validar")).toBeVisible();
});
