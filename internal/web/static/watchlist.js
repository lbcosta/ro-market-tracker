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

function watchlistId(server, itemId) {
  return server + ":" + itemId;
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
// entrada vai acompanhar — ver MODE_PRICE e MODE_AVAILABILITY. O item é
// identificado por server+itemId, então duplicar o clique não duplica a
// entrada.
function addToWatchlist(button) {
  const server = button.dataset.server;
  const itemId = button.dataset.itemId;
  const itemName = button.dataset.itemName;
  if (!server || !itemId || !itemName) return;

  const id = watchlistId(server, itemId);
  const list = loadWatchlist();
  if (list.some((entry) => entry.id === id)) return;

  const entry = {
    id,
    server,
    itemId: Number(itemId),
    itemName,
    mode: button.dataset.mode === MODE_AVAILABILITY ? MODE_AVAILABILITY : MODE_PRICE,
    targetPrice: null,
    refineFilter: null,
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

function buildWatchlistRow(entry) {
  const li = document.createElement("li");
  li.className = "watchlist-row";
  li.dataset.id = entry.id;

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
      "&item=" + encodeURIComponent(entry.itemName);
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
  if (monitorCheckRunning) return;
  monitorCheckRunning = true;
  try {
    let first = true;
    for (const entry of loadWatchlist()) {
      if (!entry.monitoring) continue;
      if (!first) await sleep(MONITOR_ITEM_SPACING_MS);
      first = false;
      await fetchLivePrice(entry, fresh);
    }
  } finally {
    monitorCheckRunning = false;
  }
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
  if (!el || nextMonitorRunAt == null) return;
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
});
