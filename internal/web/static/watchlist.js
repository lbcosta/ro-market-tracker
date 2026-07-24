// Watchlist do RO Market Tracker.
//
// A lista em si (quais itens, preço alvo, ligado/desligado) vive inteira no
// navegador, em localStorage — não há conta de usuário nem persistência no
// servidor. O servidor só é consultado para o dado que o navegador não tem
// como calcular sozinho: o menor preço anunciado agora e o refino da loja
// mais barata (GET /web/watchlist/price), respeitando o rate limiting já
// aplicado no client Go.
const WATCHLIST_KEY = "ro-market-tracker:watchlist";

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
    monitoring: true,
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
  const row = findRow(id);
  if (row) row.remove();
  updateEmptyState();
}

function toggleMonitoring(id) {
  const list = loadWatchlist();
  const entry = list.find((e) => e.id === id);
  if (!entry) return;
  entry.monitoring = !entry.monitoring;
  saveWatchlist(list);

  const row = findRow(id);
  if (!row) return;
  const light = row.querySelector(".status-light");
  const toggle = row.querySelector(".status-toggle");
  light.classList.toggle("on", entry.monitoring);
  light.classList.toggle("off", !entry.monitoring);
  const label = entry.monitoring ? "Desativar monitoramento" : "Ativar monitoramento";
  toggle.setAttribute("aria-label", label);
  toggle.title = entry.monitoring
    ? "Monitorando (clique para desativar)"
    : "Não monitorando (clique para ativar)";
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
  nameRow.append(entry.itemName);
  const refineBadge = document.createElement("span");
  refineBadge.className = "refine-badge watchlist-refine";
  refineBadge.hidden = true;
  nameRow.appendChild(refineBadge);

  const pricesRow = document.createElement("div");
  pricesRow.className = "watchlist-prices";
  const target = document.createElement("span");
  target.className = "watchlist-target";
  target.textContent = "Alvo: " + (entry.targetPrice != null ? formatMoney(entry.targetPrice) : "—");
  const current = document.createElement("span");
  current.className = "watchlist-current";
  current.append("Atual: ");
  const spinner = document.createElement("span");
  spinner.className = "spinner";
  current.appendChild(spinner);

  pricesRow.appendChild(target);
  pricesRow.appendChild(current);
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
  return li;
}

async function fetchLivePrice(entry) {
  const row = findRow(entry.id);
  if (!row) return;
  const currentEl = row.querySelector(".watchlist-current");
  const refineEl = row.querySelector(".watchlist-refine");
  try {
    const url =
      "/web/watchlist/price?server=" + encodeURIComponent(entry.server) +
      "&itemId=" + encodeURIComponent(entry.itemId) +
      "&item=" + encodeURIComponent(entry.itemName);
    const res = await fetch(url);
    if (!res.ok) throw new Error("status " + res.status);
    const data = await res.json();
    if (!data.found) {
      currentEl.textContent = "Sem anúncios";
      return;
    }
    currentEl.textContent = "Atual: " + formatMoney(data.minPrice);
    if (data.refine !== undefined && data.refine !== null) {
      refineEl.hidden = false;
      refineEl.textContent = "+" + data.refine;
    }
  } catch {
    currentEl.textContent = "Indisponível";
  }
}

// renderWatchlist reconstrói o painel inteiro a partir do localStorage —
// usado só no carregamento da página. Ações do usuário (adicionar, remover,
// ligar/desligar) atualizam o DOM diretamente em vez de chamar isto, para
// não refazer a consulta de preço de itens que já estavam carregados.
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

document.addEventListener("DOMContentLoaded", renderWatchlist);
