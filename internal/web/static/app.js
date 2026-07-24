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
    const original = button.textContent;
    button.textContent = "Copiado!";
    setTimeout(() => {
      button.textContent = original;
    }, 1500);
  });
}
