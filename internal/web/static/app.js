// Utilitários compartilhados pela página inteira. Este arquivo é carregado
// nas duas abas, antes de watchlist.js e estoque.js (ver layout.html.tmpl),
// e é onde mora o que as duas telas usam — em vez de uma delas ter que
// importar da outra.

// formatMoney escreve um preço em zeny no padrão da comunidade de Ragnarok
// Online: separador de milhar e o sufixo "z".
function formatMoney(n) {
  return n.toLocaleString("pt-BR") + " z";
}

// cssEscape prepara um valor para entrar num seletor de atributo. O fallback
// existe para navegadores sem CSS.escape: escapar aspas e barras cobre o que
// um id nosso pode conter, já que eles são opacos de propósito.
function cssEscape(value) {
  return window.CSS && CSS.escape ? CSS.escape(String(value)) : String(value).replace(/["\\]/g, "\\$&");
}

// showToast mostra um aviso passageiro no canto inferior direito. É o único
// canal de aviso que não depende de permissão nenhuma do navegador — por isso
// ele sempre acompanha os outros (som, notificação nativa, Telegram), nunca
// os substitui.
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

// toggleRow mostra/esconde a linha de detalhe logo abaixo da linha clicada.
// A busca ao servidor (hx-get) só acontece uma vez, na primeira vez que a
// linha é expandida ("hx-trigger=click once"); depois disso, cliques
// subsequentes só alternam a visibilidade do que já foi carregado, sem
// refazer a requisição.
function toggleRow(row) {
  const detail = row.nextElementSibling;
  if (!detail) return;
  const open = detail.classList.toggle("open");
  const icon = row.querySelector(".expand-icon");
  if (icon) icon.textContent = open ? "▾" : "▸";
}

function copyNavi(button, command) {
  navigator.clipboard.writeText(command).then(() => {
    // innerHTML, e não textContent: a versão da watchlist tem um ícone de
    // prancheta dentro do botão (ver buildClipboardIcon em watchlist.js), e
    // textContent apagaria esse ícone ao restaurar.
    const original = button.innerHTML;
    button.textContent = "Copiado!";
    setTimeout(() => {
      button.innerHTML = original;
    }, 1500);
  });
}
