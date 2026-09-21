// Estoque da loja: a lista do que o usuário está vendendo, por quanto, e
// (nas etapas seguintes) o que o mercado está pagando por isso.
//
// Mesmo desenho da watchlist e pelos mesmos motivos: a lista vive inteira no
// localStorage do navegador — não há conta de usuário nem persistência no
// servidor —, a ordem do array É a ordem de exibição, e nenhuma ação de
// edição (preço, ligar/desligar, remover) fala com o servidor. Ver
// static/watchlist.js.
//
// Falam com o servidor: os cliques do usuário (Validar, ↻, trocar a janela do
// histórico) e o rodízio compartilhado (ver monitor.js), que reconsulta o
// mercado dos itens NA LOJA para avisar quando alguém corta o seu preço.

const ESTOQUE_KEY = "ro-market-tracker:estoque";
const ESTOQUE_SERVIDOR_KEY = "ro-market-tracker:estoque-servidor";

// Os personagens do usuário. Lista separada, e não um campo por item: ela é
// do usuário, não de um item, e vale para o estoque inteiro. É por ela que o
// programa sabe quais anúncios do mercado são do próprio usuário — sem isso,
// você competiria consigo mesmo.
const PERSONAGENS_KEY = "ro-market-tracker:meus-personagens";

const ESTOQUE_SERVIDOR_PADRAO = "NIDHOGG";

// Qual item está aberto no painel de detalhe. Persistido para a seleção
// sobreviver à troca de aba e ao recarregar — o usuário costuma trabalhar um
// item de cada vez, e perder a seleção a cada ida à Watchlist seria hostil.
const ESTOQUE_SELECAO_KEY = "ro-market-tracker:estoque-selecionado";

// Itens cuja validação está em voo. Em memória, não persistido: uma fila que
// sobrevivesse ao recarregar descreveria requisições que já não existem.
const filaDeValidacao = new Set();

// Estados da validação. O item nasce NAO_VALIDADO; "Validar" o leva a
// VALIDADO (achado no mercado ou no histórico) ou a INVALIDO — e este último
// só é alcançado quando as DUAS consultas responderam que o item não existe.
// Uma falha de rede deixa o item onde estava: inválido é o estado que manda
// o usuário apagar o cadastro, e um tropeço do site não pode mandar isso.
const VALIDACAO_PENDENTE = "nao-validado";
const VALIDACAO_OK = "validado";
const VALIDACAO_INVALIDO = "invalido";

const ROTULO_VALIDACAO = {
  [VALIDACAO_PENDENTE]: "Não validado",
  [VALIDACAO_OK]: "Validado",
  [VALIDACAO_INVALIDO]: "Inválido",
};

// Janela do histórico de preços praticados. Os valores são os que o próprio
// site aceita (ver MarketPricePeriod* em internal/gnjoy/client.go); o rótulo
// é o que aparece no seletor do card.
const JANELAS = [
  { valor: "1", rotulo: "Último dia" },
  { valor: "7", rotulo: "7 dias" },
  { valor: "30", rotulo: "30 dias" },
  { valor: "ALL", rotulo: "Todo o histórico" },
];
const JANELA_PADRAO = "7";

// O estoque entra no rodízio compartilhado como uma fonte, com os itens que
// estão NA LOJA. É deles que importa saber se alguém cortou o preço, e é por
// eles que a tela percebe a loja offline (ver avaliarPresencaNaLoja). Um item
// fora da loja fica no último dado conhecido, e atualizá-lo é o ↻ do card.
//
// Registro no topo do arquivo pelo mesmo motivo da watchlist: o monitor monta
// a lista de fontes no DOMContentLoaded.
registrarFonte({
  nome: "estoque",
  listar: () => loadEstoque().filter(estaNoRodizio),
  consultar: (entrada, fresh) => consultarMercado(entrada.id, fresh, { deFundo: true }),
});

// Só os validados entram. Sem itemId não há o que perguntar ao site, e o
// monitor escolhe sempre a entrada mais antiga: uma que nunca consegue
// consultar continuaria a mais antiga para sempre e travaria a fila.
function estaNoRodizio(item) {
  return Boolean(item.naLoja) && item.validacao === VALIDACAO_OK && item.itemId != null;
}

// ---------------------------------------------------------------------------
// Estado
// ---------------------------------------------------------------------------

function loadEstoque() {
  try {
    const raw = localStorage.getItem(ESTOQUE_KEY);
    return raw ? JSON.parse(raw) : [];
  } catch {
    return [];
  }
}

function saveEstoque(list) {
  try {
    localStorage.setItem(ESTOQUE_KEY, JSON.stringify(list));
  } catch {
    // localStorage indisponível (modo privado, cota cheia): a sessão continua
    // funcionando com o que está na tela; só não sobrevive ao recarregar.
  }
}

// updateEstoqueItem faz um merge raso na entrada e persiste. Devolve a
// entrada atualizada, ou null se ela já não existir mais (remover e editar
// podem se cruzar).
function updateEstoqueItem(id, changes) {
  const list = loadEstoque();
  const idx = list.findIndex((e) => e.id === id);
  if (idx === -1) return null;
  list[idx] = { ...list[idx], ...changes };
  saveEstoque(list);
  return list[idx];
}

// estoqueId é opaco e sorteado na criação, e NÃO derivado do nome ou do
// itemId — ao contrário do watchlistId, que é derivado de propósito para
// deduplicar. Aqui o item nasce sem itemId (só a validação descobre qual é),
// então um id derivado teria que mudar no meio do caminho e levaria junto o
// data-id do card já montado na tela.
function estoqueId() {
  return "e" + Date.now().toString(36) + Math.random().toString(36).slice(2, 8);
}

function servidorDoEstoque() {
  try {
    const salvo = localStorage.getItem(ESTOQUE_SERVIDOR_KEY);
    if (salvo) return salvo;
  } catch {
    // Sem preferência salva: o padrão vale para esta sessão.
  }
  return ESTOQUE_SERVIDOR_PADRAO;
}

function gravarServidorDoEstoque(server) {
  try {
    localStorage.setItem(ESTOQUE_SERVIDOR_KEY, server);
  } catch {
    // Ver saveEstoque.
  }
}

function carregarPersonagens() {
  try {
    const raw = localStorage.getItem(PERSONAGENS_KEY);
    const lista = raw ? JSON.parse(raw) : [];
    if (!Array.isArray(lista)) return [];
    return lista.map((n) => String(n).trim()).filter((n) => n !== "");
  } catch {
    return [];
  }
}

function salvarPersonagens(lista) {
  try {
    localStorage.setItem(PERSONAGENS_KEY, JSON.stringify(lista));
  } catch {
    // Ver saveEstoque.
  }
}

// ehMeuAnuncio compara o vendedor do anúncio com a lista de personagens.
// Insensível a caixa e com as pontas aparadas: quem digita o nome do próprio
// personagem não deveria precisar acertar a capitalização exata do jogo.
function ehMeuAnuncio(anuncio, personagens) {
  const vendedor = String(anuncio.seller || "").trim().toLowerCase();
  if (vendedor === "") return false;
  return personagens.some((n) => n.toLowerCase() === vendedor);
}

// separarAnuncios divide o que veio do servidor entre os seus anúncios e os
// da concorrência.
//
// Esta separação acontece AQUI, no navegador, e não no servidor, por um
// motivo de custo: se o servidor fizesse o desconto, editar a lista de
// personagens obrigaria cada card a reconsultar o site — vinte itens no
// estoque seriam vinte requisições e vinte segundos de fila. Do jeito atual,
// mexer na lista recalcula a tela inteira de graça.
function separarAnuncios(resultado) {
  const anuncios = (resultado && resultado.listings) || [];
  const personagens = carregarPersonagens();
  const meus = [];
  const outros = [];
  for (const anuncio of anuncios) {
    (ehMeuAnuncio(anuncio, personagens) ? meus : outros).push(anuncio);
  }
  // O servidor já manda ordenado por preço crescente, então o primeiro de
  // cada lado é o mais barato.
  return { meus, outros };
}

function somarUnidades(anuncios) {
  return anuncios.reduce((total, a) => total + (a.units || 0), 0);
}

// nomeVisivel é o que o card mostra: o nome canônico depois de validado, e o
// que o usuário digitou até lá. Preservar o digitado importa — é por ele que
// o usuário reconhece a linha que precisa corrigir quando a validação falha.
function nomeVisivel(item) {
  return item.itemName || item.nomeDigitado || "";
}

function janelaDoItem(item) {
  return item.janela || JANELA_PADRAO;
}

// quandoDoMercado diz de quando é o lastResult, o dado que a tela mostra.
//
// Não é o lastCheckedAt. Esse marca a vez no rodízio e avança também quando a
// consulta falha, senão um item com erro seria escolhido a todo tick. Usá-lo
// como idade do dado faria uma falha parecer um dado novo, e o aviso de loja
// offline tomaria como fresca uma evidência de meia hora atrás. Itens gravados
// antes do mercadoEm só tinham o lastCheckedAt, e nele só se gravava sucesso.
function quandoDoMercado(item) {
  if (!item.lastResult) return null;
  return item.mercadoEm != null ? item.mercadoEm : item.lastCheckedAt;
}

// ---------------------------------------------------------------------------
// Ações
// ---------------------------------------------------------------------------

// adicionarAoEstoque cria a entrada a partir do nome digitado. Nenhuma
// requisição sai daqui: o item entra como não-validado e só fala com o site
// quando o usuário pedir, no botão "Validar".
function adicionarAoEstoque(nomeDigitado) {
  const nome = nomeDigitado.trim();
  if (nome === "") return null;

  const item = {
    id: estoqueId(),
    server: servidorDoEstoque(),
    nomeDigitado: nome,
    itemName: null,
    searchName: null,
    itemId: null,
    svrId: null,
    validacao: VALIDACAO_PENDENTE,
    databaseType: null,
    // motivo só é preenchido no estado inválido; candidatos, só enquanto a
    // escolha estiver pendente. Os dois voltam a null assim que a validação
    // se resolve, para o card não carregar sobra de uma tentativa anterior.
    motivo: null,
    candidatos: null,
    precoVenda: null,
    naLoja: false,
    undercut: false,
    janela: JANELA_PADRAO,
    lastCheckedAt: null,
    lastResult: null,
    mercadoEm: null,
    historico: null,
    notified: false,
  };

  const list = loadEstoque();
  list.push(item);
  saveEstoque(list);

  const container = document.getElementById("estoque-list");
  if (container) container.appendChild(buildLinhaDoEstoque(item));
  atualizarEstoqueVazio();
  renderResumoDoTopo();
  // Abre o item recém-cadastrado: o passo seguinte é sempre validá-lo, e o
  // botão para isso está no painel.
  selecionarItem(item.id);
  return item;
}

function removerDoEstoque(id) {
  saveEstoque(loadEstoque().filter((e) => e.id !== id));
  if (itemSelecionado() === id) gravarSelecao(null);
  const linha = findEstoqueLinha(id);
  if (linha) linha.remove();
  atualizarEstoqueVazio();
  renderResumoDoTopo();
  renderDetalhe();
  renderAvisoDeLoja();
}

// findEstoqueCard acha o card do item no painel de detalhe — que só existe
// quando ele é o item selecionado. Todo chamador já trata null, e isso é o que
// permite uma consulta terminar com o painel mostrando outro item sem nada
// quebrar: o dado é gravado do mesmo jeito, só não há o que pintar.
function findEstoqueCard(id) {
  return document.querySelector('.estoque-card[data-id="' + cssEscape(id) + '"]');
}

function findEstoqueLinha(id) {
  return document.querySelector('.estoque-linha[data-id="' + cssEscape(id) + '"]');
}

function itemSelecionado() {
  try {
    return localStorage.getItem(ESTOQUE_SELECAO_KEY);
  } catch {
    return null;
  }
}

function gravarSelecao(id) {
  try {
    if (id == null) localStorage.removeItem(ESTOQUE_SELECAO_KEY);
    else localStorage.setItem(ESTOQUE_SELECAO_KEY, id);
  } catch {
    // Ver saveEstoque.
  }
}

// selecionarItem(null) volta ao resumo do estoque.
function selecionarItem(id) {
  // Abrir um item encerra a lista de reprecificação em lote: ela é uma sessão
  // de trabalho sobre o resumo, e voltar a ela depois de mexer num item
  // mostraria uma conferência feita sobre dados que o usuário acabou de mudar.
  if (id != null) loteDeReprecificacao = null;
  gravarSelecao(id);
  for (const linha of document.querySelectorAll(".estoque-linha")) {
    linha.classList.toggle("is-selecionada", linha.dataset.id === id);
    linha.setAttribute("aria-selected", String(linha.dataset.id === id));
  }
  renderDetalhe();
}

function atualizarEstoqueVazio() {
  const vazio = document.getElementById("estoque-empty");
  if (vazio) vazio.hidden = loadEstoque().length > 0;
}

function precoVendaLabel(precoVenda) {
  return "Vendo por: " + (precoVenda != null ? formatMoney(precoVenda) : "—");
}

// startEditingPrecoVenda troca o texto "Vendo por: ..." por um <input>
// numérico. Enter confirma e persiste; Escape ou perder o foco sem confirmar
// descarta. É a mesma receita do "Alvo:" da watchlist (startEditingTarget) —
// clicar no valor é o gesto que o usuário já conhece das outras telas.
function startEditingPrecoVenda(span, id) {
  if (span.querySelector("input")) return;
  const item = loadEstoque().find((e) => e.id === id);
  if (!item) return;

  const input = document.createElement("input");
  input.type = "number";
  input.min = "0";
  input.step = "1";
  input.className = "estoque-preco-input";
  input.value = item.precoVenda != null ? String(item.precoVenda) : "";
  input.setAttribute("aria-label", "Preço de venda de " + nomeVisivel(item));

  span.textContent = "";
  span.appendChild(input);
  input.focus();
  input.select();

  let confirmado = false;

  const confirmar = () => {
    confirmado = true;
    const cru = input.value.trim();
    let precoVenda = null;
    if (cru !== "") {
      const parsed = Math.round(Number(cru));
      if (Number.isFinite(parsed) && parsed >= 0) precoVenda = parsed;
    }
    // O preço que se compara com o mercado mudou, então o aviso de
    // undercutting se rearma, a partir do que o card mostra agora (ver
    // comAvisoArmado).
    const atual = loadEstoque().find((e) => e.id === id) || item;
    const atualizado = updateEstoqueItem(id, comAvisoArmado(atual, { precoVenda })) || item;
    span.textContent = precoVendaLabel(atualizado.precoVenda);
    // Repintar o card inteiro, e não só este texto: o preço é insumo da fila,
    // do tempo até vender e dos cenários (ver calcularSugestao). Sem isto, o
    // card continuaria mostrando a conta do preço anterior.
    repintarCard(atualizado);
  };

  // Desistir da edição devolve o texto e, se o rodízio trouxe dado novo
  // enquanto o campo estava aberto, faz a repintura que ficou esperando (ver
  // repintarCard).
  const desistir = () => {
    span.textContent = precoVendaLabel(item.precoVenda);
    const card = span.closest(".estoque-card");
    if (card && card.dataset.repintarDepois) {
      repintarCard(loadEstoque().find((e) => e.id === id));
    }
  };

  input.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter") {
      confirmar();
    } else if (ev.key === "Escape") {
      confirmado = true;
      desistir();
    }
  });

  input.addEventListener("blur", () => {
    if (!confirmado) desistir();
  });
}

// ---------------------------------------------------------------------------
// Validação
// ---------------------------------------------------------------------------

// validarItem pergunta ao servidor quais itens do servidor casam com o nome
// digitado. O servidor procura primeiro nos anúncios de agora e, só se
// ninguém estiver vendendo, no histórico de vendas — ver EstoqueValidar em
// internal/web/estoque.go.
//
// Três desfechos, e o terceiro é o que exige cuidado:
//
//   1 candidato   -> valida na hora, fixando o itemId
//   N candidatos  -> o card vira uma lista de escolha; quem decide é o usuário
//   0 candidatos  -> o item é dado como INVÁLIDO
//
// Qualquer falha de rede ou do site NÃO cai em nenhum dos três: o item fica
// exatamente como estava, e o usuário vê um toast. Inválido é o estado que
// manda apagar o cadastro, e um timeout não pode mandar isso.
async function validarItem(id) {
  const item = loadEstoque().find((e) => e.id === id);
  if (!item) return;

  const card = findEstoqueCard(id);
  const botao = card ? card.querySelector(".estoque-validar") : null;
  if (botao) {
    botao.disabled = true;
    botao.textContent = "Validando…";
  }

  try {
    const url =
      "/web/estoque/validar?server=" + encodeURIComponent(item.server) +
      "&item=" + encodeURIComponent(item.nomeDigitado);
    const res = await fetch(url);
    if (!res.ok) throw new Error(await res.text());

    const data = await res.json();
    const candidatos = data.candidates || [];

    if (candidatos.length === 1) {
      escolherCandidato(id, candidatos[0]);
      return;
    }
    if (candidatos.length === 0) {
      repintarCard(updateEstoqueItem(id, {
        validacao: VALIDACAO_INVALIDO,
        motivo: data.message || "Este item não foi encontrado no servidor.",
        candidatos: null,
      }));
      return;
    }
    // Os candidatos são persistidos, e não guardados só em memória: trocar de
    // aba no meio da escolha e voltar não pode custar outra consulta ao site.
    repintarCard(updateEstoqueItem(id, { candidatos, motivo: null }));
  } catch (err) {
    showToast(String(err.message || err).trim() || "Não foi possível validar agora.");
    repintarCard(loadEstoque().find((e) => e.id === id));
  }
}

// escolherCandidato fixa qual item do catálogo é este — o itemId e o svrId
// que todas as consultas seguintes vão usar. Não custa requisição nenhuma: os
// dados já vieram na validação.
function escolherCandidato(id, candidato) {
  repintarCard(updateEstoqueItem(id, {
    validacao: VALIDACAO_OK,
    itemId: candidato.itemId,
    svrId: candidato.svrId,
    itemName: candidato.itemName,
    searchName: candidato.searchName,
    databaseType: candidato.databaseType,
    candidatos: null,
    motivo: null,
  }));
  // A primeira consulta de mercado sai junto: acabou de se descobrir QUAL é o
  // item, e mostrar um card validado e vazio faria o usuário clicar em "↻"
  // para completar um passo que ele já pediu.
  //
  // Exceto quando o candidato veio do HISTÓRICO: nesse caso a validação já
  // provou que ninguém está anunciando o item (foi por isso que ela caiu no
  // histórico), e perguntar de novo gastaria uma requisição para receber a
  // resposta que acabamos de obter. O resultado é semeado à mão.
  if (candidato.inMarket === false) {
    const agora = Date.now();
    repintarCard(updateEstoqueItem(id, {
      lastResult: { found: false, listings: [] },
      lastCheckedAt: agora,
      mercadoEm: agora,
    }));
  } else {
    consultarMercado(id);
  }
  // O histórico é a outra metade do card: o mercado diz com quem você compete
  // hoje, o histórico diz se o preço de hoje está alto ou baixo para o padrão
  // do item.
  consultarHistorico(id);
}

// repintarCard troca o card inteiro pelo estado novo. Reconstruir é mais
// simples (e menos sujeito a esquecer um pedaço) do que remendar campo a
// campo, e é barato: o card não guarda estado nenhum fora do localStorage.
// repintarCard atualiza tudo que depende de um item: a linha na tabela, o
// card no painel (quando ele é o selecionado) e o resumo do topo.
//
// O resumo e o aviso de loja são sobre o CONJUNTO, não sobre um item: quem
// acabou de mudar pode ter sido o último que ainda tinha anúncio seu, ou o
// que acabou de sair do grupo "perdendo".
function repintarCard(item) {
  if (!item) return;

  const linha = findEstoqueLinha(item.id);
  if (linha) linha.replaceWith(buildLinhaDoEstoque(item));

  // Um preço sendo digitado não pode sumir no meio da digitação. O rodízio
  // repinta o card sozinho a cada consulta, e trocá-lo agora levaria o campo
  // junto. A repintura fica para quando a edição terminar (ver
  // startEditingPrecoVenda), e a linha da tabela já sai atualizada.
  const card = findEstoqueCard(item.id);
  if (card && card.querySelector(".estoque-preco-input")) {
    card.dataset.repintarDepois = "1";
  } else if (card) {
    card.replaceWith(buildEstoqueCard(item));
    travarSeSuspenso();
  }

  // Sem item aberto, o painel é o resumo do estoque, e a mudança deste item
  // pode ter trocado ele de grupo.
  if (!itemSelecionado()) renderDetalhe();

  renderResumoDoTopo();
  renderAvisoDeLoja();
}

// resumoDoCandidato é a linha que ajuda o usuário a reconhecer o item dele.
// Vinda do mercado ela mostra o que está à venda agora; vinda do histórico,
// a faixa de preço praticada — que é o único jeito de separar dois candidatos
// quando o histórico não traz o sufixo de slots e os nomes vêm iguais.
function resumoDoCandidato(candidato) {
  if (candidato.inMarket) {
    const unidades = candidato.units === 1 ? "1 à venda" : candidato.units + " à venda";
    return "#" + candidato.itemId + " · a partir de " + formatMoney(candidato.minPrice) + " · " + unidades;
  }
  const vendas = candidato.vol === 1 ? "1 venda" : candidato.vol + " vendas";
  return (
    "#" + candidato.itemId + " · sem anúncio · já vendido entre " +
    formatMoney(candidato.min) + " e " + formatMoney(candidato.max) + " (" + vendas + ")"
  );
}

// buildCheckIcon é o "ok" da escolha de candidato. Ícone, e não a palavra:
// ele fica colado no seletor numa linha só, e qualquer texto ali espremeria o
// dropdown, que é justamente quem precisa da largura.
function buildCheckIcon() {
  const NS = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("stroke-width", "2.6");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  const path = document.createElementNS(NS, "path");
  path.setAttribute("d", "M4 12.5 9.5 18 20 6.5");
  svg.appendChild(path);
  return svg;
}

// buildEscolhaDeCandidato desenha a desambiguação como um <select> mais um
// botão de confirmar, e não como uma lista de botões.
//
// A lista crescia o card na vertical proporcionalmente ao número de
// candidatos, e como os cards dividem uma grade, um item ambíguo deixava a
// linha inteira desalinhada. O dropdown ocupa altura fixa: dois candidatos ou
// dez, o card tem o mesmo tamanho.
//
// Todo o resumo do candidato vira o texto da opção — dentro de um <select>
// não há como formatar. É por isso que resumoDoCandidato existe: sem o preço
// e o itemId ali, dois candidatos vindos do histórico (que não traz o sufixo
// de slots) apareceriam com o mesmo texto e seriam indistinguíveis.
function buildEscolhaDeCandidato(item) {
  const bloco = document.createElement("div");
  bloco.className = "estoque-escolha";

  const titulo = document.createElement("p");
  titulo.className = "estoque-candidatos-titulo";
  titulo.textContent = item.candidatos.length + " itens casam «" + item.nomeDigitado + "». Qual é o seu?";
  bloco.appendChild(titulo);

  const linha = document.createElement("div");
  linha.className = "estoque-escolha-linha";

  const seletor = document.createElement("select");
  seletor.className = "estoque-candidatos-select";
  seletor.setAttribute("aria-label", "Escolha qual item é «" + item.nomeDigitado + "»");
  for (const candidato of item.candidatos) {
    const opcao = document.createElement("option");
    opcao.value = String(candidato.itemId);
    opcao.textContent = candidato.itemName + " · " + resumoDoCandidato(candidato);
    seletor.appendChild(opcao);
  }
  linha.appendChild(seletor);

  const ok = document.createElement("button");
  ok.type = "button";
  ok.className = "estoque-escolha-ok";
  ok.title = "Confirmar o item escolhido";
  ok.setAttribute("aria-label", "Confirmar o item escolhido para " + item.nomeDigitado);
  ok.appendChild(buildCheckIcon());
  linha.appendChild(ok);

  bloco.appendChild(linha);
  return bloco;
}

// ---------------------------------------------------------------------------
// Mercado
// ---------------------------------------------------------------------------

// consultarMercado busca os anúncios do item agora. Sai ao escolher o item na
// validação, no botão "↻" do card e, para os itens na loja, pelo rodízio
// (deFundo). De fundo, um erro não vira toast: ninguém pediu aquela consulta,
// e a suspensão por limite do site já tem o seu próprio aviso.
//
// Grava o resultado SEMPRE, inclusive quando o card não está na tela — pelo
// mesmo motivo que fetchLivePrice (ver monitor.js): o que é vigiado não pode
// depender de qual aba está aberta.
async function consultarMercado(id, fresh = false, { deFundo = false } = {}) {
  const item = loadEstoque().find((e) => e.id === id);
  if (!item || item.itemId == null) return;

  const botao = () => {
    const card = findEstoqueCard(id);
    return card ? card.querySelector(".estoque-atualizar") : null;
  };
  const b = botao();
  if (b) b.disabled = true;

  try {
    let url =
      "/web/estoque/mercado?server=" + encodeURIComponent(item.server) +
      "&itemId=" + encodeURIComponent(item.itemId) +
      "&item=" + encodeURIComponent(item.searchName || item.nomeDigitado);
    if (fresh) url += "&fresh=1";

    const res = await fetch(url);
    if (!res.ok) throw new Error(await res.text());
    const data = await res.json();

    const agora = Date.now();
    const mudancas = { lastResult: data, lastCheckedAt: agora, mercadoEm: agora };
    // Um item validado pelo histórico não sabia o sufixo de slots; a primeira
    // consulta de mercado corrige o nome sem custar requisição nenhuma.
    if (data.displayName && data.displayName !== item.itemName) {
      mudancas.itemName = data.displayName;
    }
    const atualizado = updateEstoqueItem(id, mudancas);
    repintarCard(atualizado);
    avaliarUndercut(atualizado);
  } catch (err) {
    // A vez no rodízio avança mesmo com erro, como exige o monitor, senão este
    // item seria o escolhido de novo a cada tick. O dado anterior continua
    // sendo o mostrado, com a idade que ele tem de verdade.
    const atual = loadEstoque().find((e) => e.id === id);
    if (atual) updateEstoqueItem(id, { lastCheckedAt: Date.now(), mercadoEm: quandoDoMercado(atual) });
    if (!deFundo) {
      showToast(String(err.message || err).trim() || "Não foi possível consultar o mercado agora.");
    }
    // A falha pode ter sido justamente o 429 que suspendeu o site: aí o botão
    // fica travado como os outros (ver travarControlesDoEstoque).
    const depois = botao();
    if (depois) depois.disabled = siteSuspenso();
  }
}

// ---------------------------------------------------------------------------
// Undercutting
// ---------------------------------------------------------------------------

// estaSendoCortado diz se alguém vende este item mais barato que você, pelo
// último retrato do mercado.
//
// Estritamente mais barato: empatar no menor preço não é ser cortado (a
// tabela já mostra isso como "empatado"). Os seus anúncios ficam de fora. Sem
// isso, o seu anúncio antigo, mais barato que um preço novo, dispararia um
// aviso contra você mesmo.
function estaSendoCortado(item) {
  if (!item.naLoja || item.precoVenda == null) return false;
  if (!item.lastResult || !item.lastResult.found) return false;
  const { outros } = separarAnuncios(item.lastResult);
  return outros.length > 0 && outros[0].price < item.precoVenda;
}

// O sino avisa de um CRUZAMENTO: você não estava sendo cortado e agora está.
// O "notified" da entrada guarda se o corte atual já é sabido, com a mesma
// mecânica de avaliarHit na watchlist. Ele segura o aviso enquanto o corte
// continuar, e se rearma quando o corte acaba: um aviso por cruzamento, e não
// um a cada volta do rodízio.
//
// comAvisoArmado completa uma decisão do usuário (preço, sino, loja) com o
// "notified" do estado que resulta dela. Quem decide está olhando para o
// card: se ele já diz que há alguém mais barato, isso é sabido, e repetir
// pelo Telegram um minuto depois seria avisar do que a pessoa acabou de ler.
// É também o que deixa a estratégia de segurar funcionar, porque anunciar
// acima do mercado de propósito não pode render um aviso a cada edição.
function comAvisoArmado(atual, mudancas) {
  return { ...mudancas, notified: estaSendoCortado({ ...atual, ...mudancas }) };
}

// avaliarUndercut roda a cada retrato novo do mercado, com ou sem o card na
// tela: o sino existe justamente para avisar quem não está olhando.
//
// O "notified" acompanha o corte mesmo com o sino desligado. Um corte que
// começou em silêncio já está no card quando o sino for ligado, e não é
// novidade para ninguém.
function avaliarUndercut(item) {
  if (!item) return;
  const cortado = estaSendoCortado(item);
  if (cortado && !item.notified) {
    updateEstoqueItem(item.id, { notified: true });
    if (item.undercut) avisarUndercut(item);
  } else if (!cortado && item.notified) {
    updateEstoqueItem(item.id, { notified: false });
  }
}

// avisarUndercut sai pelos mesmos canais da watchlist (ver avisar em
// watchlist.js). A loja do concorrente vai só no Telegram: ele é lido longe
// da tela, e o toast é lido com o card ao lado.
function avisarUndercut(item) {
  const maisBarato = separarAnuncios(item.lastResult).outros[0];
  const nome = nomeVisivel(item);
  const valores =
    formatMoney(maisBarato.price) + ", abaixo dos seus " + formatMoney(item.precoVenda);

  let telegram =
    "<b>" + escapeTelegramHtml(nome) + "</b>: alguém está vendendo por <b>" +
    formatMoney(maisBarato.price) + "</b>, abaixo dos seus " + formatMoney(item.precoVenda);
  if (maisBarato.storeName) {
    telegram += "\n\nLoja: <b>" + escapeTelegramHtml(maisBarato.storeName) + "</b>";
  }
  avisar(nome + ": alguém está vendendo por " + valores, telegram);
}

// buildBlocoDeMercado desenha o que se sabe do mercado agora. São três
// situações bem diferentes, e a interface não pode confundi-las:
//
//   ninguém anuncia        -> não há concorrência (nem referência de preço)
//   só você anuncia        -> não há concorrência, e isso é bom
//   terceiros anunciando   -> o menor preço deles é o número que interessa
function buildBlocoDeMercado(item) {
  const bloco = document.createElement("div");
  bloco.className = "estoque-mercado";

  if (!item.lastResult) return bloco;

  const { meus, outros } = separarAnuncios(item.lastResult);

  const linha = document.createElement("span");
  linha.className = "estoque-mercado-linha";

  if (!item.lastResult.found) {
    linha.textContent = "Ninguém está anunciando este item.";
  } else if (outros.length === 0) {
    linha.textContent = "Você é o único anunciando este item.";
    linha.classList.add("estoque-mercado-sozinho");
  } else {
    const maisBarato = outros[0];
    const unidades = somarUnidades(outros);
    linha.textContent =
      "Mercado: " + formatMoney(maisBarato.price) +
      " · " + (unidades === 1 ? "1 unidade" : unidades + " unidades") +
      " em " + (outros.length === 1 ? "1 anúncio" : outros.length + " anúncios");
    if (maisBarato.storeName) linha.title = "Loja mais barata: " + maisBarato.storeName;
  }
  bloco.appendChild(linha);

  if (meus.length > 0) {
    const seu = document.createElement("span");
    seu.className = "estoque-mercado-seu";
    const meuMaisBarato = meus[0];
    seu.textContent = "Seu anúncio: " + formatMoney(meuMaisBarato.price);
    // A comparação que interessa a quem vende: alguém está abaixo de você?
    if (outros.length > 0 && outros[0].price < meuMaisBarato.price) {
      seu.textContent += " — estão vendendo mais barato";
      seu.classList.add("estoque-mercado-cortado");
    }
    bloco.appendChild(seu);
  }

  // Honestidade sobre o que não foi olhado: o servidor corta a lista de
  // anúncios (ver maxAnunciosPorItem), e um item muito popular pode ter mais
  // do que isso.
  if (item.lastResult.truncated) {
    const aviso = document.createElement("span");
    aviso.className = "estoque-mercado-aviso";
    aviso.textContent = "Mostrando só os anúncios mais baratos.";
    bloco.appendChild(aviso);
  }

  return bloco;
}

// ---------------------------------------------------------------------------
// Histórico de vendas
// ---------------------------------------------------------------------------

// consultarHistorico busca a série diária de vendas do item na janela
// escolhida. Sai ao validar e a cada troca do seletor — e só para AQUELE item,
// que é o motivo de a janela ser por card e não da tela.
async function consultarHistorico(id, fresh = false) {
  const item = loadEstoque().find((e) => e.id === id);
  if (!item || item.itemId == null || item.svrId == null) return;

  const janela = janelaDoItem(item);
  const card = findEstoqueCard(id);
  const seletor = card ? card.querySelector(".estoque-janela") : null;
  if (seletor) seletor.disabled = true;

  try {
    let url =
      "/web/estoque/historico?itemId=" + encodeURIComponent(item.itemId) +
      "&svrId=" + encodeURIComponent(item.svrId) +
      "&janela=" + encodeURIComponent(janela);
    if (fresh) url += "&fresh=1";

    const res = await fetch(url);
    if (!res.ok) throw new Error(await res.text());
    const data = await res.json();
    repintarCard(updateEstoqueItem(id, { historico: data }));
  } catch (err) {
    showToast(String(err.message || err).trim() || "Não foi possível consultar o histórico agora.");
    const depois = findEstoqueCard(id);
    const seletorDepois = depois ? depois.querySelector(".estoque-janela") : null;
    if (seletorDepois) seletorDepois.disabled = siteSuspenso();
  }
}

function rotuloDaJanela(valor) {
  const janela = JANELAS.find((j) => j.valor === valor);
  return janela ? janela.rotulo.toLowerCase() : valor;
}

// buildBlocoDeHistorico mostra o resumo da janela numa linha e esconde a
// tabela de dias atrás de um <details>.
//
// A tabela fica recolhida porque a janela de 30 dias (ou "tudo") pode ter
// dezenas de linhas, e os cards do estoque dividem uma grade: um card aberto
// esticaria a linha inteira. O resumo — que é o que se olha no dia a dia —
// cabe em uma linha e fica sempre visível.
function buildBlocoDeHistorico(item) {
  const bloco = document.createElement("div");
  bloco.className = "estoque-historico";

  const dados = item.historico;
  if (!dados) return bloco;

  if (!dados.days || dados.days.length === 0) {
    const resumo = document.createElement("p");
    resumo.className = "estoque-historico-resumo";
    resumo.textContent = "Sem vendas registradas " + textoDaJanela(dados.window) + ".";
    bloco.appendChild(resumo);
    return bloco;
  }

  // Os agregados do período viram tiles, e não uma frase corrida.
  //
  // Espremidos numa linha só eles se atropelavam e quebravam no meio de um
  // valor. Em caixas lado a lado cada número tem rótulo e respiro — e eles
  // espelham as colunas da tabela logo abaixo, então a leitura é a mesma de
  // cima para baixo: o período inteiro primeiro, depois dia a dia.
  const s = dados.summary;
  const tendencia = calcularTendencia(dados);

  const tiles = document.createElement("div");
  tiles.className = "estoque-tiles";
  tiles.appendChild(buildTile("Mínimo", formatMoney(s.min), "", "", "hist-min"));
  tiles.appendChild(
    buildTile(
      "Médio",
      formatMoney(Math.round(s.weightedAvg)),
      // A tendência só aparece quando existe: uma seta para toda variação de
      // 1% seria ruído com aparência de sinal (ver calcularTendencia).
      tendencia
        ? (tendencia.subindo ? "↗ " : "↘ ") +
          Math.abs(Math.round(tendencia.variacao * 100)) + "% vs. o período anterior"
        : "",
      tendencia ? (tendencia.subindo ? "is-subindo" : "is-caindo") : "",
      "hist-med",
    ),
  );
  tiles.appendChild(buildTile("Máximo", formatMoney(s.max), "", "", "hist-max"));
  tiles.appendChild(
    buildTile("Vendido", s.qtySold + " un.", rotuloDaJanela(dados.window), "", "hist-qtd"),
  );
  bloco.appendChild(tiles);

  // Sem <details>: a aba já é a divulgação. Quando o histórico morava no card,
  // dentro de uma grade, recolher a tabela era o que impedia um item de
  // esticar a linha inteira — numa aba própria isso virou um clique a mais
  // para ver o que a aba existe para mostrar.
  const detalhe = document.createElement("div");
  detalhe.className = "estoque-historico-dias";

  const legenda = document.createElement("p");
  legenda.className = "estoque-historico-legenda";
  // Quantos dias vieram e quantos existem: a diferença é o que diz ao usuário
  // que trocar para uma janela maior tem o que mostrar.
  const quantos = dados.days.length === 1 ? "1 dia com venda" : dados.days.length + " dias com venda";
  legenda.textContent =
    quantos + (dados.daysAvailable > dados.days.length ? " de " + dados.daysAvailable + " registrados" : "");
  detalhe.appendChild(legenda);

  const tabela = document.createElement("table");
  tabela.className = "estoque-dias";
  const cabecalho = document.createElement("thead");
  cabecalho.innerHTML =
    "<tr><th>Dia</th><th>Mín.</th><th>Méd.</th><th>Máx.</th><th>Qtd.</th></tr>";
  tabela.appendChild(cabecalho);

  const corpo = document.createElement("tbody");
  for (const dia of dados.days) {
    const tr = document.createElement("tr");
    for (const valor of [
      dia.date,
      formatMoney(dia.min),
      formatMoney(dia.avg),
      formatMoney(dia.max),
      String(dia.qty),
    ]) {
      const td = document.createElement("td");
      td.textContent = valor;
      tr.appendChild(td);
    }
    corpo.appendChild(tr);
  }
  tabela.appendChild(corpo);
  detalhe.appendChild(tabela);
  bloco.appendChild(detalhe);

  return bloco;
}

function textoDaJanela(janela) {
  return janela === "ALL" ? "em todo o histórico" : "nos últimos " + rotuloDaJanela(janela);
}

// ---------------------------------------------------------------------------
// Régua de preços
// ---------------------------------------------------------------------------

// NUM_BALDES é em quantas faixas de preço o histograma divide o mercado.
// Oito é o que cabe legível na largura do painel sem virar um borrão.
const NUM_BALDES = 8;

// buildHistograma mostra onde cada anúncio está na escala de preço: altura da
// barra = quantos anúncios caem naquela faixa, e a faixa onde VOCÊ está
// pintada na cor do seu status.
//
// A escala é LOGARÍTMICA porque é isso que o problema pede. Num item cujos
// anúncios vão de 400k a 22M, uma escala linear empilharia os baratos numa
// barra só e deixaria o resto do gráfico vazio — escondendo justamente a
// estrutura que o gráfico existe para mostrar: se o mercado está partido em
// faixas, o vazio entre elas aparece aqui.
function buildHistograma(item, sugestao, statusInfo) {
  const { meus, outros } = separarAnuncios(item.lastResult);
  const todos = [...outros, ...meus];
  if (todos.length < 2) return null;

  const precos = todos.map((a) => a.price).filter((p) => p > 0);
  const menor = Math.min(...precos);
  const maior = Math.max(...precos);
  if (maior <= menor) return null;

  const base = Math.log(menor);
  const amplitude = Math.log(maior) - base;
  const baldeDe = (preco) =>
    Math.min(NUM_BALDES - 1, Math.floor(((Math.log(preco) - base) / amplitude) * NUM_BALDES));

  const baldes = Array.from({ length: NUM_BALDES }, () => ({ total: 0, seu: false }));
  for (const anuncio of todos) {
    if (anuncio.price <= 0) continue;
    baldes[baldeDe(anuncio.price)].total++;
  }
  const seuBalde = item.precoVenda != null && item.precoVenda > 0 ? baldeDe(item.precoVenda) : -1;
  if (seuBalde >= 0 && seuBalde < NUM_BALDES) baldes[seuBalde].seu = true;

  const pico = Math.max(...baldes.map((b) => b.total), 1);

  const bloco = document.createElement("div");
  bloco.className = "estoque-histograma";

  const titulo = document.createElement("p");
  titulo.className = "estoque-secao-titulo";
  titulo.textContent = "Onde cada anúncio está";
  bloco.appendChild(titulo);

  const grafico = document.createElement("div");
  grafico.className = "estoque-barras";
  grafico.setAttribute("role", "img");
  grafico.setAttribute(
    "aria-label",
    todos.length + " anúncios entre " + formatMoney(menor) + " e " + formatMoney(maior) +
      (seuBalde >= 0 ? ", com o seu preço em " + formatMoney(item.precoVenda) : ""),
  );

  for (const balde of baldes) {
    const coluna = document.createElement("div");
    coluna.className = "estoque-barra-coluna";
    const barra = document.createElement("div");
    barra.className = "estoque-barra";
    if (balde.seu) barra.classList.add("is-seu", "estoque-barra-" + statusInfo.status);
    // Uma faixa vazia continua ocupando espaço: é o vazio que revela o mercado
    // partido, e escondê-lo apagaria a informação.
    barra.style.height = (balde.total === 0 ? 2 : Math.round((balde.total / pico) * 100)) + "%";
    barra.title = balde.total === 1 ? "1 anúncio" : balde.total + " anúncios";
    coluna.appendChild(barra);
    if (balde.seu) {
      const marca = document.createElement("span");
      marca.className = "estoque-barra-voce";
      marca.textContent = "você";
      coluna.appendChild(marca);
    }
    grafico.appendChild(coluna);
  }
  bloco.appendChild(grafico);

  const pontas = document.createElement("div");
  pontas.className = "estoque-barras-pontas";
  const esq = document.createElement("span");
  esq.textContent = formatMoney(menor);
  const meio = document.createElement("span");
  meio.className = "estoque-barras-meio";
  meio.textContent = sugestao.faixas
    ? sugestao.faixas.baixa.length + " anúncios até " +
      formatMoney(sugestao.faixas.baixa[sugestao.faixas.baixa.length - 1].price) + " · " +
      sugestao.faixas.alta.length + " a partir de " + formatMoney(sugestao.faixas.alta[0].price)
    : "mercado uniforme: " + todos.length + " anúncios na mesma faixa";
  const dir = document.createElement("span");
  dir.textContent = formatMoney(maior);
  pontas.appendChild(esq);
  pontas.appendChild(meio);
  pontas.appendChild(dir);
  bloco.appendChild(pontas);

  return bloco;
}

function buildTile(rotulo, valor, detalhe, classeDetalhe, classeTile) {
  const tile = document.createElement("div");
  tile.className = "estoque-tile" + (classeTile ? " estoque-tile-" + classeTile : "");
  const r = document.createElement("span");
  r.className = "estoque-tile-rotulo";
  r.textContent = rotulo;
  tile.appendChild(r);
  const v = document.createElement("span");
  v.className = "estoque-tile-valor";
  v.textContent = valor;
  tile.appendChild(v);
  if (detalhe) {
    const d = document.createElement("span");
    d.className = "estoque-tile-detalhe" + (classeDetalhe ? " " + classeDetalhe : "");
    d.textContent = detalhe;
    tile.appendChild(d);
  }
  return tile;
}

// buildAbaMercado: os quatro números que resumem a situação, o histograma e a
// expectativa de venda.
function buildAbaMercado(item, sugestao, statusInfo) {
  const bloco = document.createElement("div");

  const tiles = document.createElement("div");
  tiles.className = "estoque-tiles";

  const outros = sugestao.concorrencia || [];
  const { meus } = separarAnuncios(item.lastResult);
  const souOMaisBarato = statusInfo.status === STATUS_NA_FRENTE && meus.length > 0;

  tiles.appendChild(
    buildTile(
      "Mais barato",
      outros.length > 0 ? formatMoney(outros[0].price) : "—",
      souOMaisBarato ? "(você)" : "",
      "",
      "mais-barato",
    ),
  );
  tiles.appendChild(
    buildTile(
      "Oferta",
      outros.length > 0 ? somarUnidades(outros) + " un." : "—",
      outros.length > 0 ? outros.length + (outros.length === 1 ? " anúncio" : " anúncios") : "",
      "",
      "oferta",
    ),
  );

  const resumo = item.historico && item.historico.summary;
  // O rótulo acompanha a janela escolhida: dizer "vendido 7 d" com o seletor
  // em 30 dias seria mentira.
  const rotuloJanela = "Vendido · " + rotuloDaJanela(janelaDoItem(item));
  const tend = sugestao.tendencia;
  tiles.appendChild(
    buildTile(
      rotuloJanela,
      resumo && resumo.qtySold > 0 ? resumo.qtySold + " un." : "—",
      tend ? (tend.subindo ? "↗ " : "↘ ") + Math.abs(Math.round(tend.variacao * 100)) + "%" : "",
      tend ? (tend.subindo ? "is-subindo" : "is-caindo") : "",
      "vendido",
    ),
  );
  tiles.appendChild(
    buildTile(
      "Faixa sugerida",
      sugestao.faixa ? formatMoney(sugestao.faixa.min) + " – " + formatMoney(sugestao.faixa.max) : "—",
      ROTULO_CONFIANCA[sugestao.confianca.nivel],
      "estoque-confianca-" + sugestao.confianca.nivel,
      "faixa",
    ),
  );
  bloco.appendChild(tiles);

  const histograma = buildHistograma(item, sugestao, statusInfo);
  if (histograma) bloco.appendChild(histograma);

  if (sugestao.tempoNoSeuPreco || sugestao.posicao) {
    const rodape = document.createElement("div");
    rodape.className = "estoque-venda-estimada";
    const r = document.createElement("span");
    r.className = "estoque-tile-rotulo";
    r.textContent = "Venda estimada";
    rodape.appendChild(r);
    const v = document.createElement("span");
    v.className = "estoque-venda-valor";
    // Os FATOS primeiro — colocação e distância do mais barato —, e só depois a
    // estimativa de tempo. Os dois primeiros se movem a cada zeny que o
    // usuário digita; o terceiro é grosseiro de propósito, e sozinho daria a
    // impressão de que o card não reage ao preço.
    const partes = [];
    if (sugestao.posicao) partes.push(sugestao.posicao.colocacao + "º de " + sugestao.posicao.total);
    if (sugestao.distancia != null) {
      const pct = Math.round(Math.abs(sugestao.distancia) * 100);
      partes.push(
        pct === 0
          ? "no mesmo preço do mais barato"
          : pct + "% " + (sugestao.distancia > 0 ? "acima" : "abaixo") + " do mais barato",
      );
    }
    if (sugestao.tempoNoSeuPreco) partes.push(sugestao.tempoNoSeuPreco);
    v.textContent = partes.join(" · ") || "—";
    rodape.appendChild(v);
    bloco.appendChild(rodape);
  }

  return bloco;
}

const ROTULO_CONFIANCA = {
  alta: "confiança alta",
  media: "confiança média",
  baixa: "confiança baixa",
  nenhuma: "sem dados",
};

// buildCenarios lista as três estratégias, cada uma com preço, expectativa e a
// aposta que embute.
//
// Sem <details>, pelo mesmo motivo da tabela de dias: quando isto morava no
// card, dentro de uma grade, recolher era o que impedia um item de esticar a
// linha inteira. Numa aba própria, a aba já é a divulgação — e esconder o
// conteúdo atrás de mais um clique é esconder o que se foi ver.
function buildCenarios(cenarios) {
  const bloco = document.createElement("div");
  bloco.className = "estoque-cenarios";

  for (const cenario of cenarios) {
    const item = document.createElement("div");
    item.className = "estoque-cenario";

    const cabecalho = document.createElement("div");
    cabecalho.className = "estoque-cenario-topo";

    const nome = document.createElement("span");
    nome.className = "estoque-cenario-nome";
    nome.textContent = cenario.nome;
    cabecalho.appendChild(nome);

    const preco = document.createElement("span");
    preco.className = "estoque-cenario-preco";
    preco.textContent = formatMoney(cenario.preco);
    cabecalho.appendChild(preco);

    if (cenario.expectativa) {
      const expectativa = document.createElement("span");
      expectativa.className = "estoque-cenario-expectativa";
      expectativa.textContent = cenario.expectativa;
      cabecalho.appendChild(expectativa);
    }

    item.appendChild(cabecalho);

    const aposta = document.createElement("p");
    aposta.className = "estoque-cenario-aposta";
    aposta.textContent = cenario.aposta;
    item.appendChild(aposta);

    bloco.appendChild(item);
  }
  return bloco;
}

function buildAbaRessalvas(ressalvas) {
  const bloco = document.createElement("div");
  bloco.className = "estoque-ressalvas";
  if (ressalvas.length === 0) {
    const vazio = document.createElement("p");
    vazio.className = "estoque-detalhe-vazio";
    vazio.textContent = "Nada a ressalvar: os números desta tela são diretos.";
    bloco.appendChild(vazio);
    return bloco;
  }
  for (const ressalva of ressalvas) {
    const item = document.createElement("div");
    item.className = "estoque-ressalva";
    const t = document.createElement("span");
    t.className = "estoque-ressalva-titulo";
    t.textContent = ressalva.titulo;
    item.appendChild(t);
    const p = document.createElement("p");
    p.className = "estoque-ressalva-texto";
    p.textContent = ressalva.texto;
    item.appendChild(p);
    bloco.appendChild(item);
  }
  return bloco;
}

// ---------------------------------------------------------------------------
// Card
// ---------------------------------------------------------------------------

function pintarStatus(el, validacao) {
  el.textContent = ROTULO_VALIDACAO[validacao] || ROTULO_VALIDACAO[VALIDACAO_PENDENTE];
  el.className = "estoque-status estoque-status-" + (validacao || VALIDACAO_PENDENTE);
}

// pintarToggle deixa botão e rótulo de acordo com o estado. aria-pressed, e
// não uma classe só: é um botão de alternância, e é assim que o leitor de
// tela anuncia "ligado"/"desligado".
function buildSinoIcon() {
  const NS = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("stroke-width", "2");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  const corpo = document.createElementNS(NS, "path");
  corpo.setAttribute("d", "M18 8a6 6 0 1 0-12 0c0 7-3 9-3 9h18s-3-2-3-9");
  const badalo = document.createElementNS(NS, "path");
  badalo.setAttribute("d", "M13.7 21a2 2 0 0 1-3.4 0");
  svg.appendChild(corpo);
  svg.appendChild(badalo);
  return svg;
}

function pintarSino(botao, ligado) {
  botao.setAttribute("aria-pressed", String(ligado));
  botao.classList.toggle("is-on", ligado);
  botao.setAttribute(
    "aria-label",
    ligado ? "Desligar o aviso de quando alguém vender mais barato" : "Avisar quando alguém vender mais barato",
  );
}

function pintarToggle(botao, ligado, rotuloLigado, rotuloDesligado) {
  botao.setAttribute("aria-pressed", String(ligado));
  botao.classList.toggle("is-on", ligado);
  botao.textContent = ligado ? rotuloLigado : rotuloDesligado;
}

// O undercutting só faz sentido para um item que está na loja: não há o que
// comparar com o mercado se você não está vendendo. Desabilitar (em vez de
// esconder) mantém o card estável e mostra que a opção existe.
function aplicarDisponibilidadeDoUndercut(card, item) {
  const botao = card.querySelector(".estoque-sino");
  if (!botao) return;
  botao.disabled = !item.naLoja;
  botao.title = item.naLoja
    ? "Avisar quando alguém estiver vendendo mais barato que você"
    : "Disponível apenas para itens que estão na loja";
}

// Qual aba do painel está aberta. Persistida: quem está comparando histórico
// entre itens não quer voltar para "Mercado" a cada troca de seleção.
const ESTOQUE_ABA_KEY = "ro-market-tracker:estoque-aba";
const ABAS = ["mercado", "historico", "estrategias", "ressalvas"];
const ROTULO_ABA = {
  mercado: "Mercado",
  historico: "Histórico",
  estrategias: "Estratégias",
  ressalvas: "Ressalvas",
};

function abaAtiva() {
  try {
    const salva = localStorage.getItem(ESTOQUE_ABA_KEY);
    if (ABAS.includes(salva)) return salva;
  } catch {
    // Sem preferência salva: começa no mercado.
  }
  return "mercado";
}

function gravarAba(aba) {
  try {
    localStorage.setItem(ESTOQUE_ABA_KEY, aba);
  } catch {
    // Ver saveEstoque.
  }
}

// buildEstoqueCard monta o painel de detalhe do item selecionado.
//
// A ordem é a da decisão: quem é este item, por quanto você vende, O QUE
// FAZER AGORA (a tarja), e só então os números que sustentam isso — em abas,
// porque juntos eles não cabem e, separados por assunto, cada um é procurado
// quando faz falta.
function buildEstoqueCard(item) {
  const li = document.createElement("li");
  li.className = "estoque-card";
  li.dataset.id = item.id;

  // A volta ao resumo precisa de um botão à vista: cadastrar um item já o
  // abre, e sem este caminho o resumo do estoque sumiria depois do primeiro
  // cadastro e não voltaria mais. Numa linha própria, e não no cabeçalho:
  // lá ele tiraria espaço do nome do item, que já disputa a linha com o
  // seletor de janela e os botões.
  const voltar = document.createElement("button");
  voltar.type = "button";
  voltar.className = "estoque-voltar-resumo";
  voltar.textContent = "← Resumo do estoque";
  li.appendChild(voltar);

  // --- cabeçalho ---
  const topo = document.createElement("div");
  topo.className = "estoque-painel-topo";

  const nome = document.createElement("h3");
  nome.className = "estoque-painel-nome";
  nome.textContent = nomeVisivel(item);
  topo.appendChild(nome);

  const status = document.createElement("span");
  pintarStatus(status, item.validacao);
  topo.appendChild(status);

  const janela = document.createElement("select");
  janela.className = "estoque-janela";
  janela.setAttribute("aria-label", "Janela do histórico de " + nomeVisivel(item));
  for (const opcao of JANELAS) {
    const option = document.createElement("option");
    option.value = opcao.valor;
    option.textContent = opcao.rotulo;
    if (opcao.valor === janelaDoItem(item)) option.selected = true;
    janela.appendChild(option);
  }
  topo.appendChild(janela);

  const validar = document.createElement("button");
  validar.type = "button";
  validar.className = "estoque-validar";
  validar.textContent = item.validacao === VALIDACAO_INVALIDO ? "Tentar de novo" : "Validar";
  topo.appendChild(validar);

  // Só faz sentido depois de validado: sem itemId não há o que consultar.
  if (item.validacao === VALIDACAO_OK) {
    const atualizar = document.createElement("button");
    atualizar.type = "button";
    atualizar.className = "estoque-atualizar";
    atualizar.textContent = "↻";
    atualizar.title = "Consultar o mercado agora";
    atualizar.setAttribute("aria-label", "Consultar o mercado agora para " + nomeVisivel(item));
    topo.appendChild(atualizar);

    const quando = document.createElement("span");
    quando.className = "estoque-atualizado-em";
    paintUpdatedAt(quando, quandoDoMercado(item));
    topo.appendChild(quando);
  }

  const remover = document.createElement("button");
  remover.type = "button";
  remover.className = "estoque-remover";
  remover.textContent = "×";
  remover.setAttribute("aria-label", "Remover " + nomeVisivel(item) + " do estoque");
  remover.title = "Remover do estoque";
  topo.appendChild(remover);

  li.appendChild(topo);

  // --- linha do seu preço e dos interruptores ---
  const sub = document.createElement("div");
  sub.className = "estoque-painel-sub";

  const precoVenda = document.createElement("span");
  precoVenda.className = "estoque-preco-venda";
  precoVenda.textContent = precoVendaLabel(item.precoVenda);
  precoVenda.tabIndex = 0;
  precoVenda.title = "Clique para editar o preço de venda";
  sub.appendChild(precoVenda);

  const loja = document.createElement("button");
  loja.type = "button";
  loja.className = "estoque-toggle estoque-toggle-loja";
  pintarToggle(loja, item.naLoja, "Na loja", "Fora da loja");
  sub.appendChild(loja);

  // Sininho, e não um rótulo escrito: com a bolinha da tabela mostrando o
  // estado, este interruptor deixou de ser sobre exibição e passou a
  // significar uma coisa só — "me avise mesmo quando eu não estiver na tela".
  const undercut = document.createElement("button");
  undercut.type = "button";
  undercut.className = "estoque-toggle estoque-sino";
  undercut.appendChild(buildSinoIcon());
  pintarSino(undercut, item.undercut);
  sub.appendChild(undercut);

  li.appendChild(sub);

  // --- escolha de candidato, quando o nome foi ambíguo ---
  if (Array.isArray(item.candidatos) && item.candidatos.length > 0) {
    li.appendChild(buildEscolhaDeCandidato(item));
  }
  if (item.validacao === VALIDACAO_INVALIDO && item.motivo) {
    const motivo = document.createElement("p");
    motivo.className = "estoque-motivo";
    motivo.textContent = item.motivo;
    li.appendChild(motivo);
  }

  aplicarDisponibilidadeDoUndercut(li, item);

  // Sem validar não há o que comparar, medir nem sugerir.
  if (item.validacao !== VALIDACAO_OK) return li;

  const sugestao = calcularSugestao(item);
  const statusInfo = classificarStatus(item, { naFila: filaDeValidacao.has(item.id) });

  li.appendChild(buildTarja(item, sugestao, statusInfo));
  li.appendChild(buildAbas(item, sugestao, statusInfo));
  return li;
}

// aplicarNovoPreco grava o preço sugerido E o copia para a área de
// transferência.
//
// A cópia não é conveniência: o programa NÃO consegue mexer na sua loja
// dentro do jogo. Quem aplica o preço é você, e com o valor já na área de
// transferência isso é um colar. Enquanto você não fizer, o programa está
// acreditando num preço que a loja não pratica — o botão avisa disso no
// toast, que é o único lugar onde cabe dizer sem poluir a tela.
function aplicarNovoPreco(id, preco, botao) {
  if (!Number.isFinite(preco) || preco <= 0) return;

  const atual = loadEstoque().find((e) => e.id === id);
  if (!atual) return;
  const atualizado = updateEstoqueItem(id, comAvisoArmado(atual, { precoVenda: preco }));
  if (!atualizado) return;
  repintarCard(atualizado);

  const texto = String(preco);
  if (navigator.clipboard) {
    navigator.clipboard.writeText(texto).then(
      () => showToast(formatMoney(preco) + " copiado. Aplique o preço na sua loja dentro do jogo."),
      () => showToast("Preço anotado. Aplique " + formatMoney(preco) + " na sua loja dentro do jogo."),
    );
  } else {
    showToast("Preço anotado. Aplique " + formatMoney(preco) + " na sua loja dentro do jogo.");
  }
  if (botao) botao.blur();
}

// buildTarja é o que o usuário lê primeiro: em que situação este item está e
// o que dá para fazer a respeito agora.
function buildTarja(item, sugestao, statusInfo) {
  const { titulo, detalhe, acao } = montarTarja(item, sugestao, statusInfo);

  const tarja = document.createElement("div");
  tarja.className = "estoque-tarja estoque-tarja-" + statusInfo.status;

  const bolinha = document.createElement("span");
  bolinha.className = "estoque-bolinha estoque-bolinha-" + statusInfo.status;
  bolinha.setAttribute("aria-hidden", "true");
  tarja.appendChild(bolinha);

  const texto = document.createElement("p");
  texto.className = "estoque-tarja-texto";
  const forte = document.createElement("strong");
  forte.textContent = titulo;
  texto.appendChild(forte);
  texto.appendChild(document.createTextNode(" " + detalhe));
  tarja.appendChild(texto);

  if (acao && acao.validar) {
    const botao = document.createElement("button");
    botao.type = "button";
    botao.className = "estoque-acao estoque-validar";
    botao.textContent = "Validar agora";
    tarja.appendChild(botao);
  } else if (acao) {
    const botao = document.createElement("button");
    botao.type = "button";
    botao.className = "estoque-acao estoque-reprecificar";
    botao.dataset.preco = String(acao.preco);
    botao.textContent = acao.rotulo;
    tarja.appendChild(botao);
  }
  return tarja;
}

function buildAbas(item, sugestao, statusInfo) {
  const ressalvas = montarRessalvas(item, sugestao);
  const contagens = { estrategias: sugestao.cenarios.length, ressalvas: ressalvas.length };

  const bloco = document.createElement("div");

  const tiras = document.createElement("div");
  tiras.className = "estoque-abas";
  tiras.setAttribute("role", "tablist");

  const ativa = abaAtiva();
  for (const aba of ABAS) {
    const botao = document.createElement("button");
    botao.type = "button";
    botao.className = "estoque-aba" + (aba === ativa ? " is-ativa" : "");
    botao.dataset.aba = aba;
    botao.setAttribute("role", "tab");
    botao.setAttribute("aria-selected", String(aba === ativa));
    botao.textContent = ROTULO_ABA[aba];
    // A contagem na própria aba avisa que há o que ler ali sem ocupar espaço
    // nenhum na tela.
    if (contagens[aba]) {
      const conta = document.createElement("span");
      conta.className = "estoque-aba-conta";
      conta.textContent = String(contagens[aba]);
      botao.appendChild(conta);
    }
    tiras.appendChild(botao);
  }
  bloco.appendChild(tiras);

  const conteudo = document.createElement("div");
  conteudo.className = "estoque-aba-conteudo";
  conteudo.setAttribute("role", "tabpanel");
  if (ativa === "mercado") {
    conteudo.appendChild(buildAbaMercado(item, sugestao, statusInfo));
  } else if (ativa === "historico") {
    conteudo.appendChild(buildBlocoDeHistorico(item));
  } else if (ativa === "estrategias") {
    conteudo.appendChild(
      sugestao.cenarios.length > 0
        ? buildCenarios(sugestao.cenarios)
        : semEstrategias(sugestao),
    );
  } else {
    conteudo.appendChild(buildAbaRessalvas(ressalvas));
  }
  bloco.appendChild(conteudo);
  return bloco;
}

// Com confiança baixa nenhum preço é recomendado — e dizer por quê é mais
// útil do que uma aba vazia.
function semEstrategias(sugestao) {
  const p = document.createElement("p");
  p.className = "estoque-detalhe-vazio";
  p.textContent = sugestao.confianca.motivo
    ? "Nenhum preço é recomendado para este item. " + sugestao.confianca.motivo
    : "Ainda não há dados suficientes para recomendar um preço.";
  return p;
}

// ---------------------------------------------------------------------------
// Meus personagens
// ---------------------------------------------------------------------------

function renderPersonagens() {
  const lista = document.getElementById("estoque-personagens-lista");
  const contagem = document.getElementById("estoque-personagens-contagem");
  if (!lista) return;

  const personagens = carregarPersonagens();
  if (contagem) contagem.textContent = String(personagens.length);

  lista.innerHTML = "";
  for (const nome of personagens) {
    const li = document.createElement("li");
    li.className = "estoque-personagem";
    li.dataset.nome = nome;

    const texto = document.createElement("span");
    texto.textContent = nome;
    li.appendChild(texto);

    const remover = document.createElement("button");
    remover.type = "button";
    remover.className = "estoque-personagem-remover";
    remover.textContent = "×";
    remover.setAttribute("aria-label", "Remover o personagem " + nome);
    li.appendChild(remover);

    lista.appendChild(li);
  }
}

// mudarPersonagens grava a lista e redesenha o estoque inteiro. Redesenhar
// todos os cards é o ponto: quais anúncios são seus muda para TODO item de
// uma vez, e como a separação é feita no navegador (ver separarAnuncios),
// isso não custa requisição nenhuma.
//
// O aviso de undercutting se rearma junto, pelo mesmo motivo do preço (ver
// comAvisoArmado): tirar um nome da lista pode transformar um anúncio seu em
// concorrência mais barata, e isso a tela já mostra na hora.
function mudarPersonagens(lista) {
  salvarPersonagens(lista);
  saveEstoque(loadEstoque().map((item) => ({ ...item, ...comAvisoArmado(item, {}) })));
  renderPersonagens();
  renderEstoque();
}

function adicionarPersonagem(nome) {
  const limpo = nome.trim();
  if (limpo === "") return false;

  const personagens = carregarPersonagens();
  if (personagens.some((n) => n.toLowerCase() === limpo.toLowerCase())) {
    showToast("«" + limpo + "» já está na lista.");
    return false;
  }
  mudarPersonagens([...personagens, limpo]);
  return true;
}

function removerPersonagem(nome) {
  mudarPersonagens(carregarPersonagens().filter((n) => n !== nome));
}

// ---------------------------------------------------------------------------
// Loja offline
// ---------------------------------------------------------------------------

// Checagens mais velhas que isto não contam como evidência: o mercado é
// dinâmico, e um item consultado há quarenta minutos não diz nada sobre agora.
const FRESCOR_DA_EVIDENCIA_MS = 15 * 60 * 1000;

// avaliarPresencaNaLoja procura os seus anúncios entre os itens que você
// marcou como "na loja".
//
// Se nenhum deles aparece no mercado, algo aconteceu — e o caso que isto
// existe para pegar é o desconexão silenciosa: a loja caiu e você não viu.
//
// Mas NÃO dá para afirmar "loja offline": uma loja fechada e um estoque que
// vendeu tudo somem do mercado exatamente igual, e o site não distingue os
// dois. Por isso o aviso descreve o que foi observado e diz de quando são as
// checagens, em vez de cravar a causa.
function avaliarPresencaNaLoja() {
  if (carregarPersonagens().length === 0) return null;

  const limite = Date.now() - FRESCOR_DA_EVIDENCIA_MS;
  const checados = loadEstoque().filter(
    (item) =>
      item.naLoja &&
      item.validacao === VALIDACAO_OK &&
      quandoDoMercado(item) != null &&
      quandoDoMercado(item) >= limite,
  );
  if (checados.length === 0) return null;

  let comAnuncioSeu = 0;
  let maisAntiga = Infinity;
  let maisRecente = 0;
  for (const item of checados) {
    if (separarAnuncios(item.lastResult).meus.length > 0) comAnuncioSeu++;
    maisAntiga = Math.min(maisAntiga, quandoDoMercado(item));
    maisRecente = Math.max(maisRecente, quandoDoMercado(item));
  }

  return { itens: checados.length, comAnuncioSeu, maisAntiga, maisRecente };
}

function horaCurta(timestampMs) {
  return new Date(timestampMs).toLocaleTimeString("pt-BR", { hour: "2-digit", minute: "2-digit" });
}

// renderAvisoDeLoja pinta (ou esconde) o aviso no topo do Estoque. Roda a
// cada repintura, e não custa requisição nenhuma: a evidência já está no
// lastResult de cada item.
function renderAvisoDeLoja() {
  const aviso = document.getElementById("estoque-loja-aviso");
  if (!aviso) return;

  const presenca = avaliarPresencaNaLoja();
  if (!presenca || presenca.comAnuncioSeu > 0) {
    aviso.hidden = true;
    return;
  }

  const quantos =
    presenca.itens === 1
      ? "o item que você marcou como na loja"
      : "nenhum dos " + presenca.itens + " itens que você marcou como na loja";
  const janela =
    presenca.maisAntiga === presenca.maisRecente
      ? "Checagem das " + horaCurta(presenca.maisRecente) + "."
      : "Checagens entre " + horaCurta(presenca.maisAntiga) + " e " + horaCurta(presenca.maisRecente) + ".";

  aviso.textContent = "";

  const titulo = document.createElement("strong");
  titulo.textContent =
    presenca.itens === 1
      ? "Seu anúncio não está aparecendo no mercado."
      : "Nenhum dos seus anúncios está aparecendo no mercado.";
  aviso.appendChild(titulo);

  const detalhe = document.createElement("span");
  // A ambiguidade é dita, não escondida: as duas causas somem do mercado
  // igual, e mandar o usuário conferir a loja à toa é o custo de fingir
  // certeza.
  detalhe.textContent =
    " Sua loja pode ter caído, ou tudo pode ter sido vendido — procurando por " +
    quantos + ", nenhum tinha anúncio seu. " + janela;
  aviso.appendChild(detalhe);

  aviso.hidden = false;
}

// ---------------------------------------------------------------------------
// Montagem
// ---------------------------------------------------------------------------

// renderEstoque reconstrói a lista a partir do localStorage. Não dispara
// nenhuma requisição: cada card nasce do que já estava guardado.
// buildLinhaDoEstoque desenha uma linha da tabela: bolinha de status, nome,
// seu preço e a frase que resume a sua posição contra o mercado.
//
// A frase não é o número cru de propósito. "14 anúncios a 4.500 z" diz o que
// fazer; "menor preço: 4.500 z" não diz (ver classificarStatus).
function buildLinhaDoEstoque(item) {
  const tr = document.createElement("tr");
  tr.className = "estoque-linha";
  tr.dataset.id = item.id;
  tr.tabIndex = 0;
  tr.setAttribute("role", "option");

  const { status, texto } = classificarStatus(item, { naFila: filaDeValidacao.has(item.id) });
  tr.dataset.status = status;

  const tdItem = document.createElement("td");
  tdItem.className = "estoque-col-item";

  const bolinha = document.createElement("span");
  bolinha.className = "estoque-bolinha estoque-bolinha-" + status;
  bolinha.setAttribute("aria-hidden", "true");
  tdItem.appendChild(bolinha);

  const nome = document.createElement("span");
  nome.className = "estoque-linha-nome";
  nome.textContent = nomeVisivel(item);
  tdItem.appendChild(nome);
  tr.appendChild(tdItem);

  const tdPreco = document.createElement("td");
  tdPreco.className = "estoque-col-preco";
  tdPreco.textContent = item.precoVenda != null ? formatMoney(item.precoVenda) : "—";
  tr.appendChild(tdPreco);

  const tdMercado = document.createElement("td");
  tdMercado.className = "estoque-col-mercado estoque-mercado-" + status;
  tdMercado.textContent = texto;
  tr.appendChild(tdMercado);

  if (item.id === itemSelecionado()) {
    tr.classList.add("is-selecionada");
    tr.setAttribute("aria-selected", "true");
  }
  return tr;
}

// GRUPOS é a ordem em que os estados aparecem nas pílulas: do que exige ação
// para o que não exige.
const GRUPOS = [
  { status: STATUS_PERDENDO, rotulo: "perdendo" },
  { status: STATUS_EMPATADO, rotulo: "empatado" },
  { status: STATUS_NA_FRENTE, rotulo: "na frente" },
  { status: STATUS_NA_FILA, rotulo: "na fila" },
  { status: STATUS_SEM_DADOS, rotulo: "sem dados" },
];

function renderResumoDoTopo() {
  const contagem = document.getElementById("estoque-contagem");
  const pilulas = document.getElementById("estoque-pilulas");
  if (!contagem || !pilulas) return;

  const lista = loadEstoque();
  contagem.textContent = lista.length === 1 ? "1 item" : lista.length + " itens";

  const porStatus = {};
  for (const item of lista) {
    const { status } = classificarStatus(item, { naFila: filaDeValidacao.has(item.id) });
    porStatus[status] = (porStatus[status] || 0) + 1;
  }

  pilulas.innerHTML = "";
  for (const grupo of GRUPOS) {
    const quantos = porStatus[grupo.status];
    if (!quantos) continue;
    // Botão, e não enfeite: clicar leva ao primeiro item do grupo, que é o
    // gesto natural de quem viu "2 perdendo" e quer resolver.
    const pilula = document.createElement("button");
    pilula.type = "button";
    pilula.className = "estoque-pilula estoque-pilula-" + grupo.status;
    pilula.dataset.status = grupo.status;
    const ponto = document.createElement("span");
    ponto.className = "estoque-bolinha estoque-bolinha-" + grupo.status;
    ponto.setAttribute("aria-hidden", "true");
    pilula.appendChild(ponto);
    pilula.appendChild(document.createTextNode(quantos + " " + grupo.rotulo));
    pilulas.appendChild(pilula);
  }
}

function statusDoItem(item) {
  return classificarStatus(item, { naFila: filaDeValidacao.has(item.id) }).status;
}

// abrirPrimeiroDoGrupo é o gesto de quem viu "2 perdendo" e quer resolver: as
// pílulas do topo e as linhas do resumo levam ao primeiro item do grupo.
function abrirPrimeiroDoGrupo(status) {
  const alvo = loadEstoque().find((item) => statusDoItem(item) === status);
  if (alvo) selecionarItem(alvo.id);
}

// ---------------------------------------------------------------------------
// Resumo do estoque (nenhum item aberto)
// ---------------------------------------------------------------------------
//
// Com nenhum item aberto, o painel responde pelo estoque inteiro: quantos
// itens estão em cada situação, e o que dá para fazer em lote. Tudo sai do
// que já está guardado, sem requisição nenhuma.

// Por que um item está sem dados, na forma curta do resumo. A ordem dos testes
// é a de classificarStatus: sem validar não há mercado, e sem mercado o preço
// não tem com o que ser comparado.
function motivoSemDados(item) {
  if (item.validacao === VALIDACAO_INVALIDO) return "inválido";
  if (item.validacao !== VALIDACAO_OK) return "sem validar";
  if (!item.lastResult) return "sem consulta ao mercado";
  if (!item.lastResult.found) return "sem anúncios";
  return "sem preço";
}

// O "Validar agora" do resumo só pega o que validar resolve. Um item sem preço
// ou sem anúncios continuaria igual depois de consultar o site. Um inválido
// já teve as duas consultas respondendo que ele não existe: repeti-las custa
// duas requisições para ouvir o mesmo, e o que ele pede é o nome corrigido.
function validarResolve(item) {
  if (item.validacao === VALIDACAO_INVALIDO) return false;
  return item.validacao !== VALIDACAO_OK || !item.lastResult;
}

// detalharSemDados junta os motivos do grupo. Quando todos têm o mesmo, a
// contagem sairia repetida ("3 sem dados — 3 sem validar"), então vai só o
// motivo.
function detalharSemDados(itens) {
  const contagem = new Map();
  for (const item of itens) {
    const motivo = motivoSemDados(item);
    contagem.set(motivo, (contagem.get(motivo) || 0) + 1);
  }
  if (contagem.size === 1) return [...contagem.keys()][0];
  return [...contagem].map(([motivo, n]) => n + " " + motivo).join(" · ");
}

// As frases do resumo, por grupo. "Quem anunciou antes vende primeiro" ficou
// de fora do empate de propósito: é uma afirmação sobre a mecânica do jogo que
// nada nos dados permite verificar.
function textoDoGrupo(status, itens) {
  const n = itens.length;
  switch (status) {
    case STATUS_PERDENDO:
      return { texto: n + " perdendo a venda", detalhe: "" };
    case STATUS_EMPATADO:
      return { texto: n + (n === 1 ? " empatado" : " empatados") + " com o mais barato", detalhe: "" };
    case STATUS_NA_FRENTE:
      return { texto: n + " na frente", detalhe: "nada a fazer agora" };
    case STATUS_NA_FILA:
      return { texto: n + " na fila para validação", detalhe: "" };
    default:
      return { texto: n + " sem dados", detalhe: detalharSemDados(itens) };
  }
}

function buildLinhaDoResumo(status, itens) {
  const li = document.createElement("li");
  li.className = "estoque-resumo-grupo estoque-resumo-grupo-" + status;

  // A linha é um botão, e a ação do grupo é outro ao lado dele: um botão
  // dentro de outro não é HTML válido, e o leitor de tela não saberia qual dos
  // dois está sendo acionado.
  const abrir = document.createElement("button");
  abrir.type = "button";
  abrir.className = "estoque-resumo-abrir";
  abrir.dataset.status = status;
  abrir.title = "Abrir o primeiro item do grupo";

  const bolinha = document.createElement("span");
  bolinha.className = "estoque-bolinha estoque-bolinha-" + status;
  bolinha.setAttribute("aria-hidden", "true");
  abrir.appendChild(bolinha);

  const { texto, detalhe } = textoDoGrupo(status, itens);
  const textoEl = document.createElement("span");
  textoEl.className = "estoque-resumo-texto";
  textoEl.textContent = texto;
  abrir.appendChild(textoEl);
  if (detalhe) {
    const detalheEl = document.createElement("span");
    detalheEl.className = "estoque-resumo-detalhe";
    detalheEl.textContent = " — " + detalhe;
    abrir.appendChild(detalheEl);
  }
  li.appendChild(abrir);

  if (status === STATUS_PERDENDO) {
    const botao = document.createElement("button");
    botao.type = "button";
    botao.className = "estoque-acao estoque-resumo-reprecificar";
    botao.textContent = itens.length === 1 ? "Reprecificar" : "Reprecificar os " + itens.length;
    li.appendChild(botao);
  }

  if (status === STATUS_SEM_DADOS && itens.some(validarResolve)) {
    const botao = document.createElement("button");
    botao.type = "button";
    botao.className = "estoque-validar estoque-resumo-validar";
    botao.textContent = "Validar agora";
    li.appendChild(botao);
  }
  return li;
}

function buildResumoDoEstoque(lista) {
  const bloco = document.createElement("div");
  bloco.className = "estoque-resumo";

  const topo = document.createElement("div");
  topo.className = "estoque-painel-topo";
  const titulo = document.createElement("h3");
  titulo.className = "estoque-painel-nome";
  titulo.textContent = "Nenhum item selecionado";
  topo.appendChild(titulo);
  const contagem = document.createElement("span");
  contagem.className = "estoque-resumo-contagem";
  contagem.textContent = lista.length === 1 ? "1 item" : lista.length + " itens";
  topo.appendChild(contagem);
  bloco.appendChild(topo);

  if (loteDeReprecificacao) {
    bloco.appendChild(buildLoteDeReprecificacao(lista));
    return bloco;
  }

  const secao = document.createElement("h4");
  secao.className = "estoque-resumo-titulo";
  secao.textContent = "Resumo do estoque";
  bloco.appendChild(secao);

  const porStatus = new Map();
  for (const item of lista) {
    const status = statusDoItem(item);
    if (!porStatus.has(status)) porStatus.set(status, []);
    porStatus.get(status).push(item);
  }

  const grupos = document.createElement("ul");
  grupos.className = "estoque-resumo-grupos";
  for (const grupo of GRUPOS) {
    const itens = porStatus.get(grupo.status);
    if (itens) grupos.appendChild(buildLinhaDoResumo(grupo.status, itens));
  }
  bloco.appendChild(grupos);

  const dica = document.createElement("p");
  dica.className = "estoque-detalhe-vazio";
  dica.textContent = "Selecione um item à esquerda para ver mercado, histórico, estratégias e ressalvas.";
  bloco.appendChild(dica);
  return bloco;
}

// ---------------------------------------------------------------------------
// Reprecificar em lote
// ---------------------------------------------------------------------------

// O "Reprecificar os N" em andamento. O Reprecificar de um item grava o preço
// e o copia para a área de transferência, porque quem aplica o preço na loja
// é você, dentro do jogo. A área de transferência guarda um valor só, então N
// preços não cabem num clique. Gravar os N de uma vez faria o programa
// acreditar em N preços que você ainda não colou em lugar nenhum.
//
// Por isso o lote é uma lista de conferência: um clique por item, cada um
// copiando o seu preço, e a linha marcada quando é feita. Em memória, e não
// no localStorage: uma lista pela metade que voltasse depois de recarregar
// descreveria uma sessão de trabalho que já acabou. Reabrir é de graça, e os
// itens já reprecificados nem entram, porque deixaram de estar perdendo.
let loteDeReprecificacao = null;

function abrirLoteDeReprecificacao() {
  const ids = loadEstoque().filter((item) => statusDoItem(item) === STATUS_PERDENDO).map((item) => item.id);
  if (ids.length === 0) return;
  loteDeReprecificacao = { ids, feitos: new Map() };
  renderDetalhe();
}

function fecharLoteDeReprecificacao() {
  loteDeReprecificacao = null;
  renderDetalhe();
}

// O preço de cada linha é o da tarja do item (ver montarTarja), calculado na
// hora de desenhar e não congelado ao abrir a lista: o rodízio continua
// consultando, e um concorrente pode ter baixado o preço de novo nesse meio
// tempo.
function acaoDoItem(item) {
  const statusInfo = classificarStatus(item, { naFila: filaDeValidacao.has(item.id) });
  return montarTarja(item, calcularSugestao(item), statusInfo).acao;
}

function buildLoteDeReprecificacao(lista) {
  const bloco = document.createElement("div");
  bloco.className = "estoque-lote";

  const itens = loteDeReprecificacao.ids
    .map((id) => lista.find((e) => e.id === id))
    .filter(Boolean);
  const feitos = itens.filter((item) => loteDeReprecificacao.feitos.has(item.id)).length;

  const titulo = document.createElement("h4");
  titulo.className = "estoque-resumo-titulo";
  titulo.textContent = "Reprecificar " + (itens.length === 1 ? "1 item" : itens.length + " itens") +
    " · " + feitos + " de " + itens.length + (itens.length === 1 ? " feito" : " feitos");
  bloco.appendChild(titulo);

  const ajuda = document.createElement("p");
  ajuda.className = "estoque-lote-ajuda";
  ajuda.textContent =
    "Cada clique grava o preço novo e o copia para você colar na sua loja dentro do jogo. " +
    "Um item de cada vez: o jogo não recebe os preços daqui.";
  bloco.appendChild(ajuda);

  const ul = document.createElement("ul");
  ul.className = "estoque-lote-lista";
  for (const item of itens) {
    const li = document.createElement("li");
    li.className = "estoque-lote-linha";
    li.dataset.id = item.id;

    const nome = document.createElement("span");
    nome.className = "estoque-lote-nome";
    nome.textContent = nomeVisivel(item);
    li.appendChild(nome);

    const precos = document.createElement("span");
    precos.className = "estoque-lote-precos";
    li.appendChild(precos);

    const feito = loteDeReprecificacao.feitos.get(item.id);
    const acao = feito == null ? acaoDoItem(item) : null;
    if (feito != null) {
      li.classList.add("is-feita");
      precos.textContent = "✓ " + formatMoney(feito);
    } else if (acao && acao.preco != null) {
      precos.textContent = formatMoney(item.precoVenda) + " → " + formatMoney(acao.preco);
      const botao = document.createElement("button");
      botao.type = "button";
      botao.className = "estoque-acao estoque-lote-aplicar";
      botao.dataset.preco = String(acao.preco);
      botao.textContent = "Reprecificar";
      li.appendChild(botao);
    } else {
      // O mercado mudou desde que a lista abriu: o concorrente que passou na
      // sua frente saiu, ou subiu o preço.
      precos.textContent = "já não precisa";
    }
    ul.appendChild(li);
  }
  bloco.appendChild(ul);

  const rodape = document.createElement("div");
  rodape.className = "estoque-lote-rodape";
  const fechar = document.createElement("button");
  fechar.type = "button";
  fechar.className = "estoque-lote-fechar";
  fechar.textContent = "Voltar ao resumo";
  rodape.appendChild(fechar);
  bloco.appendChild(rodape);
  return bloco;
}

// ---------------------------------------------------------------------------
// Suspensão
// ---------------------------------------------------------------------------

// Os controles do estoque que consultam o site. Adicionar, editar o preço,
// pôr na loja, o sino e reprecificar são locais, e continuam livres enquanto
// o site limita as consultas.
const CONTROLES_QUE_CONSULTAM = [
  "#estoque-validar-tudo",
  "#estoque-detalhe .estoque-validar",
  "#estoque-detalhe .estoque-atualizar",
  "#estoque-detalhe .estoque-janela",
  "#estoque-detalhe .estoque-escolha-ok",
].join(", ");

// travarControlesDoEstoque é chamada pelo applySuspension (activity-bar.js)
// quando o estado muda. O painel, porém, é redesenhado o tempo todo, e cada
// redesenho traz os botões de volta habilitados. Por isso o render também
// passa por travarSeSuspenso, que lê o estado que o applySuspension deixou no
// <html>.
function travarControlesDoEstoque(suspenso) {
  for (const el of document.querySelectorAll(CONTROLES_QUE_CONSULTAM)) el.disabled = suspenso;
}

function siteSuspenso() {
  return document.documentElement.dataset.suspended === "1";
}

function travarSeSuspenso() {
  if (siteSuspenso()) travarControlesDoEstoque(true);
}

// renderDetalhe pinta o painel da direita: o item selecionado ou, sem
// seleção, o resumo do estoque inteiro.
function renderDetalhe() {
  const painel = document.getElementById("estoque-detalhe");
  if (!painel) return;

  const lista = loadEstoque();
  const id = itemSelecionado();
  const item = id ? lista.find((e) => e.id === id) : null;

  // Sem seleção, as pílulas do topo repetiriam o resumo que o painel já
  // mostra, uma ao lado da outra.
  const quadro = painel.closest(".estoque-quadro");
  if (quadro) quadro.classList.toggle("sem-selecao", !item);

  painel.innerHTML = "";
  if (item) {
    painel.appendChild(buildEstoqueCard(item));
  } else if (lista.length === 0) {
    const vazio = document.createElement("p");
    vazio.className = "estoque-detalhe-vazio";
    vazio.textContent = "Cadastre um item para começar.";
    painel.appendChild(vazio);
  } else {
    painel.appendChild(buildResumoDoEstoque(lista));
  }
  travarSeSuspenso();
}

// renderEstoque reconstrói a tabela a partir do localStorage. Não dispara
// nenhuma requisição: cada linha nasce do que já estava guardado.
function renderEstoque() {
  const container = document.getElementById("estoque-list");
  if (!container) return;
  container.innerHTML = "";

  const lista = loadEstoque();
  // Uma seleção que aponta para um item já removido tem que ser esquecida,
  // senão o painel fica preso num vazio que não corresponde a nada.
  if (itemSelecionado() && !lista.some((e) => e.id === itemSelecionado())) {
    gravarSelecao(null);
  }
  for (const item of lista) {
    container.appendChild(buildLinhaDoEstoque(item));
  }
  atualizarEstoqueVazio();
  renderResumoDoTopo();
  renderDetalhe();
  renderAvisoDeLoja();
}

// validarTudo enfileira a validação de todos os itens que ainda não têm dado
// de mercado.
//
// Em SÉRIE, um await de cada vez, e não em rajada: cada validação são uma ou
// duas requisições, e o programa inteiro se segura em uma por segundo. Sete
// itens já são até quatorze idas ao site — por isso o custo é dito antes, e
// por isso a bolinha muda para "na fila" enquanto a vez não chega.
async function validarTudo() {
  const pendentes = loadEstoque().filter(
    (item) => item.validacao !== VALIDACAO_OK || !item.lastResult,
  );
  if (pendentes.length === 0) {
    showToast("Todos os itens já estão validados.");
    return;
  }
  await validarEmLote(pendentes);
}

// validarEmLote é o miolo do "Validar tudo" e do "Validar agora" do resumo:
// o custo dito antes, a fila visível nas bolinhas, uma validação de cada vez.
async function validarEmLote(pendentes) {
  if (pendentes.length === 0) return;

  const minimo = pendentes.length * 2;
  const confirmado = window.confirm(
    "Validar " + pendentes.length + (pendentes.length === 1 ? " item" : " itens") +
      " vai consultar o site cerca de " + minimo + " vezes, uma por segundo — " +
      "aproximadamente " + minimo + " segundos.\n\n" +
      "O site limita consultas, e pedir demais de uma vez bloqueia todas por alguns minutos. Continuar?",
  );
  if (!confirmado) return;

  for (const item of pendentes) filaDeValidacao.add(item.id);
  renderEstoque();

  for (const item of pendentes) {
    // Só valida o que ainda existe: o usuário pode remover um item enquanto a
    // fila anda.
    if (!loadEstoque().some((e) => e.id === item.id)) {
      filaDeValidacao.delete(item.id);
      continue;
    }
    await validarItem(item.id);
    filaDeValidacao.delete(item.id);
    renderEstoque();
  }
}

// montarPainelDoEstoque liga a tela que acabou de entrar no DOM. Chamada no
// carregamento e de novo a cada troca de aba (ver religarCorpo em
// navegacao.js), porque o corpo da página é substituído inteiro e chega sem
// ouvintes. Faz só DOM — nenhuma requisição.
function montarPainelDoEstoque() {
  const container = document.getElementById("estoque-list");
  if (!container) return;

  renderEstoque();
  renderPersonagens();

  const formPersonagem = document.getElementById("estoque-personagem-form");
  if (formPersonagem) {
    formPersonagem.addEventListener("submit", (ev) => {
      ev.preventDefault();
      const campo = document.getElementById("estoque-personagem");
      if (!campo) return;
      if (adicionarPersonagem(campo.value)) {
        campo.value = "";
        campo.focus();
      }
    });
  }

  const listaPersonagens = document.getElementById("estoque-personagens-lista");
  if (listaPersonagens) {
    listaPersonagens.addEventListener("click", (ev) => {
      if (!ev.target.closest(".estoque-personagem-remover")) return;
      const li = ev.target.closest(".estoque-personagem");
      if (li) removerPersonagem(li.dataset.nome);
    });
  }

  const seletor = document.getElementById("estoque-servidor");
  if (seletor) {
    seletor.value = servidorDoEstoque();
    seletor.addEventListener("change", () => gravarServidorDoEstoque(seletor.value));
  }

  const form = document.getElementById("estoque-form");
  if (form) {
    form.addEventListener("submit", (ev) => {
      // O formulário existe para o Enter funcionar sozinho e para o campo ser
      // anunciado como tal; o envio de verdade nunca acontece — nada aqui vai
      // ao servidor nesta etapa.
      ev.preventDefault();
      const campo = document.getElementById("estoque-item");
      if (!campo) return;
      if (adicionarAoEstoque(campo.value)) {
        campo.value = "";
        campo.focus();
      }
    });
  }

  // Selecionar um item: clique ou teclado na linha da tabela.
  container.addEventListener("click", (ev) => {
    const linha = ev.target.closest(".estoque-linha");
    if (linha) selecionarItem(linha.dataset.id);
  });
  container.addEventListener("keydown", (ev) => {
    if (ev.key !== "Enter" && ev.key !== " ") return;
    const linha = ev.target.closest(".estoque-linha");
    if (!linha) return;
    ev.preventDefault();
    selecionarItem(linha.dataset.id);
  });

  const pilulas = document.getElementById("estoque-pilulas");
  if (pilulas) {
    pilulas.addEventListener("click", (ev) => {
      const pilula = ev.target.closest(".estoque-pilula");
      if (!pilula) return;
      abrirPrimeiroDoGrupo(pilula.dataset.status);
    });
  }

  const validarTudoBtn = document.getElementById("estoque-validar-tudo");
  if (validarTudoBtn) validarTudoBtn.addEventListener("click", validarTudo);

  // Delegação no painel: o card nasce e morre o tempo todo, e um ouvinte por
  // botão morreria junto com o card que o hospedava.
  const painel = document.getElementById("estoque-detalhe");
  if (!painel) return;

  painel.addEventListener("click", (ev) => {
    // --- resumo do estoque (nenhum item aberto) ---
    if (ev.target.closest(".estoque-resumo-reprecificar")) {
      abrirLoteDeReprecificacao();
      return;
    }
    if (ev.target.closest(".estoque-resumo-validar")) {
      validarEmLote(
        loadEstoque().filter((item) => statusDoItem(item) === STATUS_SEM_DADOS && validarResolve(item)),
      );
      return;
    }
    const aplicar = ev.target.closest(".estoque-lote-aplicar");
    if (aplicar) {
      const id = aplicar.closest(".estoque-lote-linha").dataset.id;
      const preco = Number(aplicar.dataset.preco);
      // Marca antes de aplicar: aplicarNovoPreco repinta o painel, e a linha
      // precisa já nascer marcada nessa repintura.
      loteDeReprecificacao.feitos.set(id, preco);
      aplicarNovoPreco(id, preco, aplicar);
      renderDetalhe();
      return;
    }
    if (ev.target.closest(".estoque-lote-fechar")) {
      fecharLoteDeReprecificacao();
      return;
    }
    const grupo = ev.target.closest(".estoque-resumo-abrir");
    if (grupo) {
      abrirPrimeiroDoGrupo(grupo.dataset.status);
      return;
    }

    // --- item aberto ---
    const card = ev.target.closest(".estoque-card");
    if (!card) return;
    const id = card.dataset.id;

    if (ev.target.closest(".estoque-voltar-resumo")) {
      selecionarItem(null);
      return;
    }

    if (ev.target.closest(".estoque-remover")) {
      removerDoEstoque(id);
      return;
    }

    const aba = ev.target.closest(".estoque-aba");
    if (aba) {
      gravarAba(aba.dataset.aba);
      const atual = loadEstoque().find((e) => e.id === id);
      if (atual) repintarCard(atual);
      return;
    }

    const reprecificar = ev.target.closest(".estoque-reprecificar");
    if (reprecificar) {
      aplicarNovoPreco(id, Number(reprecificar.dataset.preco), reprecificar);
      return;
    }

    if (ev.target.closest(".estoque-validar")) {
      validarItem(id);
      return;
    }

    if (ev.target.closest(".estoque-atualizar")) {
      // fresh: quem apertou quer o estado de agora, não o do cache.
      consultarMercado(id, true);
      return;
    }

    if (ev.target.closest(".estoque-escolha-ok")) {
      const atual = loadEstoque().find((e) => e.id === id);
      if (!atual || !Array.isArray(atual.candidatos)) return;
      const seletor = card.querySelector(".estoque-candidatos-select");
      if (!seletor) return;
      const escolhido = atual.candidatos.find((c) => String(c.itemId) === seletor.value);
      if (escolhido) escolherCandidato(id, escolhido);
      return;
    }

    if (ev.target.closest(".estoque-preco-venda")) {
      startEditingPrecoVenda(ev.target.closest(".estoque-preco-venda"), id);
      return;
    }

    const botaoLoja = ev.target.closest(".estoque-toggle-loja");
    if (botaoLoja) {
      const atual = loadEstoque().find((e) => e.id === id);
      if (!atual) return;
      const naLoja = !atual.naLoja;
      // Pôr na loja é entrar no rodízio, e o teto do rodízio é conjunto com a
      // watchlist (ver MONITOR_MAX_ITENS em monitor.js). A checagem vale até
      // para um item ainda não validado: ele entra no rodízio sozinho assim
      // que validar. Tirar da loja nunca é barrado.
      if (naLoja && !podeMonitorarMais()) {
        showToast(
          "Já são " + MONITOR_MAX_ITENS + " itens sendo vigiados entre a watchlist e o estoque. " +
            "Desligue algum para pôr este na loja.",
        );
        return;
      }
      // Sair da loja desliga o undercutting junto: deixá-lo ligado guardaria
      // uma intenção que não vale para nada e que voltaria a valer sozinha na
      // próxima vez que o item entrasse na loja, sem o usuário pedir.
      const mudancas = naLoja ? { naLoja } : { naLoja, undercut: false };
      const atualizado = updateEstoqueItem(id, comAvisoArmado(atual, mudancas));
      if (!atualizado) return;
      pintarToggle(botaoLoja, atualizado.naLoja, "Na loja", "Fora da loja");
      const botaoUndercut = card.querySelector(".estoque-sino");
      if (botaoUndercut) pintarSino(botaoUndercut, atualizado.undercut);
      aplicarDisponibilidadeDoUndercut(card, atualizado);
      renderAvisoDeLoja();
      return;
    }

    const botaoUndercut = ev.target.closest(".estoque-sino");
    if (botaoUndercut) {
      const atual = loadEstoque().find((e) => e.id === id);
      if (!atual || !atual.naLoja) return;
      const atualizado = updateEstoqueItem(id, comAvisoArmado(atual, { undercut: !atual.undercut }));
      if (!atualizado) return;
      pintarSino(botaoUndercut, atualizado.undercut);
    }
  });

  // Teclado: o preço de venda é um <span> clicável, então ele precisa
  // responder a Enter para quem navega com Tab — a watchlist faz igual com o
  // alvo e o refino.
  painel.addEventListener("keydown", (ev) => {
    if (ev.key !== "Enter") return;
    const span = ev.target.closest(".estoque-preco-venda");
    if (!span) return;
    const card = span.closest(".estoque-card");
    if (!card) return;
    ev.preventDefault();
    startEditingPrecoVenda(span, card.dataset.id);
  });

  painel.addEventListener("change", (ev) => {
    const seletorJanela = ev.target.closest(".estoque-janela");
    if (!seletorJanela) return;
    const card = seletorJanela.closest(".estoque-card");
    if (!card) return;
    const id = card.dataset.id;
    const item = updateEstoqueItem(id, { janela: seletorJanela.value });
    // Só consulta se já há o que consultar. Num item ainda não validado a
    // janela é só uma preferência guardada para depois.
    if (item && item.validacao === VALIDACAO_OK) consultarHistorico(id);
  });
}

document.addEventListener("DOMContentLoaded", montarPainelDoEstoque);
