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
// têm o preço reconsultado; se o menor preço atual cair para o valor do
// alvo ou abaixo dele, o usuário é avisado (toast + notificação do SO) e a
// linha do item é destacada. Só existe uma notificação por "cruzamento" do
// alvo — enquanto o preço continuar abaixo do alvo, não notifica de novo a
// cada checagem; só volta a notificar se o preço subir acima do alvo e cair
// de novo depois (ver campo "notified" da entrada, persistido).
const WATCHLIST_KEY = "ro-market-tracker:watchlist";
const MONITOR_INTERVAL_MS = 5 * 60 * 1000;

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

// addToWatchlist é chamado pelo botão "+ Watchlist" nos resultados de
// busca (ver results.html.tmpl). O item é identificado por server+itemId —
// duplicar o clique não duplica a entrada.
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
  const target = document.createElement("span");
  target.className = "watchlist-target";
  target.tabIndex = 0;
  target.title = "Clique para editar o preço alvo";
  target.textContent = targetLabel(entry.targetPrice);
  target.addEventListener("click", () => startEditingTarget(target, entry.id));
  const current = document.createElement("span");
  current.className = "watchlist-current";
  current.append("Atual: ");
  const spinner = document.createElement("span");
  spinner.className = "spinner";
  current.appendChild(spinner);
  const hitBadge = document.createElement("span");
  hitBadge.className = "watchlist-hit-badge";
  hitBadge.textContent = "🎯 Alvo atingido";
  hitBadge.hidden = true;

  pricesRow.appendChild(target);
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

  if (entry.targetPrice != null && lastKnownPrice.has(entry.id)) {
    updateHitState(li, entry, lastKnownPrice.get(entry.id));
  }
  return li;
}

async function fetchLivePrice(entry) {
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
      currentEl.textContent = "Sem anúncios";
      lastKnownPrice.set(entry.id, null);
      updateHitState(row, entry, null);
      return;
    }
    currentEl.textContent = "Atual: " + formatMoney(data.minPrice);
    lastKnownPrice.set(entry.id, data.minPrice);
    updateHitState(row, entry, data.minPrice);
  } catch {
    currentEl.textContent = "Indisponível";
  }
}

// updateHitState decide se o item está com o alvo atingido (preço mínimo
// atual <= preço alvo), atualiza o destaque visual da linha e, ao detectar
// a transição de "não atingido" para "atingido", dispara o aviso (toast +
// notificação do SO) uma única vez por "cruzamento" do alvo.
function updateHitState(row, entry, minPrice) {
  const hit = entry.targetPrice != null && minPrice != null && minPrice <= entry.targetPrice;
  row.classList.toggle("target-hit", hit);
  const badge = row.querySelector(".watchlist-hit-badge");
  if (badge) badge.hidden = !hit;

  const wasNotified = Boolean(entry.notified);
  if (hit && !wasNotified) {
    const updated = updateEntry(entry.id, { notified: true });
    if (updated) entry.notified = true;
    notifyTargetHit(entry, minPrice);
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

// notifyTargetHit sempre mostra o toast (funciona sem nenhuma permissão) e,
// se o navegador suportar e permitir, também dispara uma notificação nativa
// do sistema operacional. A permissão só é pedida na hora em que ela de
// fato faz falta (primeiro alvo atingido), não no carregamento da página.
async function notifyTargetHit(entry, minPrice) {
  const message = entry.itemName + " atingiu o alvo: " + formatMoney(minPrice) +
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

// runMonitoringCheck é a checagem periódica: só os itens com monitoring
// ativado participam (é o que o "ligado/desligado" da luz da watchlist
// significa). Itens desligados continuam na lista, só não são
// reconsultados nem podem disparar aviso enquanto assim permanecerem.
function runMonitoringCheck() {
  for (const entry of loadWatchlist()) {
    if (entry.monitoring) fetchLivePrice(entry);
  }
}

// scheduleMonitoring (re)agenda a próxima checagem automática usando
// setTimeout (em vez de setInterval) para que "forçar atualização agora"
// possa cancelar a espera pendente e recomeçar a contagem do zero, sem
// deixar uma checagem duplicada rodando em paralelo.
function scheduleMonitoring(delayMs = MONITOR_INTERVAL_MS) {
  if (monitorTimerId) clearTimeout(monitorTimerId);
  nextMonitorRunAt = Date.now() + delayMs;
  monitorTimerId = setTimeout(() => {
    runMonitoringCheck();
    scheduleMonitoring();
  }, delayMs);
  updateCountdownDisplay();
}

// forceMonitoringNow é chamado pelo botão "↻" ao lado do título da
// watchlist: roda a checagem imediatamente e reinicia o cronômetro para um
// novo ciclo completo de MONITOR_INTERVAL_MS.
function forceMonitoringNow() {
  runMonitoringCheck();
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
