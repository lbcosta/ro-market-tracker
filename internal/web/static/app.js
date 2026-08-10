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
