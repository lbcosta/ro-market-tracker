// Watchlist do RO Market Tracker.
//
// A lista em si (quais itens, preço alvo, ligado/desligado, e o último
// resultado conhecido de cada um) vive inteira no navegador, em
// localStorage — não há conta de usuário nem persistência no servidor. O
// servidor só é consultado para o dado que o navegador não tem como
// calcular sozinho: o menor preço anunciado agora e o refino da loja mais
// barata (GET /web/watchlist/price), respeitando o rate limiting já
// aplicado no client Go.
//
// Monitoramento: a cada MONITOR_TICK_MS, UM item entre os com
// monitoring=true tem o preço reconsultado — o escolhido é sempre o que está
// há mais tempo sem consulta (ver pickNextEntry), revezando entre todos ao
// longo do tempo em vez de despachar a lista inteira de uma vez. Quando a
// condição que o item acompanha passa a valer, o usuário é avisado (toast +
// notificação do SO) e a linha é destacada. Só existe uma notificação por
// "cruzamento" da condição — enquanto ela continuar valendo, não notifica de
// novo a cada checagem; só volta a notificar se ela deixar de valer e voltar
// a valer depois (ver campo "notified" da entrada, persistido).
//
// Ao carregar a página, cada linha nasce já com o último resultado conhecido
// (campo "lastResult" da entrada, persistido) — sem nenhuma requisição —, e
// só o item escolhido pelo rodízio acima é consultado de verdade, na hora
// (ver DOMContentLoaded).
const WATCHLIST_KEY = "ro-market-tracker:watchlist";

// MONITOR_TICK_MS é o intervalo entre uma consulta automática e a seguinte —
// sempre UMA só, nunca a lista inteira de uma vez (ver pickNextEntry e
// runMonitoringTick). Isto troca o antigo ciclo de 5 minutos, que despachava
// todos os itens monitorados em série e em rajada, por um ritmo constante:
// não importa quantos itens a watchlist tenha, o navegador nunca pede mais
// que uma consulta por minuto.
const MONITOR_TICK_MS = 60 * 1000;

// WATCHLIST_MAX_ITEMS é o teto de itens que a watchlist aceita.
//
// O ritmo automático é sempre de UMA consulta por minuto, revezando entre os
// itens monitorados — então o intervalo entre duas consultas do MESMO item
// cresce com o tamanho da lista (aproximadamente N × MONITOR_TICK_MS). Sem
// teto, uma watchlist muito grande faria cada item demorar cada vez mais
// para ser reconsultado; 50 itens já significa quase uma hora entre uma
// consulta e a seguinte do mesmo item — grande o bastante para acompanhar
// listas razoáveis sem o intervalo virar impraticável.
const WATCHLIST_MAX_ITEMS = 50;

// Uma entrada da watchlist acompanha uma de duas condições, conforme de onde
// ela foi adicionada:
//
//   MODE_PRICE        o item está à venda e o que se espera é um PREÇO. É o
//                     modo do botão na tabela de resultados da busca: a linha
//                     mostra "Alvo: X" (editável) e "Atual: Y", e o aviso
//                     dispara quando o menor preço chega ao alvo.
//   MODE_AVAILABILITY o item não está anunciado por ninguém, então não há
//                     preço a esperar — o que se espera é o item VOLTAR ao
//                     mercado. É o modo do botão na tabela de histórico (ver
//                     history.html.tmpl): a linha mostra "Nenhum anúncio" e o
//                     aviso dispara no primeiro anúncio que aparecer, seja
//                     qual for o preço.
//
// Entradas gravadas antes desta distinção existir não têm o campo "mode";
// entryMode as trata como MODE_PRICE, que era o único comportamento.
const MODE_PRICE = "price";
const MODE_AVAILABILITY = "availability";

function entryMode(entry) {
  return entry.mode === MODE_AVAILABILITY ? MODE_AVAILABILITY : MODE_PRICE;
}

function isAvailabilityWatch(entry) {
  return entryMode(entry) === MODE_AVAILABILITY;
}

// entrySearchName é o termo que o servidor manda ao GnJoy para achar o item.
// Não é o nome exibido: a busca do site casa contra o nome do item SEM o
// sufixo de slots, então procurar por "Selo de Loki [1]" — que é o que a
// linha mostra — não acharia anúncio nenhum. Entradas gravadas antes desta
// distinção não têm searchName, e para elas os dois nomes coincidem.
function entrySearchName(entry) {
  return entry.searchName || entry.itemName;
}

// BONUS_FILTER_SLOTS é quantos bônus aleatórios uma linha pode exigir.
//
// O site expõe quatro por anúncio (randomOpt1..4), mas na prática só os dois
// primeiros aparecem preenchidos. Dois campos cobrem o que existe sem
// carregar a linha — e, como o filtro é "os pedidos têm de estar presentes",
// pedir dois de uma unidade que tem quatro continua encontrando ela: é um
// filtro mais frouxo, nunca um que exclua o anúncio de origem.
const BONUS_FILTER_SLOTS = 2;

// entryBonusSlots devolve os filtros de bônus da entrada SEMPRE com
// BONUS_FILTER_SLOTS posições, preenchendo com "" o que faltar — é o que a
// linha renderiza, um campo por posição.
//
// Posição fixa, e não lista compacta: com uma lista, limpar o primeiro campo
// faria o bônus do segundo pular para o lugar dele, debaixo do cursor de quem
// estava editando. Entradas gravadas antes dos bônus existirem não têm o
// campo, e caem no caso "tudo vazio" sem precisar de migração.
function entryBonusSlots(entry) {
  const slots = Array.isArray(entry.bonusFilters) ? entry.bonusFilters : [];
  return Array.from({ length: BONUS_FILTER_SLOTS }, (_, i) => (slots[i] || "").trim());
}

// entryBonusFilters são só os bônus de fato exigidos — é o que vai para a
// URL da consulta de preço. Um campo em branco não é filtro nenhum.
function entryBonusFilters(entry) {
  return entryBonusSlots(entry).filter((b) => b !== "");
}

// lastKnownPrice guarda, em memória (não persistido), o último preço mínimo
// visto por item — usado para reavaliar o status de "alvo atingido" na hora
// (sem esperar a próxima consulta) quando o usuário edita o preço alvo.
const lastKnownPrice = new Map();

// nextMonitorRunAt (em memória, não persistido) é o timestamp da próxima
// checagem automática — usado só para renderizar o cronômetro regressivo do
// painel. monitorTimerId guarda o setTimeout atual para poder cancelá-lo ao
// forçar uma atualização manual.
let nextMonitorRunAt = null;
let monitorTimerId = null;

function loadWatchlist() {
  try {
    const raw = localStorage.getItem(WATCHLIST_KEY);
    return raw ? JSON.parse(raw) : [];
  } catch {
    return [];
  }
}

function saveWatchlist(list) {
  localStorage.setItem(WATCHLIST_KEY, JSON.stringify(list));
}

// updateEntry aplica "changes" à entrada com o id informado e persiste.
// Devolve a entrada já atualizada, ou null se ela não existir mais (por
// exemplo, foi removida entre a leitura e a gravação).
function updateEntry(id, changes) {
  const list = loadWatchlist();
  const idx = list.findIndex((e) => e.id === id);
  if (idx === -1) return null;
  list[idx] = Object.assign({}, list[idx], changes);
  saveWatchlist(list);
  return list[idx];
}

// O id identifica a LINHA da watchlist, e é o que impede o mesmo clique de
// criar duas. Quando a busca separa as seções por refino, cada seção acompanha
// uma unidade diferente do mesmo item — a "+7" e a "+10" da mesma espada são
// duas linhas legítimas —, então o refino entra no id.
//
// Os bônus entram pelo mesmo motivo: com a busca separada por bônus, as
// unidades "CRIT +4" e "CRIT +5" do mesmo item são duas seções, e clicar no
// "+ Watchlist" das duas tem de dar duas linhas — sem isso, a segunda seria
// descartada como duplicata.
//
// Sem refino nem bônus fixados o id continua "server:itemId": as entradas
// gravadas antes desta mudança seguem válidas, sem migração.
//
// É opaco e não muda depois de criado: editar o refino ou os bônus pela linha
// altera os filtros, não o id.
function watchlistId(server, itemId, refineFilter, bonusFilters) {
  let id = server + ":" + itemId;
  if (refineFilter != null) id += ":+" + refineFilter;

  // Ordenado antes de virar chave, como o bonusKey do servidor faz: a mesma
  // combinação em ordens diferentes é a mesma linha.
  const bonus = (bonusFilters || []).map((b) => (b || "").trim()).filter((b) => b !== "");
  if (bonus.length > 0) id += ":ba" + hashKey(bonus.sort().join(""));
  return id;
}

// hashKey resume um texto em algo curto e estável (FNV-1a de 32 bits, em
// base36). Os bônus entram no id como hash, e não como as frases inteiras,
// porque o id vai parar num atributo data-id e volta por seletor CSS —
// carregar frases com aspas, barras e acentos até lá seria fragilidade sem
// ganho nenhum, já que o id é opaco de propósito.
function hashKey(text) {
  let hash = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) {
    hash ^= text.charCodeAt(i);
    // Multiplicação de 32 bits sem estourar para ponto flutuante.
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return hash.toString(36);
}

// parseRefineData lê o data-refine que o cabeçalho da seção emite quando a
// busca separou os anúncios por refino. Ausente (a busca não verificou) é
// diferente de "+0" (verificou, e a unidade não tem refino).
function parseRefineData(raw) {
  if (raw == null || raw === "") return null;
  const parsed = Math.round(Number(raw));
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : null;
}

// parseBonusData lê o data-bonus (JSON) que o cabeçalho da seção emite quando
// a busca separou os anúncios por bônus. Trunca em BONUS_FILTER_SLOTS: a
// linha só tem campo para esses, e guardar filtros que não aparecem em lugar
// nenhum deixaria a busca apertada por um motivo invisível.
function parseBonusData(raw) {
  if (!raw) return [];
  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return [];
  }
  if (!Array.isArray(parsed)) return [];
  return parsed
    .filter((b) => typeof b === "string")
    .map((b) => b.trim())
    .filter((b) => b !== "")
    .slice(0, BONUS_FILTER_SLOTS);
}

function cssEscape(value) {
  return window.CSS && CSS.escape ? CSS.escape(String(value)) : String(value).replace(/["\\]/g, "\\$&");
}

function findRow(id) {
  return document.querySelector('.watchlist-row[data-id="' + cssEscape(id) + '"]');
}

function updateEmptyState() {
  const empty = document.getElementById("watchlist-empty");
  if (empty) empty.hidden = loadWatchlist().length > 0;
}

function formatMoney(n) {
  return n.toLocaleString("pt-BR") + " z";
}

function targetLabel(targetPrice) {
  return "Alvo: " + (targetPrice != null ? formatMoney(targetPrice) : "—");
}

// addToWatchlist é chamado pelo botão "+ Watchlist" das duas tabelas: a de
// resultados da busca (results.html.tmpl) e a de histórico de um item fora do
// mercado (history.html.tmpl). É o data-mode do botão que diz qual condição a
// entrada vai acompanhar — ver MODE_PRICE e MODE_AVAILABILITY. A entrada é
// identificada por server+itemId+refino, então duplicar o clique não duplica a
// entrada, mas duas seções do mesmo item em refinos diferentes viram duas.
function addToWatchlist(button) {
  const server = button.dataset.server;
  const itemId = button.dataset.itemId;
  const itemName = button.dataset.itemName;
  if (!server || !itemId || !itemName) return;

  // Já vêm fixados quando a seção representa um refino só / uma combinação de
  // bônus só: a linha nasce acompanhando aquela unidade, e não "a mais barata
  // de qualquer refino ou bônus".
  const refineFilter = parseRefineData(button.dataset.refine);
  const bonusFilters = parseBonusData(button.dataset.bonus);

  const id = watchlistId(server, itemId, refineFilter, bonusFilters);
  const list = loadWatchlist();
  if (list.some((entry) => entry.id === id)) return;

  // O teto existe para a watchlist nunca virar, sozinha, o consumo dominante
  // da cota de consultas ao site (ver WATCHLIST_MAX_ITEMS) — e precisa de um
  // aviso, não um clique silenciosamente ignorado: ao contrário do duplicado
  // acima, aqui não há nenhuma linha já na tela que explique por que nada
  // aconteceu.
  if (list.length >= WATCHLIST_MAX_ITEMS) {
    showToast("A watchlist já tem o máximo de " + WATCHLIST_MAX_ITEMS + " itens. Remova algum para adicionar outro.");
    return;
  }

  const entry = {
    id,
    server,
    itemId: Number(itemId),
    itemName,
    searchName: button.dataset.searchName || itemName,
    mode: button.dataset.mode === MODE_AVAILABILITY ? MODE_AVAILABILITY : MODE_PRICE,
    targetPrice: null,
    refineFilter,
    bonusFilters,
    monitoring: true,
    notified: false,
  };
  list.push(entry);
  saveWatchlist(list);

  const container = document.getElementById("watchlist-list");
  if (!container) return;
  container.appendChild(buildWatchlistRow(entry));
  updateEmptyState();
  fetchLivePrice(entry);
}

function removeFromWatchlist(id) {
  saveWatchlist(loadWatchlist().filter((entry) => entry.id !== id));
  lastKnownPrice.delete(id);
  const row = findRow(id);
  if (row) row.remove();
  updateEmptyState();
}

function toggleMonitoring(id) {
  const entry = updateEntry(id, {});
  if (!entry) return;
  const updated = updateEntry(id, { monitoring: !entry.monitoring });
  if (!updated) return;

  const row = findRow(id);
  if (!row) return;
  const light = row.querySelector(".status-light");
  const toggle = row.querySelector(".status-toggle");
  light.classList.toggle("on", updated.monitoring);
  light.classList.toggle("off", !updated.monitoring);
  const label = updated.monitoring ? "Desativar monitoramento" : "Ativar monitoramento";
  toggle.setAttribute("aria-label", label);
  toggle.title = updated.monitoring
    ? "Monitorando (clique para desativar)"
    : "Não monitorando (clique para ativar)";
}

// startEditingTarget troca o texto "Alvo: ..." por um <input> numérico.
// Enter confirma e persiste; Escape ou perder o foco sem confirmar descarta
// a edição e volta ao valor anterior.
function startEditingTarget(span, id) {
  if (span.querySelector("input")) return;
  const list = loadWatchlist();
  const entry = list.find((e) => e.id === id);
  if (!entry) return;

  const input = document.createElement("input");
  input.type = "number";
  input.min = "0";
  input.step = "1";
  input.className = "watchlist-target-input";
  input.value = entry.targetPrice != null ? String(entry.targetPrice) : "";
  input.setAttribute("aria-label", "Preço alvo de " + entry.itemName);

  span.textContent = "";
  span.appendChild(input);
  input.focus();
  input.select();

  let confirmed = false;

  const confirmEdit = () => {
    confirmed = true;
    const raw = input.value.trim();
    let targetPrice = null;
    if (raw !== "") {
      const parsed = Math.round(Number(raw));
      if (Number.isFinite(parsed) && parsed >= 0) targetPrice = parsed;
    }
    const updated = updateEntry(id, { targetPrice, notified: false }) || entry;
    span.textContent = targetLabel(updated.targetPrice);

    const row = findRow(id);
    if (row) {
      const naviCommand = updated.lastResult ? updated.lastResult.naviCommand : null;
      updateHitState(row, updated, lastKnownPrice.has(id) ? lastKnownPrice.get(id) : null, naviCommand);
    }
  };

  input.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter") {
      confirmEdit();
    } else if (ev.key === "Escape") {
      confirmed = true;
      span.textContent = targetLabel(entry.targetPrice);
    }
  });

  input.addEventListener("blur", () => {
    if (!confirmed) span.textContent = targetLabel(entry.targetPrice);
  });
}

// refineFilterLabel é como o refino EXIGIDO aparece no badge. A seta é o que
// distingue os dois significados que o mesmo badge tem: sem filtro ele mostra
// "+N", o refino ao vivo da loja mais barata; com filtro, "+N↑", o piso que o
// usuário pediu (ver watchlistFilter no backend — o filtro é um mínimo).
function refineFilterLabel(refineFilter) {
  return "+" + refineFilter + "↑";
}

// refineSuffix é o refino real do anúncio encontrado, para acompanhar o preço
// quando o badge está ocupado mostrando o piso exigido. Vazio quando não há
// exigência (o badge já mostra esse mesmo número) ou quando o servidor não
// conseguiu o detalhe da loja de onde ele sai.
function refineSuffix(entry, data) {
  if (entry.refineFilter == null) return "";
  if (data.refine === undefined || data.refine === null) return "";
  return " (+" + data.refine + ")";
}

// startEditingRefine troca o badge "+N" (refino) por um <input> numérico.
// Só existe para itens que já mostraram ter refino (armas/armaduras — ver
// buildWatchlistRow). Confirmar com Enter passa a exigir esse refino nas
// próximas consultas de preço, como MÍNIMO (o "menor preço atual" da linha
// passa a ser o menor preço entre as lojas com refino igual ou maior);
// deixar o campo vazio e confirmar volta ao padrão (mostra o refino de
// qualquer loja que estiver mais barata, sem exigir nada).
function startEditingRefine(span, id) {
  if (span.querySelector("input")) return;
  const list = loadWatchlist();
  const entry = list.find((e) => e.id === id);
  if (!entry) return;

  const previousText = span.textContent;
  const input = document.createElement("input");
  input.type = "number";
  input.min = "0";
  input.step = "1";
  input.className = "watchlist-refine-input";
  input.value = entry.refineFilter != null ? String(entry.refineFilter) : "";
  input.setAttribute("aria-label", "Refino fixo de " + entry.itemName);

  span.textContent = "";
  span.appendChild(input);
  input.focus();
  input.select();

  let confirmed = false;

  const confirmEdit = () => {
    confirmed = true;
    const raw = input.value.trim();
    let refineFilter = null;
    if (raw !== "") {
      const parsed = Math.round(Number(raw));
      if (Number.isFinite(parsed) && parsed >= 0) refineFilter = parsed;
    }
    const updated = updateEntry(id, { refineFilter, notified: false }) || entry;
    if (updated.refineFilter != null) {
      span.hidden = false;
      span.textContent = refineFilterLabel(updated.refineFilter);
    } else {
      span.textContent = "";
      span.hidden = true;
    }
    fetchLivePrice(updated);
  };

  input.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter") {
      confirmEdit();
    } else if (ev.key === "Escape") {
      confirmed = true;
      span.textContent = previousText;
    }
  });

  input.addEventListener("blur", () => {
    if (!confirmed) span.textContent = previousText;
  });
}

// BONUS_VAZIO é o rótulo do campo de bônus que ainda não exige nada. Não é um
// filtro: é o convite para clicar e digitar um.
const BONUS_VAZIO = "+ bônus";

// paintBonusChip põe o campo de bônus no estado que o valor pede: a frase
// exigida, ou o convite discreto quando não há nenhuma.
function paintBonusChip(span, valor) {
  const preenchido = valor !== "";
  span.textContent = preenchido ? valor : BONUS_VAZIO;
  // O texto completo no title porque o chip trunca com reticências: os bônus
  // são frases inteiras e o painel recolhido tem 300px de largura.
  span.title = preenchido ? valor : "Clique para exigir um bônus aleatório";
  span.classList.toggle("vazio", !preenchido);
}

// startEditingBonus troca um dos campos de bônus por um <input> de texto.
// Enter confirma e passa a procurar anúncios que tenham aquele bônus; Escape
// ou perder o foco sem confirmar descarta a edição. Confirmar com o campo
// vazio remove a exigência daquela posição.
//
// Mesmo padrão de startEditingRefine, com uma diferença: aqui o valor é a
// frase do site, palavra por palavra ("CRIT +5", "Conjuração variável -4%"),
// e a comparação no servidor é por igualdade — um bônus digitado quase certo
// não acha nada. É por isso que o campo já nasce preenchido com o que veio da
// busca, em vez de esperar alguém digitar do zero.
function startEditingBonus(span, id, slot) {
  if (span.querySelector("input")) return;
  const list = loadWatchlist();
  const entry = list.find((e) => e.id === id);
  if (!entry) return;

  const slots = entryBonusSlots(entry);
  const previousValue = slots[slot];

  const input = document.createElement("input");
  input.type = "text";
  input.className = "watchlist-bonus-input";
  input.value = previousValue;
  input.setAttribute("aria-label", "Bônus aleatório " + (slot + 1) + " de " + entry.itemName);

  span.textContent = "";
  span.appendChild(input);
  input.focus();
  input.select();

  let confirmed = false;

  const confirmEdit = () => {
    confirmed = true;
    const bonusFilters = entryBonusSlots(entry);
    bonusFilters[slot] = input.value.trim();
    // notified zerado: o que a linha acompanha mudou, então o aviso precisa
    // poder disparar de novo para a condição nova.
    const updated = updateEntry(id, { bonusFilters, notified: false }) || entry;
    paintBonusChip(span, entryBonusSlots(updated)[slot]);
    fetchLivePrice(updated);
  };

  input.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter") {
      confirmEdit();
    } else if (ev.key === "Escape") {
      confirmed = true;
      paintBonusChip(span, previousValue);
    }
  });

  input.addEventListener("blur", () => {
    if (!confirmed) paintBonusChip(span, previousValue);
  });
}

// --- expandir a watchlist ---
//
// O estado vive em um atributo do <html>, e não numa classe da .page: o script
// inline do <head> precisa aplicá-lo antes da primeira pintura (a .page ainda
// nem existe àquela altura), senão a tela salta do layout de duas colunas para
// o de uma a cada carregamento. Ver index.html.tmpl.
//
// Duas fontes decidem esse atributo, com prioridades diferentes:
//   1. Preferência salva (localStorage) — o usuário já clicou em "«"/"»"
//      alguma vez. Vale sempre, até ele clicar de novo.
//   2. Sem preferência salva, o padrão é expandida — o script do <head> já
//      aplica isso, porque #results sempre começa vazio num carregamento
//      novo. Recolhe sozinha assim que a primeira busca é enviada.
// A distinção entre as duas é o que "não trava nele" quer dizer: o padrão
// nunca é GRAVADO, então nunca vira uma preferência de verdade — só a
// escolha explícita do botão é.
const WATCHLIST_EXPANDIDA_KEY = "ro-market-tracker:watchlist-expandida";

function watchlistExpandida() {
  return document.documentElement.dataset.watchlistExpandida === "1";
}

// aplicarVisualExpansaoDaWatchlist muda a aparência (o atributo que o CSS lê,
// e o rótulo/estado do botão) SEM gravar nada — nem o padrão de "sem
// resultados ainda", nem o recolhimento automático ao buscar, viram
// preferência.
function aplicarVisualExpansaoDaWatchlist(expandida) {
  document.documentElement.dataset.watchlistExpandida = expandida ? "1" : "";

  const botao = document.getElementById("watchlist-expand");
  if (!botao) return;
  botao.setAttribute("aria-expanded", String(expandida));
  botao.textContent = expandida ? "»" : "«";
  botao.title = expandida ? "Recolher" : "Expandir";
  botao.setAttribute(
    "aria-label",
    expandida ? "Recolher a watchlist e mostrar os resultados" : "Expandir a watchlist sobre a área de resultados",
  );
}

// dispararAnimacaoDeTrocaDeLayout reinicia a animação de fade dos painéis que
// trocam de lugar (ver .watchlist-layout-mudando no CSS). Só é chamada nas
// trocas de verdade (clique manual, recolhimento automático ao buscar) — a
// aplicação inicial no carregamento da página (DOMContentLoaded) usa
// aplicarVisualExpansaoDaWatchlist sozinha, sem isto, para não fazer a tela
// piscar num fade logo na primeira pintura, onde não há "troca" nenhuma.
function dispararAnimacaoDeTrocaDeLayout() {
  const page = document.querySelector(".page");
  if (!page) return;
  // Remove e força reflow antes de recolocar: sem isto, alternar duas vezes
  // rápido (a classe já presente da vez anterior) não reinicia a animação —
  // o navegador vê a mesma classe "adicionada" de novo e não dispara nada.
  page.classList.remove("watchlist-layout-mudando");
  void page.offsetWidth;
  page.classList.add("watchlist-layout-mudando");
}

// aplicarExpansaoDaWatchlist é o clique manual no botão "«"/"»": muda a
// aparência e GRAVA a escolha, que passa a valer em qualquer carregamento
// futuro da página até o usuário clicar de novo (ver o comentário acima).
function aplicarExpansaoDaWatchlist(expandida) {
  aplicarVisualExpansaoDaWatchlist(expandida);
  dispararAnimacaoDeTrocaDeLayout();
  try {
    localStorage.setItem(WATCHLIST_EXPANDIDA_KEY, expandida ? "1" : "0");
  } catch {
    // localStorage indisponível (modo privado): a troca vale para a sessão,
    // só não sobrevive a um recarregamento — mesmo caso do tema.
  }
}

// colapsarWatchlistParaNovaBusca recolhe a watchlist quando uma nova busca é
// enviada, se ela estiver expandida — para dar espaço ao resultado. Chamada
// pelo "htmx:beforeRequest" do formulário de busca, e não por um "submit"
// comum: é o evento do próprio htmx, que dispara de forma confiável tanto
// pelo clique na lupa quanto por Enter no campo, sem depender da ordem de
// registro entre os dois listeners do mesmo elemento.
//
// NUNCA grava em localStorage — de propósito. Isto é uma reação a "agora tem
// busca para mostrar", não uma escolha do usuário: se não houver preferência
// salva, o próximo carregamento da página volta a mostrar a watchlist
// expandida por padrão (ver o script do <head>).
function colapsarWatchlistParaNovaBusca() {
  if (!watchlistExpandida()) return;
  aplicarVisualExpansaoDaWatchlist(false);
  dispararAnimacaoDeTrocaDeLayout();
}

// --- reordenar a watchlist ---
//
// Pointer events, e não a API de drag and drop do HTML5: os testes de navegador
// rodam sem repetição de propósito (ver playwright.config.js), e o DnD nativo
// depende de o navegador sintetizar eventos a partir do mouse — a parte
// historicamente mais instável do Playwright. pointerdown/move/up são dirigidos
// diretamente, funcionam no toque de graça e deixam o visual sob controle do
// CSS.
//
// A ordem do array no localStorage JÁ É a ordem de exibição (renderWatchlist
// itera loadWatchlist() direto, e é o desempate de pickNextEntry), então
// reordenar é reordenar o array — sem campo novo na entrada e sem migração
// das que já existem.

// PIXELS_ALEM_DO_CENTRO evita o tremor na fronteira entre dois itens: sem uma
// margem, um movimento de um pixel sobre a divisa faria a linha pular de um
// lado para o outro a cada evento.
const PIXELS_ALEM_DO_CENTRO = 6;

function buildDragHandle(li) {
  const handle = document.createElement("button");
  handle.type = "button";
  handle.className = "watchlist-drag";
  handle.textContent = "⠿";
  handle.title = "Arraste para reordenar";
  handle.setAttribute("aria-label", "Reordenar: arraste, ou use as setas para cima e para baixo");

  // Os eventos do arraste ficam no document, e não no handle com
  // setPointerCapture: reordenar remove e reinsere o <li>, e o handle vai
  // junto — o que libera a captura implicitamente. Na prática, a linha
  // trocava de lugar uma vez e parava de responder no meio do arraste.
  handle.addEventListener("pointerdown", (ev) => {
    ev.preventDefault();
    li.classList.add("dragging");

    const aoMover = (mv) => moverParaPerto(li, mv.clientX, mv.clientY);
    const aoSoltar = () => {
      document.removeEventListener("pointermove", aoMover);
      document.removeEventListener("pointerup", aoSoltar);
      document.removeEventListener("pointercancel", aoSoltar);
      li.classList.remove("dragging");
      persistWatchlistOrder();
    };

    document.addEventListener("pointermove", aoMover);
    document.addEventListener("pointerup", aoSoltar);
    document.addEventListener("pointercancel", aoSoltar);
  });

  // Arrastar não é a única forma de reordenar: sem o teclado, quem não usa
  // mouse não teria como.
  handle.addEventListener("keydown", (ev) => {
    if (ev.key !== "ArrowUp" && ev.key !== "ArrowDown") return;
    ev.preventDefault();
    const alvo = ev.key === "ArrowUp" ? li.previousElementSibling : li.nextElementSibling;
    if (!alvo) return;
    if (ev.key === "ArrowUp") {
      alvo.before(li);
    } else {
      alvo.after(li);
    }
    persistWatchlistOrder();
    handle.focus();
  });

  return handle;
}

// moverParaPerto reinsere a linha arrastada junto do irmão cujo centro está
// mais próximo do ponteiro.
//
// A distância é medida nos dois eixos, e não só na vertical, porque a watchlist
// expandida dispõe as linhas em grade — em uma coluna só o resultado é o mesmo,
// mas em duas o eixo Y sozinho escolheria o vizinho errado.
function moverParaPerto(arrastada, x, y) {
  const container = arrastada.parentElement;
  if (!container) return;

  let alvo = null;
  let menorDistancia = Infinity;
  for (const irmao of container.querySelectorAll(".watchlist-row")) {
    if (irmao === arrastada) continue;
    const r = irmao.getBoundingClientRect();
    const cx = r.left + r.width / 2;
    const cy = r.top + r.height / 2;
    const distancia = Math.hypot(x - cx, y - cy);
    if (distancia < menorDistancia) {
      menorDistancia = distancia;
      alvo = { el: irmao, cx, cy, altura: r.height };
    }
  }
  if (!alvo) return;

  // Só troca depois de o ponteiro passar do centro do vizinho, com folga, e a
  // comparação segue a ordem de leitura: o eixo X só decide quando os dois
  // estão na mesma faixa horizontal — o que em uma coluna só nunca acontece
  // entre linhas diferentes, e na watchlist expandida é o caso comum.
  const depois = arrastada.compareDocumentPosition(alvo.el) & Node.DOCUMENT_POSITION_FOLLOWING;
  const mesmaFaixa = Math.abs(y - alvo.cy) <= alvo.altura / 2;
  const passou = depois
    ? y > alvo.cy + PIXELS_ALEM_DO_CENTRO || (mesmaFaixa && x > alvo.cx + PIXELS_ALEM_DO_CENTRO)
    : y < alvo.cy - PIXELS_ALEM_DO_CENTRO || (mesmaFaixa && x < alvo.cx - PIXELS_ALEM_DO_CENTRO);
  if (!passou) return;

  if (depois) {
    alvo.el.after(arrastada);
  } else {
    alvo.el.before(arrastada);
  }
}

// persistWatchlistOrder grava a ordem que está na tela.
//
// NÃO chama renderWatchlist: ela limpa o painel e reconsulta o preço de TODOS
// os itens, então um arrastar custaria uma requisição por item ao site. Como
// aqui o nó real é que foi movido, não há nada a re-renderizar.
function persistWatchlistOrder() {
  const container = document.getElementById("watchlist-list");
  if (!container) return;

  const ids = [...container.querySelectorAll(".watchlist-row")].map((li) => li.dataset.id);
  const porId = new Map(loadWatchlist().map((entry) => [entry.id, entry]));

  const ordenada = [];
  for (const id of ids) {
    const entry = porId.get(id);
    if (entry) {
      ordenada.push(entry);
      porId.delete(id);
    }
  }
  // O que sobrou não tinha linha na tela (não deveria acontecer) vai para o
  // fim, nunca é descartado: um erro aqui apagaria a watchlist do usuário.
  for (const entry of porId.values()) ordenada.push(entry);

  saveWatchlist(ordenada);
}

function buildWatchlistRow(entry) {
  const li = document.createElement("li");
  li.className = "watchlist-row";
  li.dataset.id = entry.id;

  li.appendChild(buildDragHandle(li));

  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.className = "status-toggle";
  toggle.setAttribute("aria-label", entry.monitoring ? "Desativar monitoramento" : "Ativar monitoramento");
  toggle.title = entry.monitoring
    ? "Monitorando (clique para desativar)"
    : "Não monitorando (clique para ativar)";
  toggle.addEventListener("click", () => toggleMonitoring(entry.id));
  const light = document.createElement("span");
  light.className = "status-light " + (entry.monitoring ? "on" : "off");
  toggle.appendChild(light);

  const info = document.createElement("div");
  info.className = "watchlist-info";

  const nameRow = document.createElement("div");
  nameRow.className = "watchlist-name";
  // O nome vai em um elemento próprio (e não como texto solto) para ser ele —
  // e não o badge de refino ao lado — quem encurta com reticências quando não
  // couber; ver .watchlist-name no CSS.
  const nameText = document.createElement("span");
  nameText.className = "watchlist-name-text";
  nameText.textContent = entry.itemName;
  nameText.title = entry.itemName;
  nameRow.appendChild(nameText);
  const refineBadge = document.createElement("span");
  refineBadge.className = "refine-badge watchlist-refine";
  refineBadge.tabIndex = 0;
  refineBadge.title = "Clique para exigir um refino mínimo deste item";
  refineBadge.addEventListener("click", () => startEditingRefine(refineBadge, entry.id));
  if (entry.refineFilter != null) {
    refineBadge.textContent = refineFilterLabel(entry.refineFilter);
    refineBadge.hidden = false;
  } else {
    refineBadge.hidden = true;
  }
  nameRow.appendChild(refineBadge);

  const pricesRow = document.createElement("div");
  pricesRow.className = "watchlist-prices";

  // Sem preço alvo no modo de disponibilidade: não há valor a esperar, e um
  // campo "Alvo: —" ali só confundiria o que a linha está acompanhando.
  if (!isAvailabilityWatch(entry)) {
    const target = document.createElement("span");
    target.className = "watchlist-target";
    target.tabIndex = 0;
    target.title = "Clique para editar o preço alvo";
    target.textContent = targetLabel(entry.targetPrice);
    target.addEventListener("click", () => startEditingTarget(target, entry.id));
    pricesRow.appendChild(target);
  }

  // .watchlist-current é o espaço do estado ao vivo do item nos dois modos —
  // o preço atual em um, o "tem anúncio?" no outro (ver fetchLivePrice).
  const current = document.createElement("span");
  current.className = "watchlist-current";
  if (!isAvailabilityWatch(entry)) current.append("Atual: ");
  const spinner = document.createElement("span");
  spinner.className = "spinner";
  current.appendChild(spinner);
  const hitBadge = document.createElement("span");
  hitBadge.className = "watchlist-hit-badge";
  hitBadge.textContent = isAvailabilityWatch(entry) ? "🎯 Disponível" : "🎯 Alvo atingido";
  hitBadge.hidden = true;

  // A localização (comando "/navi ...") do vendedor mais barato — mesmo
  // botão/classe da tabela de busca (.navi-copy, copyNavi em app.js), só
  // aparece junto do badge acima, quando a condição que a linha acompanha
  // está valendo (ver updateHitState): é a hora de saber pra onde ir
  // comprar, não em toda checagem sem alvo atingido ainda.
  const location = document.createElement("button");
  location.type = "button";
  location.className = "navi-copy watchlist-location";
  location.title = "Clique para copiar o comando de localização";
  location.hidden = true;
  location.addEventListener("click", () => copyNavi(location, location.dataset.command || ""));

  // Único jeito de forçar uma consulta agora, desde que o "↻" global saiu: só
  // atualiza ESTA linha, sem tocar no cronômetro nem no item que o rodízio
  // automático escolheria a seguir (ver forceEntryUpdate).
  const refreshNow = document.createElement("button");
  refreshNow.type = "button";
  refreshNow.className = "watchlist-refresh-item";
  refreshNow.textContent = "↻";
  refreshNow.title = "Atualizar agora";
  refreshNow.setAttribute("aria-label", "Atualizar preço de " + entry.itemName + " agora");
  refreshNow.addEventListener("click", () => forceEntryUpdate(entry.id));

  pricesRow.appendChild(current);
  pricesRow.appendChild(hitBadge);
  pricesRow.appendChild(location);
  pricesRow.appendChild(refreshNow);

  // Os bônus aleatórios ganham linha própria, e não um lugar ao lado do nome
  // ou do preço: são frases inteiras ("Conjuração variável -4%"), não cabem
  // onde o badge "+7" cabe — e a linha do preço é o estado que o servidor
  // respondeu, enquanto estes campos são o que o usuário está pedindo.
  //
  // Sempre construída, e só escondida quando o item não é equipamento (quem
  // responde isso é o servidor, ver applyPriceResult): assim as entradas
  // gravadas antes desta mudança ganham os campos na primeira checagem, sem
  // migração nenhuma.
  const bonusRow = document.createElement("div");
  bonusRow.className = "watchlist-bonus";
  bonusRow.hidden = entry.isEquipment !== true;
  entryBonusSlots(entry).forEach((valor, slot) => {
    const chip = document.createElement("span");
    chip.className = "bonus-chip watchlist-bonus-chip";
    chip.tabIndex = 0;
    chip.addEventListener("click", () => startEditingBonus(chip, entry.id, slot));
    paintBonusChip(chip, valor);
    bonusRow.appendChild(chip);
  });

  info.appendChild(nameRow);
  info.appendChild(pricesRow);
  info.appendChild(bonusRow);

  const remove = document.createElement("button");
  remove.type = "button";
  remove.className = "watchlist-remove";
  remove.setAttribute("aria-label", "Remover da watchlist");
  remove.textContent = "×";
  remove.addEventListener("click", () => removeFromWatchlist(entry.id));

  li.appendChild(toggle);
  li.appendChild(info);
  li.appendChild(remove);

  // Pinta com o último resultado conhecido (persistido em lastResult) na
  // hora, sem nenhuma requisição — só uma entrada recém-criada, sem cache
  // ainda, fica com o spinner até a primeira consulta responder.
  if (entry.lastResult) {
    applyPriceResult(li, entry, entry.lastResult);
  } else if (lastKnownPrice.has(entry.id)) {
    updateHitState(li, entry, lastKnownPrice.get(entry.id));
  }
  return li;
}

// applyPriceResult aplica ao DOM o resultado de uma consulta de preço — seja
// ela ao vivo (fetchLivePrice, ao terminar) ou o último resultado conhecido,
// lido do cache persistido em localStorage (buildWatchlistRow, no
// carregamento da página, sem nenhuma requisição).
function applyPriceResult(row, entry, data) {
  const currentEl = row.querySelector(".watchlist-current");
  const refineEl = row.querySelector(".watchlist-refine");

  // Com refino exigido pelo usuário, o badge sempre mostra esse valor
  // (é a intenção dele, independente de ter achado anúncio agora ou
  // não); sem exigência, o badge reflete o refino ao vivo da loja mais
  // barata, quando o item for um equipamento.
  if (entry.refineFilter != null) {
    refineEl.hidden = false;
    refineEl.textContent = refineFilterLabel(entry.refineFilter);
  } else if (data.refine !== undefined && data.refine !== null) {
    refineEl.hidden = false;
    refineEl.textContent = "+" + data.refine;
  }

  // O tipo do item vem sempre que houver anúncio, mesmo quando o filtro não
  // casou com nenhum — é o que decide se a linha oferece os campos de bônus.
  // Ausente significa "não sei" (item sem anúncio nenhum agora), e aí o que
  // já se sabia continua valendo: perder os campos levaria junto os filtros
  // que o usuário digitou neles.
  if (data.equipment !== undefined) {
    const bonusRow = row.querySelector(".watchlist-bonus");
    if (bonusRow) bonusRow.hidden = !data.equipment;
    if (entry.isEquipment !== data.equipment) {
      updateEntry(entry.id, { isEquipment: data.equipment });
      entry.isEquipment = data.equipment;
    }
  }

  if (!data.found) {
    // "partial" é o servidor dizendo que nem chegou a olhar todos os
    // anúncios — o orçamento de consultas acabou antes. Dizer "Sem anúncios"
    // aí seria mentira: a cobertura ainda está crescendo, e o próximo ciclo
    // continua de onde este parou.
    if (data.partial) {
      currentEl.textContent = "Verificando…";
      currentEl.title = "O site é consultado aos poucos para não ser bloqueado; a busca continua no próximo ciclo.";
    } else {
      currentEl.textContent = isAvailabilityWatch(entry) ? "Nenhum anúncio" : "Sem anúncios";
      currentEl.title = "";
    }
    lastKnownPrice.set(entry.id, null);
    updateHitState(row, entry, null, null);
    return;
  }
  currentEl.title = "";
  // Com refino exigido, o badge mostra o PISO, então o refino que o anúncio
  // encontrado tem de fato não caberia em lugar nenhum — e ele importa: quem
  // pede "+7 ou mais" precisa saber se o que apareceu barato é um +7 ou um
  // +9 antes de ir comprar. Sem exigência os dois números são o mesmo, e o
  // badge já basta.
  const achado = formatMoney(data.minPrice) + refineSuffix(entry, data);
  currentEl.textContent = isAvailabilityWatch(entry)
    ? "Produto encontrado por " + achado
    : "Atual: " + achado;
  lastKnownPrice.set(entry.id, data.minPrice);
  updateHitState(row, entry, data.minPrice, data.naviCommand);
}

// fetchLivePrice consulta o preço ao vivo de uma entrada. Por padrão o
// servidor pode responder do cache dele (bom para o tick automático e para
// recarregamentos de página — várias abas não multiplicam o tráfego ao
// GnJoy); com fresh=true (o "↻" de cada linha), o cache é ignorado e a
// consulta vai ao mercado de verdade.
//
// O resultado é persistido na entrada (lastCheckedAt, lastResult): é o que
// alimenta tanto o rodízio automático (pickNextEntry escolhe pelo
// lastCheckedAt mais antigo) quanto a pintura instantânea da linha num
// carregamento futuro (buildWatchlistRow).
async function fetchLivePrice(entry, fresh = false) {
  const row = findRow(entry.id);
  if (!row) return;
  const currentEl = row.querySelector(".watchlist-current");
  try {
    let url =
      "/web/watchlist/price?server=" + encodeURIComponent(entry.server) +
      "&itemId=" + encodeURIComponent(entry.itemId) +
      "&item=" + encodeURIComponent(entrySearchName(entry));
    if (entry.refineFilter != null) {
      url += "&refine=" + encodeURIComponent(entry.refineFilter);
    }
    // Um parâmetro por bônus, em vez de uma lista com separador: as frases
    // têm pontuação livre e qualquer separador escolhido a dedo poderia
    // aparecer dentro de uma delas. encodeURIComponent (e não
    // URLSearchParams) porque o "+" de "CRIT +4" precisa virar %2B — como
    // espaço, ele viraria outro bônus.
    for (const bonus of entryBonusFilters(entry)) {
      url += "&bonus=" + encodeURIComponent(bonus);
    }
    if (fresh) {
      url += "&fresh=1";
    }
    const res = await fetch(url);
    if (!res.ok) throw new Error("status " + res.status);
    const data = await res.json();
    applyPriceResult(row, entry, data);
    updateEntry(entry.id, { lastCheckedAt: Date.now(), lastResult: data });
  } catch {
    currentEl.textContent = "Indisponível";
    // A consulta foi de fato tentada — o rodízio avança para o próximo item
    // mesmo assim, e este volta à vez quando for o mais antigo de novo. O
    // último resultado conhecido (lastResult) não é sobrescrito: continua
    // sendo o dado exibido antes desta tentativa falhar.
    updateEntry(entry.id, { lastCheckedAt: Date.now() });
  }
}

// isHit diz se a condição que a entrada acompanha está valendo agora. No modo
// de preço é o alvo ter sido alcançado; no de disponibilidade basta existir
// anúncio, que é a única coisa que se estava esperando.
function isHit(entry, minPrice) {
  if (minPrice == null) return false;
  if (isAvailabilityWatch(entry)) return true;
  return entry.targetPrice != null && minPrice <= entry.targetPrice;
}

// updateHitState atualiza o destaque visual da linha conforme isHit e, ao
// detectar a transição de "não valia" para "vale", dispara o aviso (toast +
// notificação do SO) uma única vez por cruzamento — o campo "notified" da
// entrada é o que evita repetir o aviso a cada checagem e é rearmado quando a
// condição deixa de valer.
//
// naviCommand só é usado quando hit é true: é o que mostra (e esconde de
// volta, se a condição deixar de valer) o botão de localização — o servidor
// manda a localização sempre que encontra um anúncio, não sabe qual é o
// alvo do usuário (ele vive só no navegador), então a decisão de exibir de
// fato é toda daqui.
function updateHitState(row, entry, minPrice, naviCommand) {
  const hit = isHit(entry, minPrice);
  row.classList.toggle("target-hit", hit);
  const badge = row.querySelector(".watchlist-hit-badge");
  if (badge) badge.hidden = !hit;

  const locationEl = row.querySelector(".watchlist-location");
  if (locationEl) {
    if (hit && naviCommand) {
      locationEl.textContent = naviCommand;
      locationEl.dataset.command = naviCommand;
      locationEl.hidden = false;
    } else {
      locationEl.hidden = true;
    }
  }

  const wasNotified = Boolean(entry.notified);
  if (hit && !wasNotified) {
    const updated = updateEntry(entry.id, { notified: true });
    if (updated) entry.notified = true;
    notifyHit(entry, minPrice);
  } else if (!hit && wasNotified) {
    const updated = updateEntry(entry.id, { notified: false });
    if (updated) entry.notified = false;
  }
}

function showToast(message) {
  let container = document.getElementById("toast-container");
  if (!container) {
    container = document.createElement("div");
    container.id = "toast-container";
    container.className = "toast-container";
    document.body.appendChild(container);
  }

  const toast = document.createElement("div");
  toast.className = "toast";
  toast.textContent = message;
  container.appendChild(toast);

  requestAnimationFrame(() => toast.classList.add("show"));
  setTimeout(() => {
    toast.classList.remove("show");
    setTimeout(() => toast.remove(), 300);
  }, 6000);
}

// notifyHit sempre mostra o toast (funciona sem nenhuma permissão) e, se o
// navegador suportar e permitir, também dispara uma notificação nativa do
// sistema operacional. A permissão só é pedida na hora em que ela de fato faz
// falta (primeiro aviso), não no carregamento da página.
async function notifyHit(entry, minPrice) {
  const message = isAvailabilityWatch(entry)
    ? entry.itemName + " foi encontrado no mercado por " + formatMoney(minPrice)
    : entry.itemName + " atingiu o alvo: " + formatMoney(minPrice) +
      " (alvo: " + formatMoney(entry.targetPrice) + ")";
  showToast(message);

  if (!("Notification" in window)) return;
  let permission = Notification.permission;
  if (permission === "default") {
    try {
      permission = await Notification.requestPermission();
    } catch {
      return;
    }
  }
  if (permission === "granted") {
    new Notification("RO Market Tracker", { body: message });
  }
}

// monitorCheckRunning impede dois ticks de checagem simultâneos (o timer
// disparando em cima de uma consulta ainda em andamento) — o segundo é
// simplesmente ignorado, o próximo tick tenta de novo.
let monitorCheckRunning = false;

// pickNextEntry escolhe o próximo item a consultar automaticamente: entre os
// com monitoring ativado (é o que o "ligado/desligado" da luz da watchlist
// significa), o que está há mais tempo sem consulta — lastCheckedAt mais
// antigo, e nunca consultado conta como o mais antigo de todos. Empate é
// desfeito pela ordem da lista, a mesma que já é a ordem de exibição.
//
// Isto substitui um índice/cursor de rodízio explícito: o revezamento entre
// A, B, C se ajusta sozinho a remoções e adições, porque cada entrada carrega
// consigo mesma "quando foi a última vez" — não há estado externo para
// reconciliar com a lista atual.
function pickNextEntry(list) {
  let chosen = null;
  for (const entry of list) {
    if (!entry.monitoring) continue;
    if (chosen == null || (entry.lastCheckedAt || 0) < (chosen.lastCheckedAt || 0)) {
      chosen = entry;
    }
  }
  return chosen;
}

// runMonitoringTick é o tick automático: consulta só UM item — o escolhido
// por pickNextEntry —, nunca a lista inteira de uma vez. É o que garante o
// ritmo constante de uma consulta por minuto (MONITOR_TICK_MS), não importa
// quantos itens a watchlist tenha.
async function runMonitoringTick(fresh = false) {
  if (monitorCheckRunning || watchlistSuspended) return;
  monitorCheckRunning = true;
  try {
    const entry = pickNextEntry(loadWatchlist());
    if (entry) await fetchLivePrice(entry, fresh);
  } finally {
    monitorCheckRunning = false;
  }
}

// watchlistSuspended espelha o estado que o servidor publica pelo stream de
// atividade (ver activity-bar.js): enquanto o site estiver limitando as
// consultas, o ciclo automático para de sair.
let watchlistSuspended = false;

function setWatchlistSuspended(suspended) {
  const era = watchlistSuspended;
  watchlistSuspended = suspended;

  const timer = document.getElementById("watchlist-timer");

  if (suspended) {
    // Sem cancelar o setTimeout pendente, ele dispara mesmo assim: encontra
    // runMonitoringTick recusando (correto, sem custo — ver acima), mas
    // reagenda outro tick de qualquer forma, e o cronômetro volta a contar
    // como se a checagem automática continuasse rodando normalmente. É
    // exatamente essa contagem fantasma que confundia quem olhava a
    // watchlist durante um bloqueio.
    if (monitorTimerId) clearTimeout(monitorTimerId);
    monitorTimerId = null;
    nextMonitorRunAt = null;
    updateCountdownDisplay();
    if (timer) timer.title = "Pausado: o site está limitando as consultas";
    return;
  }

  if (timer) timer.title = "Tempo até a próxima checagem automática de preços";
  // Ao voltar ao normal, uma checagem imediata: a última pode ter sido
  // interrompida no meio, e esperar mais um minuto por um dado que já dá
  // para buscar seria gratuito. Também é o que tira o cronômetro do "--:--"
  // e volta a contar.
  if (era) {
    runMonitoringTick(true);
    scheduleMonitoring();
  }
}

// scheduleMonitoring (re)agenda o próximo tick automático usando setTimeout
// (em vez de setInterval) para que retomar de uma suspensão possa cancelar a
// espera pendente e recomeçar a contagem do zero, sem deixar um tick
// duplicado rodando em paralelo. O tick seguinte só é agendado quando o
// atual termina, então uma consulta lenta (site devagar) atrasa o próximo em
// vez de se sobrepor a ele.
function scheduleMonitoring(delayMs = MONITOR_TICK_MS) {
  if (monitorTimerId) clearTimeout(monitorTimerId);
  nextMonitorRunAt = Date.now() + delayMs;
  monitorTimerId = setTimeout(async () => {
    await runMonitoringTick();
    scheduleMonitoring();
  }, delayMs);
  updateCountdownDisplay();
}

// forceEntryUpdate é o botão "↻" de cada linha da watchlist: consulta só
// aquele item, na hora, ignorando o cache do servidor (fresh) — sem tocar no
// cronômetro nem no item que o tick automático escolheria a seguir.
function forceEntryUpdate(id) {
  const entry = loadWatchlist().find((e) => e.id === id);
  if (!entry) return;
  fetchLivePrice(entry, true);
}

function updateCountdownDisplay() {
  const el = document.getElementById("watchlist-countdown");
  if (!el) return;
  if (nextMonitorRunAt == null) {
    // Sem ciclo agendado — nem antes do primeiro (ver DOMContentLoaded), nem
    // durante uma suspensão (ver setWatchlistSuspended). Mesmo texto dos dois
    // casos: não há nada de fato contando.
    el.textContent = "--:--";
    return;
  }
  const remainingMs = Math.max(0, nextMonitorRunAt - Date.now());
  const totalSeconds = Math.ceil(remainingMs / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  el.textContent = String(minutes).padStart(2, "0") + ":" + String(seconds).padStart(2, "0");
}

// renderWatchlist reconstrói o painel inteiro a partir do localStorage —
// usado só no carregamento da página. Ações do usuário (adicionar, remover,
// ligar/desligar, editar alvo) atualizam o DOM diretamente em vez de chamar
// isto, para não refazer a consulta de preço de itens que já estavam
// carregados.
//
// Não dispara nenhuma requisição: cada linha nasce com o último resultado
// conhecido (buildWatchlistRow lê entry.lastResult do próprio localStorage).
// A única consulta de verdade do carregamento é a que o DOMContentLoaded
// dispara logo em seguida, para o item escolhido pelo rodízio — uma só, não
// uma por item.
function renderWatchlist() {
  const container = document.getElementById("watchlist-list");
  if (!container) return;
  container.innerHTML = "";
  const list = loadWatchlist();
  for (const entry of list) {
    container.appendChild(buildWatchlistRow(entry));
  }
  updateEmptyState();
}

document.addEventListener("DOMContentLoaded", () => {
  renderWatchlist();
  // A primeira consulta de verdade sai na hora — só a do item escolhido pelo
  // rodízio (pickNextEntry) —, não uma em rajada por item: quem abriu a
  // página já viu o último preço conhecido no passo acima. Da segunda
  // consulta em diante, o ritmo de MONITOR_TICK_MS passa a valer.
  runMonitoringTick();
  scheduleMonitoring();
  setInterval(updateCountdownDisplay, 1000);

  const expandButton = document.getElementById("watchlist-expand");
  if (expandButton) {
    // Visual, não persistente: o atributo já veio aplicado pelo script do
    // <head> (preferência salva, ou o padrão expandido de "sem resultados
    // ainda" — ver o comentário acima de WATCHLIST_EXPANDIDA_KEY), e isto só
    // acerta o rótulo e o aria-expanded do botão, que não existiam àquela
    // altura. Usar a versão que GRAVA aqui transformaria o padrão em
    // preferência permanente no primeiro carregamento de todo mundo.
    aplicarVisualExpansaoDaWatchlist(watchlistExpandida());
    expandButton.addEventListener("click", () => aplicarExpansaoDaWatchlist(!watchlistExpandida()));
  }

  // Uma nova busca precisa do espaço que a watchlist expandida está usando.
  // Ver colapsarWatchlistParaNovaBusca.
  const searchForm = document.querySelector(".search-form");
  if (searchForm) searchForm.addEventListener("htmx:beforeRequest", colapsarWatchlistParaNovaBusca);

  // Tira a classe da animação de troca de layout assim que ela termina — ver
  // dispararAnimacaoDeTrocaDeLayout. Sem isto ela não atrapalharia nada (é só
  // a presença da classe que dispara o @keyframes, remover e recolocar é o
  // que reinicia), mas deixar a classe presa no elemento seria sujeira.
  document.querySelector(".page")?.addEventListener("animationend", (ev) => {
    if (ev.animationName === "watchlist-layout-fade") {
      ev.currentTarget.classList.remove("watchlist-layout-mudando");
    }
  });
});
