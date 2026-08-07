// Barra de atividades fixada no rodapé: mostra em tempo real o que o
// client GnJoy (Go, compartilhado no servidor) está fazendo — buscas,
// consultas de detalhe, descoberta de rota — via um stream SSE
// (GET /web/activity/stream). Como o client é um único objeto no servidor,
// a atividade mostrada reflete TODAS as origens (busca da página, expandir
// uma loja, checagem de preço da watchlist, monitoramento periódico), não
// só o que a aba atual disparou.
//
// events guarda o histórico em ordem crescente de ID (mais antigo primeiro).
// O último item é sempre "a chamada atual" — mostrada na linha sempre
// visível do cabeçalho, com um spinner enquanto não há resultado. Os itens
// anteriores formam o histórico mostrado quando a barra está expandida, na
// mesma ordem cronológica (mais antigo no topo, "chamada atual" logo abaixo
// do fim da lista, já na linha do cabeçalho).
let activityEvents = [];

// STATUS_TAG mapeia um status final para a tag textual que substitui o
// cronômetro quando a chamada termina (ex.: "Consultando X" vira
// "Consultando X [OK]"). Não há entrada para "waiting"/"running": nesses
// casos o sufixo é o cronômetro (quando há um horário de disparo conhecido)
// ou nada (aguardando resposta, duração desconhecida).
const STATUS_TAG = {
  success: "[OK]",
  error: "[Erro]",
};

function activityStatusClass(status) {
  return "activity-status-" + status;
}

// formatCountdown mostra segundos simples ("7s") quando falta menos de um
// minuto (o caso comum, já que o rate limiter padrão é de ~1 req/s) e
// mm:ss para esperas maiores (ex.: backoff de retry após um 429).
function formatCountdown(remainingMs) {
  const totalSeconds = Math.max(0, Math.ceil(remainingMs / 1000));
  if (totalSeconds < 60) return totalSeconds + "s";
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return String(minutes).padStart(2, "0") + ":" + String(seconds).padStart(2, "0");
}

// activitySuffix devolve o que aparece logo depois do texto da chamada: o
// cronômetro regressivo enquanto ela aguarda para ser (re)disparada, a tag
// de status quando já terminou, ou nada enquanto está em voo aguardando
// resposta (duração desconhecida).
function activitySuffix(ev) {
  if (ev.status === "waiting" && ev.dispatchAt) {
    return formatCountdown(ev.dispatchAt - Date.now());
  }
  return STATUS_TAG[ev.status] || "";
}

function activityLineText(ev) {
  const suffix = activitySuffix(ev);
  return suffix ? ev.label + " " + suffix : ev.label;
}

// formatTime mostra o horário local de startedAt (HH:MM:SS) — o mesmo
// instante nas três fases de uma chamada (aguardando/em voo/concluída, ver
// StartedAt em internal/gnjoy/activity.go), então o horário de uma linha não
// muda enquanto ela avança de status. Segundos entram porque o rate limiter
// padrão dispara cerca de uma chamada por segundo — sem eles, várias linhas
// seguidas mostrariam o mesmo minuto.
function formatTime(ms) {
  return new Date(ms).toLocaleTimeString("pt-BR", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function renderCurrentLine() {
  const labelEl = document.getElementById("activity-current-label");
  const spinnerEl = document.getElementById("activity-spinner");
  const timeEl = document.getElementById("activity-current-time");
  if (!labelEl || !spinnerEl || !timeEl) return;

  const current = activityEvents[activityEvents.length - 1];
  labelEl.classList.remove(
    "activity-status-waiting",
    "activity-status-running",
    "activity-status-success",
    "activity-status-error"
  );

  if (!current) {
    labelEl.textContent = "Nenhuma atividade ainda.";
    labelEl.title = "";
    spinnerEl.hidden = true;
    timeEl.textContent = "";
    return;
  }

  labelEl.textContent = activityLineText(current);
  labelEl.classList.add(activityStatusClass(current.status));
  labelEl.title = current.status === "error" && current.error ? current.error : "";
  timeEl.textContent = formatTime(current.startedAt);

  spinnerEl.hidden = !(current.status === "waiting" || current.status === "running");
}

// renderHistory redesenha as linhas anteriores à atual. Preserva a posição
// de scroll do usuário: só reancora no fim da lista se ele já estava perto
// do fim (não atrapalha quem rolou pra cima pra ver chamadas antigas).
function renderHistory() {
  const list = document.getElementById("activity-history");
  if (!list) return;

  const wasNearBottom = list.scrollHeight - list.scrollTop - list.clientHeight < 16;

  list.innerHTML = "";
  const history = activityEvents.slice(0, -1);
  for (const ev of history) {
    const li = document.createElement("li");
    li.className = activityStatusClass(ev.status);
    if (ev.status === "error" && ev.error) li.title = ev.error;

    const textEl = document.createElement("span");
    textEl.className = "activity-history-text";
    textEl.textContent = activityLineText(ev);
    li.appendChild(textEl);

    const timeEl = document.createElement("span");
    timeEl.className = "activity-time";
    timeEl.textContent = formatTime(ev.startedAt);
    li.appendChild(timeEl);

    list.appendChild(li);
  }

  if (wasNearBottom) list.scrollTop = list.scrollHeight;
}

function renderActivity() {
  renderCurrentLine();
  renderHistory();
}

function upsertActivityEvent(ev) {
  const idx = activityEvents.findIndex((e) => e.id === ev.id);
  if (idx === -1) {
    activityEvents.push(ev);
  } else {
    activityEvents[idx] = ev;
  }
}

// connectActivityStream abre o EventSource; se a conexão cair, o próprio
// navegador reconecta sozinho, e o "snapshot" da reconexão resincroniza o
// estado (substituindo activityEvents inteiro, não só anexando).
function connectActivityStream() {
  const source = new EventSource("/web/activity/stream");

  source.addEventListener("snapshot", (msg) => {
    activityEvents = JSON.parse(msg.data);
    renderActivity();
  });

  source.addEventListener("update", (msg) => {
    upsertActivityEvent(JSON.parse(msg.data));
    renderActivity();
  });

  // Um handler só, e não um par "estado inicial"/"mudou": o payload é sempre
  // o estado completo, então o primeiro evento da conexão e os seguintes são
  // tratados do mesmo jeito. É também o que faz uma reconexão do EventSource
  // resincronizar o aviso sozinha.
  source.addEventListener("suspension", (msg) => {
    applySuspension(JSON.parse(msg.data));
  });
}

// applySuspension liga e desliga o modo suspenso da página: o aviso no topo e
// o travamento dos controles que falariam com o site.
//
// Travar a interface não é o que impede as requisições — quem impede é o
// servidor, que recusa tudo na porta enquanto estiver suspenso. Aqui é para o
// usuário não ficar clicando em coisas que não vão funcionar.
function applySuspension(state) {
  const suspended = Boolean(state && state.suspended);
  document.documentElement.dataset.suspended = suspended ? "1" : "";

  const banner = document.getElementById("suspension-banner");
  if (banner) banner.hidden = !suspended;

  const detail = document.getElementById("suspension-detail");
  if (detail && suspended && state.since) {
    const desde = new Date(state.since).toLocaleTimeString("pt-BR", { hour: "2-digit", minute: "2-digit" });
    detail.textContent =
      "As buscas e a watchlist estão pausadas desde " + desde +
      ". O programa verifica sozinho de tempos em tempos e volta ao normal assim que o site liberar.";
  }

  document.querySelectorAll(".search-form input, .search-form select, .search-form button")
    .forEach((el) => { el.disabled = suspended; });

  // A watchlist inteira: nada de consultar preço, editar alvo ou ligar o
  // monitoramento de um item enquanto não há como consultar o mercado.
  document.querySelectorAll("#watchlist-list button")
    .forEach((el) => { el.disabled = suspended; });

  if (typeof setWatchlistSuspended === "function") setWatchlistSuspended(suspended);
}

document.addEventListener("DOMContentLoaded", () => {
  const bar = document.getElementById("activity-bar");
  if (bar) {
    const toggle = () => {
      const expanded = bar.classList.toggle("expanded");
      bar.setAttribute("aria-expanded", String(expanded));
      if (expanded) {
        const list = document.getElementById("activity-history");
        if (list) list.scrollTop = list.scrollHeight;
      }
    };
    bar.addEventListener("click", toggle);
    bar.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter" || ev.key === " ") {
        ev.preventDefault();
        toggle();
      }
    });
  }

  connectActivityStream();
  // Mantém os cronômetros (linha atual e, no caso raro de chamadas
  // concorrentes, linhas do histórico) em dia mesmo sem novos eventos.
  setInterval(renderActivity, 1000);
});
