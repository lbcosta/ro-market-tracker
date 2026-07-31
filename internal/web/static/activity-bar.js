// Barra de atividades fixada no rodapé da página: começa recolhida
// (só o cabeçalho aparece) e clicar em qualquer ponto dela — cabeçalho ou
// corpo — alterna para expandida (o corpo cresce para cima, acima do
// cabeçalho, que permanece fixo no rodapé).
document.addEventListener("DOMContentLoaded", () => {
  const bar = document.getElementById("activity-bar");
  if (!bar) return;

  function toggle() {
    const expanded = bar.classList.toggle("expanded");
    bar.setAttribute("aria-expanded", String(expanded));
  }

  bar.addEventListener("click", toggle);
  bar.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter" || ev.key === " ") {
      ev.preventDefault();
      toggle();
    }
  });
});
