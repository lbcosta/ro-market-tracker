// Watchlist do RO Market Tracker.
//
// A lista em si (quais itens, preço alvo, ligado/desligado) vive inteira no
// navegador, em localStorage — não há conta de usuário nem persistência no
// servidor. O servidor só é consultado para o dado que o navegador não tem
// como calcular sozinho: o menor preço anunciado agora e o refino da loja
// mais barata (GET /web/watchlist/price), respeitando o rate limiting já
// aplicado no client Go.
//
// Monitoramento: a cada MONITOR_INTERVAL_MS, os itens com monitoring=true
// têm o preço reconsultado; quando a condição que o item acompanha passa a
// valer, o usuário é avisado (toast + notificação do SO) e a linha é
// destacada. Só existe uma notificação por "cruzamento" da condição —
// enquanto ela continuar valendo, não notifica de novo a cada checagem; só
// volta a notificar se ela deixar de valer e voltar a valer depois (ver
// campo "notified" da entrada, persistido).
const WATCHLIST_KEY = "ro-market-tracker:watchlist";
const MONITOR_INTERVAL_MS = 5 * 60 * 1000;

// MONITOR_ITEM_SPACING_MS é a pausa entre a consulta de um item e a do
// seguinte dentro de um mesmo ciclo de checagem. As consultas já saem em
// série, mas emendadas elas ocupariam a fila do servidor continuamente do
// primeiro ao último item; a pausa deixa brechas para uma busca que o
// usuário faça no meio do ciclo — e é um espaçamento a mais de cortesia com
// o site do jogo.
const MONITOR_ITEM_SPACING_MS = 1000;

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
// Sem refino fixado o id continua "server:itemId": as entradas gravadas antes
// desta mudança seguem válidas, sem migração.
//
// É opaco e não muda depois de criado: editar o refino pela linha altera o
// refineFilter, não o id.
function watchlistId(server, itemId, refineFilter) {
  if (refineFilter == null) return server + ":" + itemId;
  return server + ":" + itemId + ":+" + refineFilter;
}

// parseRefineData lê o data-refine que o cabeçalho da seção emite quando a
// busca separou os anúncios por refino. Ausente (a busca não verificou) é
// diferente de "+0" (verificou, e a unidade não tem refino).
function parseRefineData(raw) {
  if (raw == null || raw === "") return null;
  const parsed = Math.round(Number(raw));
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : null;
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

  // Já vem fixado quando a seção representa um refino só: a linha nasce
  // acompanhando aquela unidade, e não "a mais barata de qualquer refino".
  const refineFilter = parseRefineData(button.dataset.refine);

  const id = watchlistId(server, itemId, refineFilter);
  const list = loadWatchlist();
  if (list.some((entry) => entry.id === id)) return;

  const entry = {
    id,
    server,
    itemId: Number(itemId),
    itemName,
    searchName: button.dataset.searchName || itemName,
    mode: button.dataset.mode === MODE_AVAILABILITY ? MODE_AVAILABILITY : MODE_PRICE,
    targetPrice: null,
    refineFilter,
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
    if (row) updateHitState(row, updated, lastKnownPrice.has(id) ? lastKnownPrice.get(id) : null);
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

// startEditingRefine troca o badge "+N" (refino) por um <input> numérico.
// Só existe para itens que já mostraram ter refino (armas/armaduras — ver
// buildWatchlistRow). Confirmar com Enter passa a exigir esse refino
// específico nas próximas consultas de preço (o "menor preço atual" da
// linha passa a ser o menor preço só entre lojas NESSE refino); deixar o
// campo vazio e confirmar volta ao padrão (mostra o refino de qualquer
// loja que estiver mais barata, sem fixar um valor).
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
      span.textContent = "+" + updated.refineFilter;
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

// --- expandir a watchlist ---
//
// O estado vive em um atributo do <html>, e não numa classe da .page: o script
// inline do <head> precisa aplicá-lo antes da primeira pintura (a .page ainda
// nem existe àquela altura), senão a tela salta do layout de duas colunas para
// o de uma a cada carregamento. Ver index.html.tmpl.
const WATCHLIST_EXPANDIDA_KEY = "ro-market-tracker:watchlist-expandida";

function watchlistExpandida() {
  return document.documentElement.dataset.watchlistExpandida === "1";
}

function aplicarExpansaoDaWatchlist(expandida) {
  document.documentElement.dataset.watchlistExpandida = expandida ? "1" : "";
  try {
    localStorage.setItem(WATCHLIST_EXPANDIDA_KEY, expandida ? "1" : "0");
  } catch {
    // localStorage indisponível (modo privado): a troca vale para a sessão,
    // só não sobrevive a um recarregamento — mesmo caso do tema.
  }

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

// --- reordenar a watchlist ---
//
// Pointer events, e não a API de drag and drop do HTML5: os testes de navegador
// rodam sem repetição de propósito (ver playwright.config.js), e o DnD nativo
// depende de o navegador sintetizar eventos a partir do mouse — a parte
// historicamente mais instável do Playwright. pointerdown/move/up são dirigidos
// diretamente, funcionam no toque de graça e deixam o visual sob controle do
// CSS.
//
// A ordem do array no localStorage JÁ É a ordem de exibição (renderWatchlist e
// runMonitoringCheck iteram loadWatchlist() direto), então reordenar é
// reordenar o array — sem campo novo na entrada e sem migração das que já
// existem.

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
  refineBadge.title = "Clique para fixar o refino que você quer acompanhar";
  refineBadge.addEventListener("click", () => startEditingRefine(refineBadge, entry.id));
  if (entry.refineFilter != null) {
    refineBadge.textContent = "+" + entry.refineFilter;
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

  pricesRow.appendChild(current);
  pricesRow.appendChild(hitBadge);
  info.appendChild(nameRow);
  info.appendChild(pricesRow);

  const remove = document.createElement("button");
  remove.type = "button";
  remove.className = "watchlist-remove";
  remove.setAttribute("aria-label", "Remover da watchlist");
  remove.textContent = "×";
  remove.addEventListener("click", () => removeFromWatchlist(entry.id));

  li.appendChild(toggle);
  li.appendChild(info);
  li.appendChild(remove);

  if (lastKnownPrice.has(entry.id)) {
    updateHitState(li, entry, lastKnownPrice.get(entry.id));
  }
  return li;
}

// fetchLivePrice consulta o preço ao vivo de uma entrada. Por padrão o
// servidor pode responder do cache dele (bom para o ciclo automático e para
// recarregamentos de página — várias abas não multiplicam o tráfego ao
// GnJoy); com fresh=true (o botão "↻"), o cache é ignorado e a consulta vai
// ao mercado de verdade.
async function fetchLivePrice(entry, fresh = false) {
  const row = findRow(entry.id);
  if (!row) return;
  const currentEl = row.querySelector(".watchlist-current");
  const refineEl = row.querySelector(".watchlist-refine");
  try {
    let url =
      "/web/watchlist/price?server=" + encodeURIComponent(entry.server) +
      "&itemId=" + encodeURIComponent(entry.itemId) +
      "&item=" + encodeURIComponent(entrySearchName(entry));
    if (entry.refineFilter != null) {
      url += "&refine=" + encodeURIComponent(entry.refineFilter);
    }
    if (fresh) {
      url += "&fresh=1";
    }
    const res = await fetch(url);
    if (!res.ok) throw new Error("status " + res.status);
    const data = await res.json();

    // Com refino fixado pelo usuário, o badge sempre mostra esse valor
    // (é a intenção dele, independente de ter achado anúncio agora ou
    // não); sem fixação, o badge reflete o refino ao vivo da loja mais
    // barata, quando o item for um equipamento.
    if (entry.refineFilter != null) {
      refineEl.hidden = false;
      refineEl.textContent = "+" + entry.refineFilter;
    } else if (data.refine !== undefined && data.refine !== null) {
      refineEl.hidden = false;
      refineEl.textContent = "+" + data.refine;
    }

    if (!data.found) {
      currentEl.textContent = isAvailabilityWatch(entry) ? "Nenhum anúncio" : "Sem anúncios";
      lastKnownPrice.set(entry.id, null);
      updateHitState(row, entry, null);
      return;
    }
    currentEl.textContent = isAvailabilityWatch(entry)
      ? "Produto encontrado por " + formatMoney(data.minPrice)
      : "Atual: " + formatMoney(data.minPrice);
    lastKnownPrice.set(entry.id, data.minPrice);
    updateHitState(row, entry, data.minPrice);
  } catch {
    currentEl.textContent = "Indisponível";
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
function updateHitState(row, entry, minPrice) {
  const hit = isHit(entry, minPrice);
  row.classList.toggle("target-hit", hit);
  const badge = row.querySelector(".watchlist-hit-badge");
  if (badge) badge.hidden = !hit;

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

// monitorCheckRunning impede dois ciclos de checagem simultâneos (o timer
// disparando em cima de um ciclo ainda em andamento, ou o "↻" apertado duas
// vezes) — o segundo é simplesmente ignorado, já que o primeiro consultará
// os mesmos itens.
let monitorCheckRunning = false;

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// runMonitoringCheck é a checagem periódica: só os itens com monitoring
// ativado participam (é o que o "ligado/desligado" da luz da watchlist
// significa). Itens desligados continuam na lista, só não são
// reconsultados nem podem disparar aviso enquanto assim permanecerem.
//
// As consultas saem EM SÉRIE (um await por item), não todas de uma vez: o
// servidor enfileiraria as paralelas de qualquer forma no rate limiter dele,
// e despejar a lista inteira de uma vez só ocuparia a fila — atrasando
// qualquer busca que o usuário fizesse no meio do ciclo. Entre um item e o
// seguinte ainda há uma pausa de MONITOR_ITEM_SPACING_MS, pelo mesmo motivo.
async function runMonitoringCheck(fresh = false) {
  if (monitorCheckRunning || watchlistSuspended) return;
  monitorCheckRunning = true;
  try {
    let first = true;
    for (const entry of loadWatchlist()) {
      if (!entry.monitoring) continue;
      // Reconferido a cada item: a suspensão pode chegar no meio do ciclo, e
      // seguir consultando os restantes seria bater numa porta que o servidor
      // já fechou.
      if (watchlistSuspended) break;
      if (!first) await sleep(MONITOR_ITEM_SPACING_MS);
      first = false;
      await fetchLivePrice(entry, fresh);
    }
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
    // runMonitoringCheck recusando (correto, sem custo — ver acima), mas
    // reagenda outro ciclo completo de qualquer forma, e o cronômetro volta
    // a contar como se a checagem automática continuasse rodando
    // normalmente. É exatamente essa contagem fantasma que confundia quem
    // olhava a watchlist durante um bloqueio.
    if (monitorTimerId) clearTimeout(monitorTimerId);
    monitorTimerId = null;
    nextMonitorRunAt = null;
    updateCountdownDisplay();
    if (timer) timer.title = "Pausado: o site está limitando as consultas";
    return;
  }

  if (timer) timer.title = "Tempo até a próxima checagem automática de preços";
  // Ao voltar ao normal, uma checagem imediata: a última pode ter sido
  // interrompida no meio, e esperar mais cinco minutos por dados que já dá
  // para buscar seria gratuito. Também é o que tira o cronômetro do "--:--"
  // e volta a contar.
  if (era) forceMonitoringNow();
}

// scheduleMonitoring (re)agenda a próxima checagem automática usando
// setTimeout (em vez de setInterval) para que "forçar atualização agora"
// possa cancelar a espera pendente e recomeçar a contagem do zero, sem
// deixar uma checagem duplicada rodando em paralelo. O ciclo seguinte só é
// agendado quando o atual termina, então um ciclo lento (muitos itens, site
// devagar) atrasa o próximo em vez de se sobrepor a ele.
function scheduleMonitoring(delayMs = MONITOR_INTERVAL_MS) {
  if (monitorTimerId) clearTimeout(monitorTimerId);
  nextMonitorRunAt = Date.now() + delayMs;
  monitorTimerId = setTimeout(async () => {
    await runMonitoringCheck();
    scheduleMonitoring();
  }, delayMs);
  updateCountdownDisplay();
}

// forceMonitoringNow é chamado pelo botão "↻" ao lado do título da
// watchlist: roda a checagem imediatamente — com fresh, ignorando o cache do
// servidor, porque quem apertou quer o estado de AGORA — e reinicia o
// cronômetro para um novo ciclo completo de MONITOR_INTERVAL_MS.
function forceMonitoringNow() {
  runMonitoringCheck(true);
  scheduleMonitoring();
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
function renderWatchlist() {
  const container = document.getElementById("watchlist-list");
  if (!container) return;
  container.innerHTML = "";
  const list = loadWatchlist();
  for (const entry of list) {
    container.appendChild(buildWatchlistRow(entry));
  }
  updateEmptyState();
  for (const entry of list) {
    fetchLivePrice(entry);
  }
}

document.addEventListener("DOMContentLoaded", () => {
  renderWatchlist();
  scheduleMonitoring();
  setInterval(updateCountdownDisplay, 1000);

  const refreshButton = document.getElementById("watchlist-refresh-now");
  if (refreshButton) refreshButton.addEventListener("click", forceMonitoringNow);

  const expandButton = document.getElementById("watchlist-expand");
  if (expandButton) {
    // O atributo já veio aplicado pelo script do <head>; isto só acerta o
    // rótulo e o aria-expanded do botão, que não existiam àquela altura.
    aplicarExpansaoDaWatchlist(watchlistExpandida());
    expandButton.addEventListener("click", () => aplicarExpansaoDaWatchlist(!watchlistExpandida()));
  }
});
