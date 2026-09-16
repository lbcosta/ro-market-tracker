// Estoque da loja: a lista do que o usuário está vendendo, por quanto, e
// (nas etapas seguintes) o que o mercado está pagando por isso.
//
// Mesmo desenho da watchlist e pelos mesmos motivos: a lista vive inteira no
// localStorage do navegador — não há conta de usuário nem persistência no
// servidor —, a ordem do array É a ordem de exibição, e nenhuma ação de
// edição (preço, ligar/desligar, remover) fala com o servidor. Ver
// static/watchlist.js.
//
// Duas coisas falam com o servidor nesta etapa, e só sob clique do usuário:
// o botão "Validar" e a escolha de um candidato. Buscar preços de mercado,
// montar o histórico e avisar sobre undercutting entram depois.

const ESTOQUE_KEY = "ro-market-tracker:estoque";
const ESTOQUE_SERVIDOR_KEY = "ro-market-tracker:estoque-servidor";

// Os personagens do usuário. Lista separada, e não um campo por item: ela é
// do usuário, não de um item, e vale para o estoque inteiro. É por ela que o
// programa sabe quais anúncios do mercado são do próprio usuário — sem isso,
// você competiria consigo mesmo.
const PERSONAGENS_KEY = "ro-market-tracker:meus-personagens";

const ESTOQUE_SERVIDOR_PADRAO = "NIDHOGG";

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
    historico: null,
    notified: false,
  };

  const list = loadEstoque();
  list.push(item);
  saveEstoque(list);

  const container = document.getElementById("estoque-list");
  if (container) container.appendChild(buildEstoqueCard(item));
  atualizarEstoqueVazio();
  return item;
}

function removerDoEstoque(id) {
  saveEstoque(loadEstoque().filter((e) => e.id !== id));
  const card = findEstoqueCard(id);
  if (card) card.remove();
  atualizarEstoqueVazio();
  renderAvisoDeLoja();
}

function findEstoqueCard(id) {
  return document.querySelector('.estoque-card[data-id="' + cssEscape(id) + '"]');
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
    // notified volta a false: o preço que se compara com o mercado mudou,
    // então o aviso de undercutting precisa poder disparar de novo.
    const atualizado = updateEstoqueItem(id, { precoVenda, notified: false }) || item;
    span.textContent = precoVendaLabel(atualizado.precoVenda);
    // Repintar o card inteiro, e não só este texto: o preço é insumo da fila,
    // do tempo até vender e dos cenários (ver calcularSugestao). Sem isto, o
    // card continuaria mostrando a conta do preço anterior.
    repintarCard(atualizado);
  };

  input.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter") {
      confirmar();
    } else if (ev.key === "Escape") {
      confirmado = true;
      span.textContent = precoVendaLabel(item.precoVenda);
    }
  });

  input.addEventListener("blur", () => {
    if (!confirmado) span.textContent = precoVendaLabel(item.precoVenda);
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
    repintarCard(updateEstoqueItem(id, {
      lastResult: { found: false, listings: [] },
      lastCheckedAt: Date.now(),
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
function repintarCard(item) {
  if (!item) return;
  const antigo = findEstoqueCard(item.id);
  if (!antigo) return;
  antigo.replaceWith(buildEstoqueCard(item));
  // O aviso de loja é sobre o conjunto, não sobre um card: qualquer card que
  // muda pode ter sido o último que ainda tinha anúncio seu (ou o primeiro a
  // voltar a ter).
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

// consultarMercado busca os anúncios do item agora. Só é chamada sob clique:
// ao escolher o item na validação e no botão "↻" do card. O rodízio
// automático entra na etapa do undercutting.
//
// Grava o resultado SEMPRE, inclusive quando o card não está na tela — pelo
// mesmo motivo que fetchLivePrice (ver monitor.js): o que é vigiado não pode
// depender de qual aba está aberta.
async function consultarMercado(id, fresh = false) {
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

    const mudancas = { lastResult: data, lastCheckedAt: Date.now(), notified: false };
    // Um item validado pelo histórico não sabia o sufixo de slots; a primeira
    // consulta de mercado corrige o nome sem custar requisição nenhuma.
    if (data.displayName && data.displayName !== item.itemName) {
      mudancas.itemName = data.displayName;
    }
    repintarCard(updateEstoqueItem(id, mudancas));
  } catch (err) {
    showToast(String(err.message || err).trim() || "Não foi possível consultar o mercado agora.");
    const depois = botao();
    if (depois) depois.disabled = false;
  }
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
    if (seletorDepois) seletorDepois.disabled = false;
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

  const resumo = document.createElement("span");
  resumo.className = "estoque-historico-resumo";

  if (!dados.days || dados.days.length === 0) {
    resumo.textContent = "Sem vendas registradas " + textoDaJanela(dados.window) + ".";
    bloco.appendChild(resumo);
    return bloco;
  }

  const s = dados.summary;
  resumo.textContent =
    "Vendido entre " + formatMoney(s.min) + " e " + formatMoney(s.max) +
    " · média " + formatMoney(Math.round(s.weightedAvg)) +
    " · " + s.qtySold + (s.qtySold === 1 ? " unidade" : " unidades");
  bloco.appendChild(resumo);

  // A tendência entra na mesma linha, e só quando existe: uma seta para toda
  // variação de 1% seria ruído com aparência de sinal (ver calcularTendencia).
  const tendencia = calcularTendencia(dados);
  if (tendencia) {
    const seta = document.createElement("span");
    seta.className = "estoque-tendencia " + (tendencia.subindo ? "is-subindo" : "is-caindo");
    seta.textContent =
      (tendencia.subindo ? " ↗ " : " ↘ ") +
      Math.abs(Math.round(tendencia.variacao * 100)) + "% vs. o período anterior";
    resumo.appendChild(seta);
  }

  const detalhe = document.createElement("details");
  detalhe.className = "estoque-historico-dias";

  const sumario = document.createElement("summary");
  // Quantos dias vieram e quantos existem: a diferença é o que diz ao usuário
  // que trocar para uma janela maior tem o que mostrar.
  const quantos = dados.days.length === 1 ? "1 dia com venda" : dados.days.length + " dias com venda";
  sumario.textContent =
    quantos + (dados.daysAvailable > dados.days.length ? " de " + dados.daysAvailable + " registrados" : "");
  detalhe.appendChild(sumario);

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
// Preço sugerido
// ---------------------------------------------------------------------------

const ROTULO_CONFIANCA = {
  alta: "confiança alta",
  media: "confiança média",
  baixa: "confiança baixa",
  nenhuma: "sem dados suficientes",
};

// buildBlocoDeSugestao desenha a faixa sugerida numa linha e guarda os
// cenários dentro de um <details>.
//
// Nada de tooltip: os cards dividem uma grade e precisam de altura previsível,
// mas a explicação de cada estratégia é longa demais para um atributo title —
// que ainda por cima demora meio segundo para aparecer e não existe no toque.
// Recolhido, cabe texto de verdade e o card não cresce.
function buildBlocoDeSugestao(item) {
  const bloco = document.createElement("div");
  bloco.className = "estoque-sugestao";

  // Sem histórico ainda não há o que calcular.
  if (!item.historico) return bloco;

  const sugestao = calcularSugestao(item);

  const linha = document.createElement("span");
  linha.className = "estoque-sugestao-faixa";
  if (sugestao.faixa) {
    linha.textContent =
      "Sugerido: " + formatMoney(sugestao.faixa.min) + " – " + formatMoney(sugestao.faixa.max);
  } else {
    linha.textContent = "Sem preço sugerido";
  }

  const selo = document.createElement("span");
  selo.className = "estoque-confianca estoque-confianca-" + sugestao.confianca.nivel;
  selo.textContent = ROTULO_CONFIANCA[sugestao.confianca.nivel];
  linha.appendChild(document.createTextNode(" · "));
  linha.appendChild(selo);
  bloco.appendChild(linha);

  // O motivo da confiança é o que transforma um selo opaco em informação: é
  // ele que conta ao usuário QUE o histórico está somando coisas diferentes.
  if (sugestao.confianca.motivo) {
    const motivo = document.createElement("p");
    motivo.className = "estoque-confianca-motivo";
    motivo.textContent = sugestao.confianca.motivo;
    bloco.appendChild(motivo);
  }

  // O mercado partido em faixas, quando os próprios anúncios revelam isso.
  if (sugestao.faixas) {
    const partido = document.createElement("p");
    partido.className = "estoque-faixas";
    partido.textContent =
      "O mercado está partido: " + sugestao.faixas.baixa.length + " anúncio(s) até " +
      formatMoney(sugestao.faixas.baixa[sugestao.faixas.baixa.length - 1].price) +
      " e " + sugestao.faixas.alta.length + " a partir de " +
      formatMoney(sugestao.faixas.alta[0].price) + ".";
    bloco.appendChild(partido);
  }

  if (item.precoVenda != null && sugestao.tempoNoSeuPreco) {
    const seu = document.createElement("span");
    seu.className = "estoque-sugestao-seu";
    seu.textContent = "No seu preço: " + sugestao.tempoNoSeuPreco;
    bloco.appendChild(seu);
  }

  if (sugestao.cenarios.length > 0) {
    bloco.appendChild(buildCenarios(sugestao.cenarios));
  }

  return bloco;
}

function buildCenarios(cenarios) {
  const detalhe = document.createElement("details");
  detalhe.className = "estoque-cenarios";

  const sumario = document.createElement("summary");
  sumario.textContent = "Estratégias de preço";
  detalhe.appendChild(sumario);

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

    detalhe.appendChild(item);
  }

  return detalhe;
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
function pintarToggle(botao, ligado, rotuloLigado, rotuloDesligado) {
  botao.setAttribute("aria-pressed", String(ligado));
  botao.classList.toggle("is-on", ligado);
  botao.textContent = ligado ? rotuloLigado : rotuloDesligado;
}

// O undercutting só faz sentido para um item que está na loja: não há o que
// comparar com o mercado se você não está vendendo. Desabilitar (em vez de
// esconder) mantém o card estável e mostra que a opção existe.
function aplicarDisponibilidadeDoUndercut(card, item) {
  const botao = card.querySelector(".estoque-toggle-undercut");
  if (!botao) return;
  botao.disabled = !item.naLoja;
  botao.title = item.naLoja
    ? "Avisar quando alguém estiver vendendo mais barato que você"
    : "Disponível apenas para itens que estão na loja";
}

function buildEstoqueCard(item) {
  const li = document.createElement("li");
  li.className = "estoque-card";
  li.dataset.id = item.id;

  const topo = document.createElement("div");
  topo.className = "estoque-card-topo";

  const nome = document.createElement("span");
  nome.className = "estoque-nome";
  nome.textContent = nomeVisivel(item);
  topo.appendChild(nome);

  const status = document.createElement("span");
  pintarStatus(status, item.validacao);
  topo.appendChild(status);

  const remover = document.createElement("button");
  remover.type = "button";
  remover.className = "estoque-remover";
  remover.textContent = "×";
  remover.setAttribute("aria-label", "Remover " + nomeVisivel(item) + " do estoque");
  remover.title = "Remover do estoque";
  topo.appendChild(remover);

  li.appendChild(topo);

  const precos = document.createElement("div");
  precos.className = "estoque-precos";

  const precoVenda = document.createElement("span");
  precoVenda.className = "estoque-preco-venda";
  precoVenda.textContent = precoVendaLabel(item.precoVenda);
  precoVenda.tabIndex = 0;
  precoVenda.title = "Clique para editar o preço de venda";
  precos.appendChild(precoVenda);

  li.appendChild(precos);

  const flags = document.createElement("div");
  flags.className = "estoque-flags";

  const loja = document.createElement("button");
  loja.type = "button";
  loja.className = "estoque-toggle estoque-toggle-loja";
  pintarToggle(loja, item.naLoja, "Na loja", "Fora da loja");
  flags.appendChild(loja);

  const undercut = document.createElement("button");
  undercut.type = "button";
  undercut.className = "estoque-toggle estoque-toggle-undercut";
  pintarToggle(undercut, item.undercut, "Undercutting ligado", "Undercutting desligado");
  flags.appendChild(undercut);

  li.appendChild(flags);

  const acoes = document.createElement("div");
  acoes.className = "estoque-acoes";

  // O seletor de janela é por item, e não da tela inteira: trocá-lo custa uma
  // consulta ao site, e cobrar isso de todos os itens de uma vez estouraria o
  // ritmo de uma requisição por minuto que o programa inteiro respeita.
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
  acoes.appendChild(janela);

  // Só faz sentido depois de o item ser validado: sem itemId não há o que
  // consultar.
  if (item.validacao === VALIDACAO_OK) {
    const atualizar = document.createElement("button");
    atualizar.type = "button";
    atualizar.className = "estoque-atualizar";
    atualizar.textContent = "↻";
    atualizar.title = "Consultar o mercado agora";
    atualizar.setAttribute("aria-label", "Consultar o mercado agora para " + nomeVisivel(item));
    acoes.appendChild(atualizar);

    const quando = document.createElement("span");
    quando.className = "estoque-atualizado-em";
    paintUpdatedAt(quando, item.lastCheckedAt);
    acoes.appendChild(quando);
  }

  const validar = document.createElement("button");
  validar.type = "button";
  validar.className = "estoque-validar";
  // "Tentar de novo" num item inválido: revalidar custa uma requisição (zero,
  // se for dentro do cache de 30s), então não faz sentido obrigar a apagar e
  // recadastrar quando o site apenas estava fora do ar na primeira tentativa.
  validar.textContent = item.validacao === VALIDACAO_INVALIDO ? "Tentar de novo" : "Validar";
  acoes.appendChild(validar);

  li.appendChild(acoes);

  if (item.validacao === VALIDACAO_OK) {
    li.appendChild(buildBlocoDeMercado(item));
    li.appendChild(buildBlocoDeHistorico(item));
    li.appendChild(buildBlocoDeSugestao(item));
  }

  // O motivo só existe no estado inválido: o selo vermelho chama a atenção, e
  // esta linha é quem diz o que aconteceu e o que fazer.
  if (item.validacao === VALIDACAO_INVALIDO && item.motivo) {
    const motivo = document.createElement("p");
    motivo.className = "estoque-motivo";
    motivo.textContent = item.motivo;
    li.appendChild(motivo);
  }

  // A escolha fica DENTRO do card, e não num diálogo: ela é sobre este item, e
  // quem está cadastrando vários seguidos não deve ser interrompido por uma
  // janela modal a cada nome ambíguo.
  if (Array.isArray(item.candidatos) && item.candidatos.length > 0) {
    li.appendChild(buildEscolhaDeCandidato(item));
  }

  aplicarDisponibilidadeDoUndercut(li, item);
  return li;
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
function mudarPersonagens(lista) {
  salvarPersonagens(lista);
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
      item.lastResult &&
      item.lastCheckedAt != null &&
      item.lastCheckedAt >= limite,
  );
  if (checados.length === 0) return null;

  let comAnuncioSeu = 0;
  let maisAntiga = Infinity;
  let maisRecente = 0;
  for (const item of checados) {
    if (separarAnuncios(item.lastResult).meus.length > 0) comAnuncioSeu++;
    maisAntiga = Math.min(maisAntiga, item.lastCheckedAt);
    maisRecente = Math.max(maisRecente, item.lastCheckedAt);
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
function renderEstoque() {
  const container = document.getElementById("estoque-list");
  if (!container) return;
  container.innerHTML = "";
  for (const item of loadEstoque()) {
    container.appendChild(buildEstoqueCard(item));
  }
  atualizarEstoqueVazio();
  renderAvisoDeLoja();
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

  // Delegação no container: os cards nascem e morrem o tempo todo, e um
  // ouvinte por botão morreria junto com o card que o hospedava.
  container.addEventListener("click", (ev) => {
    const card = ev.target.closest(".estoque-card");
    if (!card) return;
    const id = card.dataset.id;

    if (ev.target.closest(".estoque-remover")) {
      removerDoEstoque(id);
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
      // Sair da loja desliga o undercutting junto: deixá-lo ligado guardaria
      // uma intenção que não vale para nada e que voltaria a valer sozinha na
      // próxima vez que o item entrasse na loja, sem o usuário pedir.
      const naLoja = !atual.naLoja;
      const mudancas = naLoja ? { naLoja, notified: false } : { naLoja, undercut: false, notified: false };
      const atualizado = updateEstoqueItem(id, mudancas);
      if (!atualizado) return;
      pintarToggle(botaoLoja, atualizado.naLoja, "Na loja", "Fora da loja");
      const botaoUndercut = card.querySelector(".estoque-toggle-undercut");
      if (botaoUndercut) {
        pintarToggle(botaoUndercut, atualizado.undercut, "Undercutting ligado", "Undercutting desligado");
      }
      aplicarDisponibilidadeDoUndercut(card, atualizado);
      renderAvisoDeLoja();
      return;
    }

    const botaoUndercut = ev.target.closest(".estoque-toggle-undercut");
    if (botaoUndercut) {
      const atual = loadEstoque().find((e) => e.id === id);
      if (!atual || !atual.naLoja) return;
      const atualizado = updateEstoqueItem(id, { undercut: !atual.undercut, notified: false });
      if (!atualizado) return;
      pintarToggle(botaoUndercut, atualizado.undercut, "Undercutting ligado", "Undercutting desligado");
    }
  });

  // Teclado: o preço de venda é um <span> clicável, então ele precisa
  // responder a Enter para quem navega com Tab — a watchlist faz igual com o
  // alvo e o refino.
  container.addEventListener("keydown", (ev) => {
    if (ev.key !== "Enter") return;
    const span = ev.target.closest(".estoque-preco-venda");
    if (!span) return;
    const card = span.closest(".estoque-card");
    if (!card) return;
    ev.preventDefault();
    startEditingPrecoVenda(span, card.dataset.id);
  });

  container.addEventListener("change", (ev) => {
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
